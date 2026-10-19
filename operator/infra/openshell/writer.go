// Package openshell converges the credential records a KapeAgentServer renders
// into the OpenShell gateway (spec #261 M3, ADR-0088).
//
// It is the operator's only outbound path for secret material: values go in as
// provider credentials and never come back out — the gateway redacts them on
// read, and nothing here logs them.
package openshell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1 "github.com/dzungtr/kape/controller/gen"
	"github.com/kape-io/kape/operator/domain/agentserver"
)

// providerGateway is the v0.1.0 provider surface the writer uses.
// *v1.OpenShellClient satisfies it; tests fake it to pin call ordering and retry
// behaviour without a gateway.
type providerGateway interface {
	LintProviderProfiles(ctx context.Context, in *v1.LintProviderProfilesRequest, opts ...grpc.CallOption) (*v1.LintProviderProfilesResponse, error)
	GetProviderProfile(ctx context.Context, in *v1.GetProviderProfileRequest, opts ...grpc.CallOption) (*v1.ProviderProfileResponse, error)
	ImportProviderProfiles(ctx context.Context, in *v1.ImportProviderProfilesRequest, opts ...grpc.CallOption) (*v1.ImportProviderProfilesResponse, error)
	GetProvider(ctx context.Context, in *v1.GetProviderRequest, opts ...grpc.CallOption) (*v1.ProviderResponse, error)
	CreateProvider(ctx context.Context, in *v1.CreateProviderRequest, opts ...grpc.CallOption) (*v1.ProviderResponse, error)
	UpdateProvider(ctx context.Context, in *v1.UpdateProviderRequest, opts ...grpc.CallOption) (*v1.ProviderResponse, error)
}

// Retry policy for gateway conflicts: bounded exponential backoff, applied to
// codes the gateway returns for concurrent or transient conditions.
const (
	defaultAttempts = 3
	defaultBackoff  = 100 * time.Millisecond
)

// lintErrorSeverity is the diagnostic severity that fails a profile document.
const lintErrorSeverity = "error"

// Writer imports provider profiles and converges provider records on the
// gateway. One Writer is shared by every KapeAgentServer reconcile.
type Writer struct {
	gateway   providerGateway
	workspace string
	attempts  int
	backoff   time.Duration
	sleep     func(context.Context, time.Duration)

	mu sync.Mutex
	// pushed holds the fingerprint of the credential material this process last
	// wrote per provider. The gateway redacts credential values on read
	// (verified: a created credential reads back as an opaque placeholder), so
	// value drift cannot be discovered from the gateway — it is derived from what
	// was pushed. A restart empties the cache, and the next reconcile re-pushes
	// every credential: safe, and it converges on the Secret's current values.
	pushed map[string]string
}

// NewWriter creates a Writer over an established gateway connection. workspace
// is the OpenShell workspace providers are created in; empty means "default".
func NewWriter(conn grpc.ClientConnInterface, workspace string) *Writer {
	return newWriter(v1.NewOpenShellClient(conn), workspace)
}

func newWriter(gateway providerGateway, workspace string) *Writer {
	if workspace == "" {
		workspace = "default"
	}
	return &Writer{
		gateway:   gateway,
		workspace: workspace,
		attempts:  defaultAttempts,
		backoff:   defaultBackoff,
		sleep:     sleepContext,
		pushed:    map[string]string{},
	}
}

