package openshell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1 "github.com/dzungtr/kape/controller/gen"
	"github.com/kape-io/kape/operator/domain/agentserver"
)

// fakeGateway is a scripted providerGateway: it records the call sequence,
// stores provider records the way the gateway does (credential values are
// write-only — a read returns the keys with a redacted value), and injects
// failures on demand.
type fakeGateway struct {
	calls      []string
	profiles   map[string]bool
	providers  map[string]*v1.Provider
	diagnostic []*v1.ProviderProfileDiagnostic
	// fail maps a method name to a function returning the error for the given
	// 1-based attempt number; nil means success.
	fail map[string]func(attempt int) error
	// attempts counts calls per method.
	attempts map[string]int
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{
		profiles:  map[string]bool{},
		providers: map[string]*v1.Provider{},
		fail:      map[string]func(int) error{},
		attempts:  map[string]int{},
	}
}

func (f *fakeGateway) begin(method string) error {
	f.calls = append(f.calls, method)
	f.attempts[method]++
	if fn, ok := f.fail[method]; ok && fn != nil {
		return fn(f.attempts[method])
	}
	return nil
}

func (f *fakeGateway) LintProviderProfiles(_ context.Context, in *v1.LintProviderProfilesRequest, _ ...grpc.CallOption) (*v1.LintProviderProfilesResponse, error) {
	if err := f.begin("LintProviderProfiles"); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(in.GetProfiles()))
	for _, item := range in.GetProfiles() {
		ids = append(ids, item.GetProfile().GetId())
	}
	resp := &v1.LintProviderProfilesResponse{Valid: len(f.diagnostic) == 0}
	for _, id := range ids {
		// The real gateway fails a lint of an already-registered profile with an
		// "already exists" diagnostic; the fake mirrors that so the writer's
		// existence check stays honest.
		if f.profiles[id] {
			resp.Valid = false
			resp.Diagnostics = append(resp.Diagnostics, &v1.ProviderProfileDiagnostic{
				ProfileId: id,
				Field:     "id",
				Message:   "custom provider profile '" + id + "' already exists",
				Severity:  "error",
			})
		}
	}
	for _, d := range f.diagnostic {
		if len(ids) > 0 && d.GetProfileId() == "" {
			d.ProfileId = ids[0]
		}
		resp.Diagnostics = append(resp.Diagnostics, d)
	}
	return resp, nil
}

func (f *fakeGateway) GetProviderProfile(_ context.Context, in *v1.GetProviderProfileRequest, _ ...grpc.CallOption) (*v1.ProviderProfileResponse, error) {
	if err := f.begin("GetProviderProfile"); err != nil {
		return nil, err
	}
	if !f.profiles[in.GetId()] {
		return nil, grpcstatus.Error(grpccodes.NotFound, "provider profile not found")
	}
	return &v1.ProviderProfileResponse{Profile: &v1.ProviderProfile{Id: in.GetId()}}, nil
}

func (f *fakeGateway) ImportProviderProfiles(_ context.Context, in *v1.ImportProviderProfilesRequest, _ ...grpc.CallOption) (*v1.ImportProviderProfilesResponse, error) {
	if err := f.begin("ImportProviderProfiles"); err != nil {
		return nil, err
	}
	for _, item := range in.GetProfiles() {
		f.profiles[item.GetProfile().GetId()] = true
	}
	return &v1.ImportProviderProfilesResponse{Imported: true}, nil
}

func (f *fakeGateway) GetProvider(_ context.Context, in *v1.GetProviderRequest, _ ...grpc.CallOption) (*v1.ProviderResponse, error) {
	if err := f.begin("GetProvider"); err != nil {
		return nil, err
	}
	provider, ok := f.providers[in.GetName()]
	if !ok {
		return nil, grpcstatus.Error(grpccodes.NotFound, "provider not found")
	}
	read := &v1.Provider{
		Metadata:    &v1.ObjectMeta{Name: provider.GetMetadata().GetName()},
		Type:        provider.GetType(),
		Credentials: map[string]string{},
	}
	// The gateway never returns credential values: the read-back carries the
	// keys with an opaque placeholder.
	for key := range provider.GetCredentials() {
		read.Credentials[key] = "redacted"
	}
	return &v1.ProviderResponse{Provider: read}, nil
}