// Apply converges the gateway for one KapeAgentServer: lint and import the
// provider profiles, then create or update each provider record. Ordering is
// import → create/update, and only the imports a record's provider type needs.
//
// Per-credential failures ride the returned outcomes (degrade-loud, D9); the
// error is non-nil only when the gateway itself is unreachable, which the
// reconcile retries with backoff.
func (w *Writer) Apply(ctx context.Context, records []agentserver.OpenShellRecord) ([]agentserver.CredentialOutcome, error) {
	outcomes := make([]agentserver.CredentialOutcome, len(records))
	profiles := map[string]agentserver.ProviderProfile{}
	var profileOrder []string
	pending := make([]int, 0, len(records))

	for i, rec := range records {
		profile, ok := agentserver.Profile(rec.ProviderType)
		if !ok {
			outcomes[i] = agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
				Reason:  agentserver.ReasonUnknownProviderType,
				Message: fmt.Sprintf("providerType %q has no shipped provider profile", rec.ProviderType),
			})
			continue
		}
		if missing := profile.MissingCredentialKeys(rec.Values); len(missing) > 0 {
			outcomes[i] = agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
				Reason:  agentserver.ReasonMissingCredentialKey,
				Message: fmt.Sprintf("Secret for credential %q is missing required key(s): %s", rec.Credential, strings.Join(missing, ", ")),
			})
			continue
		}
		if _, seen := profiles[profile.ID]; !seen {
			profiles[profile.ID] = profile
			profileOrder = append(profileOrder, profile.ID)
		}
		pending = append(pending, i)
	}
	if len(pending) == 0 {
		return outcomes, nil
	}

	profileFailures, err := w.prepareProfiles(ctx, profileOrder, profiles)
	if err != nil {
		for _, i := range pending {
			rec := records[i]
			outcomes[i] = agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
				Reason:  agentserver.ReasonGatewayUnavailable,
				Message: fmt.Sprintf("OpenShell gateway unreachable: %s", grpcstatus.Convert(err).Message()),
			})
		}
		return outcomes, err
	}

	var gatewayErr error
	for _, i := range pending {
		rec := records[i]
		if failure, failed := profileFailures[rec.ProviderType]; failed {
			outcomes[i] = agentserver.FailedOutcome(rec.Credential, rec.Name, failure)
			continue
		}
		outcome, err := w.applyRecord(ctx, rec)
		outcomes[i] = outcome
		if err != nil && gatewayErr == nil {
			gatewayErr = err
		}
	}
	return outcomes, gatewayErr
}

// prepareProfiles makes sure the gateway holds every profile document the
// records need: existing documents are left alone, missing ones are linted and
// then imported.
//
// Linting an already-registered profile is not a validation — the gateway
// answers with an `already exists` error diagnostic (verified live against
// v0.1.0) — so existence is checked first and only new documents are linted.
func (w *Writer) prepareProfiles(
	ctx context.Context,
	order []string,
	profiles map[string]agentserver.ProviderProfile,
) (map[string]agentserver.CredentialFailure, error) {
	failures := map[string]agentserver.CredentialFailure{}
	var missing []string
	for _, id := range order {
		exists, err := w.profileExists(ctx, profiles[id])
		if err != nil {
			if isTransient(err) {
				return nil, fmt.Errorf("reading provider profile %q: %w", id, err)
			}
			failures[id] = agentserver.CredentialFailure{
				Reason:  agentserver.ReasonGatewayRejected,
				Message: fmt.Sprintf("gateway rejected provider profile %q: %s", id, grpcstatus.Convert(err).Message()),
			}
			continue
		}
		if !exists {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return failures, nil
	}

	items := make([]*v1.ProviderProfileImportItem, 0, len(missing))
	for _, id := range missing {
		items = append(items, profileDocument(profiles[id]))
	}

	var lint *v1.LintProviderProfilesResponse
	if err := w.withRetry(ctx, func() error {
		var err error
		// No workspace_scope: the operator imports platform-scoped profiles, so
		// providers in every workspace resolve them without profile_workspace.
		lint, err = w.gateway.LintProviderProfiles(ctx, &v1.LintProviderProfilesRequest{Profiles: items})
		return err
	}); err != nil {
		return nil, fmt.Errorf("linting provider profiles: %w", err)
	}
	for _, d := range lint.GetDiagnostics() {
		if d.GetSeverity() != lintErrorSeverity {
			continue
		}
		if _, seen := failures[d.GetProfileId()]; seen {
			continue
		}
		failures[d.GetProfileId()] = agentserver.CredentialFailure{
			Reason:  agentserver.ReasonProfileLintFailed,
			Message: fmt.Sprintf("provider profile %q failed lint: %s", d.GetProfileId(), d.GetMessage()),
		}
	}

	for _, id := range missing {
		if _, failed := failures[id]; failed {
			continue
		}
		if err := w.importProfile(ctx, profiles[id]); err != nil {
			if isTransient(err) {
				return nil, fmt.Errorf("importing provider profile %q: %w", id, err)
			}
			failures[id] = agentserver.CredentialFailure{
				Reason:  agentserver.ReasonGatewayRejected,
				Message: fmt.Sprintf("gateway rejected provider profile %q: %s", id, grpcstatus.Convert(err).Message()),
			}
		}
	}
	return failures, nil
}

// profileExists reports whether the gateway already holds the document. Profile
// documents are operator-owned code, so an existing one is never rewritten.
func (w *Writer) profileExists(ctx context.Context, profile agentserver.ProviderProfile) (bool, error) {
	var found bool
	err := w.withRetry(ctx, func() error {
		_, err := w.gateway.GetProviderProfile(ctx, &v1.GetProviderProfileRequest{Id: profile.ID})
		if grpcstatus.Code(err) == grpccodes.NotFound {
			found = false
			return nil
		}
		found = err == nil
		return err
	})
	return found, err
}

// importProfile registers one document.
func (w *Writer) importProfile(ctx context.Context, profile agentserver.ProviderProfile) error {
	return w.withRetry(ctx, func() error {
		_, err := w.gateway.ImportProviderProfiles(ctx, &v1.ImportProviderProfilesRequest{
			Profiles: []*v1.ProviderProfileImportItem{profileDocument(profile)},
		})
		if err != nil && strings.Contains(grpcstatus.Convert(err).Message(), "already exists") {
			// A concurrent reconcile imported it between the lookup and the
			// import; the document is operator-owned, so this is success.
			return nil
		}
		return err
	})
}

// applyRecord creates the provider record when it is missing and updates it when
// its type or credential values drifted. Rotation is that update:
// RotateProviderCredential only refreshes gateway-owned refresh material and
// answers NotFound ("provider refresh state not found") for operator-pushed
// credentials on this gateway.
func (w *Writer) applyRecord(ctx context.Context, rec agentserver.OpenShellRecord) (agentserver.CredentialOutcome, error) {
	var existing *v1.Provider
	err := w.withRetry(ctx, func() error {
		resp, err := w.gateway.GetProvider(ctx, &v1.GetProviderRequest{WorkspaceScope: w.scope(), Name: rec.Name})
		if err != nil {
			return err
		}
		existing = resp.GetProvider()
		return nil
	})

	switch {
	case grpcstatus.Code(err) == grpccodes.NotFound:
		err := w.withRetry(ctx, func() error {
			_, err := w.gateway.CreateProvider(ctx, &v1.CreateProviderRequest{
				WorkspaceScope: w.scope(),
				Provider:       providerRecord(rec),
			})
			return err
		})
		if err != nil {
			return w.writeFailure(rec, "create", err)
		}
		w.rememberPush(rec)
		return agentserver.PushedOutcome(rec), nil

	case err != nil:
		return w.writeFailure(rec, "read", err)
	}

	if existing.GetType() == rec.ProviderType && !w.pushOutstanding(rec) {
		return agentserver.PushedOutcome(rec), nil
	}

	err = w.withRetry(ctx, func() error {
		_, err := w.gateway.UpdateProvider(ctx, &v1.UpdateProviderRequest{
			WorkspaceScope: w.scope(),
			Provider:       providerRecord(rec),
		})
		return err
	})
	if err != nil {
		return w.writeFailure(rec, "update", err)
	}
	w.rememberPush(rec)
	return agentserver.PushedOutcome(rec), nil
}

// writeFailure classifies a failed write: transient conditions are surfaced as an
// error so the reconcile retries, everything else is a named per-credential
// failure that degrades only this credential.
func (w *Writer) writeFailure(rec agentserver.OpenShellRecord, verb string, err error) (agentserver.CredentialOutcome, error) {
	message := grpcstatus.Convert(err).Message()
	if isTransient(err) {
		return agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
			Reason:  agentserver.ReasonGatewayUnavailable,
			Message: fmt.Sprintf("OpenShell gateway unreachable (%s provider %q): %s", verb, rec.Name, message),
		}), err
	}
	return agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
		Reason:  agentserver.ReasonGatewayRejected,
		Message: fmt.Sprintf("gateway rejected %s of provider %q: %s", verb, rec.Name, message),
	}), nil
}