func (f *fakeGateway) CreateProvider(_ context.Context, in *v1.CreateProviderRequest, _ ...grpc.CallOption) (*v1.ProviderResponse, error) {
	if err := f.begin("CreateProvider"); err != nil {
		return nil, err
	}
	name := in.GetProvider().GetMetadata().GetName()
	if _, exists := f.providers[name]; exists {
		return nil, grpcstatus.Error(grpccodes.AlreadyExists, "provider already exists")
	}
	f.providers[name] = copyProvider(in.GetProvider())
	return &v1.ProviderResponse{Provider: f.providers[name]}, nil
}

func (f *fakeGateway) UpdateProvider(_ context.Context, in *v1.UpdateProviderRequest, _ ...grpc.CallOption) (*v1.ProviderResponse, error) {
	if err := f.begin("UpdateProvider"); err != nil {
		return nil, err
	}
	name := in.GetProvider().GetMetadata().GetName()
	if _, exists := f.providers[name]; !exists {
		return nil, grpcstatus.Error(grpccodes.NotFound, "provider not found")
	}
	f.providers[name] = copyProvider(in.GetProvider())
	return &v1.ProviderResponse{Provider: f.providers[name]}, nil
}

func copyProvider(p *v1.Provider) *v1.Provider {
	values := make(map[string]string, len(p.GetCredentials()))
	for k, v := range p.GetCredentials() {
		values[k] = v
	}
	return &v1.Provider{
		Metadata:    &v1.ObjectMeta{Name: p.GetMetadata().GetName()},
		Type:        p.GetType(),
		Credentials: values,
	}
}

// testRecord is one rendered credential: git-https, github/api.github.com.
func testRecord(name, token string) agentserver.OpenShellRecord {
	return agentserver.OpenShellRecord{
		Credential:   name,
		Name:         "kape-kape-system-sre-" + name,
		ProviderType: "git-https",
		HostPatterns: []string{"github.com:443", "api.github.com:443"},
		Values:       map[string]string{"token": token},
	}
}

// newTestWriter builds a Writer over the fake gateway with sleep stubbed out.
func newTestWriter(gateway providerGateway) (*Writer, *[]time.Duration) {
	w := newWriter(gateway, "default")
	sleeps := &[]time.Duration{}
	w.sleep = func(_ context.Context, d time.Duration) { *sleeps = append(*sleeps, d) }
	return w, sleeps
}

// TestWriter_ApplyImportsBeforeCreating pins the ordering invariant: profile
// lint → profile import → provider create.
func TestWriter_ApplyImportsBeforeCreating(t *testing.T) {
	gateway := newFakeGateway()
	writer, _ := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_value")})
	require.NoError(t, err)

	require.Len(t, outcomes, 1)
	assert.True(t, outcomes[0].Pushed, outcomes[0].Message)
	assert.Equal(t, agentserver.Pushed, outcomes[0].Reason)
	assert.Equal(t, "kape-kape-system-sre-github", outcomes[0].Provider)
	assert.Equal(t, []string{
		"GetProviderProfile",
		"LintProviderProfiles",
		"ImportProviderProfiles",
		"GetProvider",
		"CreateProvider",
	}, gateway.calls)
	require.Contains(t, gateway.providers, "kape-kape-system-sre-github")
	assert.Equal(t, "git-https", gateway.providers["kape-kape-system-sre-github"].GetType())
	assert.Equal(t, "ghp_value", gateway.providers["kape-kape-system-sre-github"].GetCredentials()["token"])
}