// pushOutstanding reports whether this process has not yet written the record's
// current credential values.
func (w *Writer) pushOutstanding(rec agentserver.OpenShellRecord) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pushed[rec.Name] != fingerprint(rec.Values)
}

// rememberPush records the fingerprint of the values just written.
func (w *Writer) rememberPush(rec agentserver.OpenShellRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pushed[rec.Name] = fingerprint(rec.Values)
}

// scope is the workspace every provider RPC is scoped to (v0.1.0 requires a
// named workspace selection).
func (w *Writer) scope() *v1.WorkspaceSelector {
	return &v1.WorkspaceSelector{Selection: &v1.WorkspaceSelector_Workspace{Workspace: w.workspace}}
}

// withRetry runs call, retrying gateway conflicts and transient unavailability
// with bounded exponential backoff.
func (w *Writer) withRetry(ctx context.Context, call func() error) error {
	backoff := w.backoff
	err := call()
	for attempt := 1; attempt < w.attempts && retryable(err); attempt++ {
		w.sleep(ctx, backoff)
		backoff *= 2
		err = call()
	}
	return err
}

// retryable reports the gateway codes a reconcile retries: concurrent updates,
// throttling, and transport unavailability.
func retryable(err error) bool {
	switch grpcstatus.Code(err) {
	case grpccodes.Aborted, grpccodes.ResourceExhausted, grpccodes.Unavailable:
		return true
	}
	return false
}

// isTransient reports whether err is a transport-level condition rather than a
// verdict on the request.
func isTransient(err error) bool {
	switch grpcstatus.Code(err) {
	case grpccodes.Unavailable, grpccodes.DeadlineExceeded, grpccodes.Canceled, grpccodes.ResourceExhausted:
		return true
	}
	return false
}

// sleepContext waits for d or for ctx to be done.
func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// fingerprint hashes credential keys and values for in-memory drift detection.
// The digest is never logged, never persisted, and never leaves the process.
func fingerprint(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(values[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// providerRecord is the provider payload for one credential: name, profile type,
// and the credential values themselves (the only place they are written).
func providerRecord(rec agentserver.OpenShellRecord) *v1.Provider {
	values := make(map[string]string, len(rec.Values))
	for k, v := range rec.Values {
		values[k] = v
	}
	return &v1.Provider{
		Metadata:    &v1.ObjectMeta{Name: rec.Name},
		Type:        rec.ProviderType,
		Credentials: values,
	}
}

// profileDocument maps a shipped profile to the import payload.
func profileDocument(profile agentserver.ProviderProfile) *v1.ProviderProfileImportItem {
	doc := &v1.ProviderProfile{
		Id:          profile.ID,
		DisplayName: profile.DisplayName,
		Description: profile.Description,
		Category:    profileCategory(profile.Category),
	}
	for _, cred := range profile.Credentials {
		doc.Credentials = append(doc.Credentials, &v1.ProviderProfileCredential{
			Name:        cred.Name,
			Description: cred.Description,
			Required:    cred.Required,
			AuthStyle:   cred.AuthStyle,
			HeaderName:  cred.HeaderName,
		})
	}
	for _, bin := range profile.Binaries {
		doc.Binaries = append(doc.Binaries, &v1.NetworkBinary{Path: bin.Path})
	}
	return &v1.ProviderProfileImportItem{Profile: doc, Source: "kape-operator"}
}

// profileCategory maps a document category string onto the gateway enum.
func profileCategory(category string) v1.ProviderProfileCategory {
	switch category {
	case agentserver.CategorySourceControl:
		return v1.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_SOURCE_CONTROL
	default:
		return v1.ProviderProfileCategory_PROVIDER_PROFILE_CATEGORY_OTHER
	}
}