// TestWriter_ApplyIsIdempotent: a second apply of unchanged credentials performs
// no write, and a changed value becomes an update (the rotation path).
func TestWriter_ApplyIsIdempotent(t *testing.T) {
	gateway := newFakeGateway()
	writer, _ := newTestWriter(gateway)
	ctx := context.Background()

	_, err := writer.Apply(ctx, []agentserver.OpenShellRecord{testRecord("github", "ghp_value_1")})
	require.NoError(t, err)

	gateway.calls = nil
	_, err = writer.Apply(ctx, []agentserver.OpenShellRecord{testRecord("github", "ghp_value_1")})
	require.NoError(t, err)
	assert.Equal(t, []string{"GetProviderProfile", "GetProvider"}, gateway.calls,
		"re-apply of an unchanged credential only reads")

	gateway.calls = nil
	outcomes, err := writer.Apply(ctx, []agentserver.OpenShellRecord{testRecord("github", "ghp_value_2")})
	require.NoError(t, err)
	assert.True(t, outcomes[0].Pushed)
	assert.Equal(t, []string{"GetProviderProfile", "GetProvider", "UpdateProvider"}, gateway.calls)
	assert.Equal(t, "ghp_value_2", gateway.providers["kape-kape-system-sre-github"].GetCredentials()["token"])
}

// TestWriter_ApplyRetriesConflictsWithBackoff: concurrent-update conflicts are
// retried with exponential backoff instead of degrading the credential.
func TestWriter_ApplyRetriesConflictsWithBackoff(t *testing.T) {
	gateway := newFakeGateway()
	gateway.fail["CreateProvider"] = func(attempt int) error {
		if attempt < 3 {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent provider mutation")
		}
		return nil
	}
	writer, sleeps := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_value")})
	require.NoError(t, err)
	assert.True(t, outcomes[0].Pushed)
	assert.Equal(t, 3, gateway.attempts["CreateProvider"])
	assert.Equal(t, []time.Duration{defaultBackoff, 2 * defaultBackoff}, *sleeps)
}

// TestWriter_ApplyDegradesOnlyTheBrokenCredential: one rejected provider never
// blocks the others (D9).
func TestWriter_ApplyDegradesOnlyTheBrokenCredential(t *testing.T) {
	gateway := newFakeGateway()
	gateway.fail["CreateProvider"] = func(attempt int) error {
		if gateway.attempts["CreateProvider"] == 1 {
			return grpcstatus.Error(grpccodes.InvalidArgument, "provider rejected")
		}
		return nil
	}
	writer, _ := newTestWriter(gateway)

	records := []agentserver.OpenShellRecord{testRecord("broken", "ghp_broken"), testRecord("github", "ghp_value")}
	outcomes, err := writer.Apply(context.Background(), records)
	require.NoError(t, err, "a rejected credential is a per-credential outcome, not a reconcile error")

	require.Len(t, outcomes, 2)
	assert.False(t, outcomes[0].Pushed)
	assert.Equal(t, agentserver.ReasonGatewayRejected, outcomes[0].Reason)
	assert.True(t, outcomes[1].Pushed)
}

// TestWriter_ApplyRejectsUnknownProviderType: no shipped profile document means
// no provider, reported and never guessed.
func TestWriter_ApplyRejectsUnknownProviderType(t *testing.T) {
	gateway := newFakeGateway()
	writer, _ := newTestWriter(gateway)

	record := testRecord("gitlab", "value")
	record.ProviderType = "gitlab-ssh"
	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{record})
	require.NoError(t, err)

	require.Len(t, outcomes, 1)
	assert.False(t, outcomes[0].Pushed)
	assert.Equal(t, agentserver.ReasonUnknownProviderType, outcomes[0].Reason)
	assert.Empty(t, gateway.calls, "nothing reaches the gateway for an unknown provider type")
}

// TestWriter_ApplyRejectsMissingCredentialKey: the Secret must carry the keys
// the profile requires — the message names the key, never a value.
func TestWriter_ApplyRejectsMissingCredentialKey(t *testing.T) {
	gateway := newFakeGateway()
	writer, _ := newTestWriter(gateway)

	record := testRecord("github", "")
	record.Values = map[string]string{"other": "value"}
	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{record})
	require.NoError(t, err)

	require.Len(t, outcomes, 1)
	assert.False(t, outcomes[0].Pushed)
	assert.Equal(t, agentserver.ReasonMissingCredentialKey, outcomes[0].Reason)
	assert.Contains(t, outcomes[0].Message, "token")
	assert.NotContains(t, outcomes[0].Message, "value")
	assert.Empty(t, gateway.calls)
}

// TestWriter_ApplyFailsCredentialsWhenProfileLintFails: an invalid document stops
// the imports and the creates for its provider type.
func TestWriter_ApplyFailsCredentialsWhenProfileLintFails(t *testing.T) {
	gateway := newFakeGateway()
	gateway.diagnostic = []*v1.ProviderProfileDiagnostic{{
		Field:    "endpoints",
		Message:  "at least one credential is required",
		Severity: "error",
	}}
	writer, _ := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_value")})
	require.NoError(t, err)

	require.Len(t, outcomes, 1)
	assert.False(t, outcomes[0].Pushed)
	assert.Equal(t, agentserver.ReasonProfileLintFailed, outcomes[0].Reason)
	assert.Equal(t, []string{"GetProviderProfile", "LintProviderProfiles"}, gateway.calls)
}

// TestWriter_ApplyReportsUnreachableGateway: a transport failure is a reconcile
// error (retry with backoff), and every credential is named as unavailable —
// never silently skipped.
func TestWriter_ApplyReportsUnreachableGateway(t *testing.T) {
	gateway := newFakeGateway()
	gateway.fail["LintProviderProfiles"] = func(int) error {
		return grpcstatus.Error(grpccodes.Unavailable, "connection refused")
	}
	writer, sleeps := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_value")})
	require.Error(t, err)
	assert.Len(t, *sleeps, defaultAttempts-1, "unavailable is retried before it is reported")
	require.Len(t, outcomes, 1)
	assert.False(t, outcomes[0].Pushed)
	assert.Equal(t, agentserver.ReasonGatewayUnavailable, outcomes[0].Reason)
}

// TestWriter_ApplyReusesImportedProfile: an existing document is neither
// re-imported (the gateway answers "already exists") nor re-linted (the lint RPC
// answers "already exists" too — verified live on v0.1.0).
func TestWriter_ApplyReusesImportedProfile(t *testing.T) {
	gateway := newFakeGateway()
	gateway.profiles["git-https"] = true
	writer, _ := newTestWriter(gateway)

	_, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_value")})
	require.NoError(t, err)
	assert.NotContains(t, gateway.calls, "ImportProviderProfiles")
	assert.NotContains(t, gateway.calls, "LintProviderProfiles")
}

// TestWriter_ApplyNeverLogsValues guards the D7 invariant on the strings the
// writer produces: no value appears in any outcome message.
func TestWriter_ApplyNeverLogsValues(t *testing.T) {
	gateway := newFakeGateway()
	gateway.fail["CreateProvider"] = func(int) error {
		return grpcstatus.Error(grpccodes.InvalidArgument, "provider rejected")
	}
	writer, _ := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), []agentserver.OpenShellRecord{testRecord("github", "ghp_super_secret")})
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	assert.NotContains(t, outcomes[0].Message, "ghp_super_secret")
	assert.NotContains(t, strings.Join(gateway.calls, ","), "ghp_super_secret")
}

// TestWriter_ApplyNoRecords: an empty credential list makes no gateway calls.
func TestWriter_ApplyNoRecords(t *testing.T) {
	gateway := newFakeGateway()
	writer, _ := newTestWriter(gateway)

	outcomes, err := writer.Apply(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, outcomes)
	assert.Empty(t, gateway.calls)
}

// TestFingerprint covers the in-memory drift detector: order-independent, and
// sensitive to both keys and values.
func TestFingerprint(t *testing.T) {
	assert.Equal(t, fingerprint(map[string]string{"a": "1", "b": "2"}), fingerprint(map[string]string{"b": "2", "a": "1"}))
	assert.NotEqual(t, fingerprint(map[string]string{"a": "1"}), fingerprint(map[string]string{"a": "2"}))
	assert.NotEqual(t, fingerprint(map[string]string{"a": "1"}), fingerprint(map[string]string{"b": "1"}))
	assert.NotEmpty(t, fingerprint(nil), "an empty credential set still hashes")
}
