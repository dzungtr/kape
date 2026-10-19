package main

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// CredentialBindingRuleName is the network policy rule the merged credential
// bindings live under (issue #264, ADR-0088). It is a sibling of the
// model-gateway rule, never a replacement: the binding rule's binaries
// (git/gh/pi) are scoped to the bound endpoints only, so credential-bearing
// egress cannot widen the model-gateway rule's binary set.
const CredentialBindingRuleName = "m1_credential_bindings"

// credentialBinaryPaths are the calling binaries allowlisted on bound
// endpoints. OPA denies egress for binaries a rule does not list (M1 spike
// finding), so a binding without them is a policy that cannot be exercised.
var credentialBinaryPaths = []string{"/usr/bin/git", "/usr/bin/gh", "/usr/bin/pi"}

// bindingHostRe matches the host half of a binding host pattern: a DNS name
// or DNS glob ("*.github.com", "**.github.com"), passed to the gateway
// verbatim. Ports are split off before this check.
var bindingHostRe = regexp.MustCompile(`^[A-Za-z0-9_*-][A-Za-z0-9._*-]*$`)

// Binding is one KAPE_CREDENTIAL_BINDINGS entry (handoff H1, issue #264): the
// gateway provider whose static credentials the egress proxy resolves for
// egress to each host pattern. Provider names carry the H2 convention
// kape-<crNamespace>-<crName>-<credName>; the controller consumes them
// verbatim and never derives them. Hosts are "host:port" strings, DNS globs
// allowed.
type Binding struct {
	Provider string   `json:"provider"`
	Hosts    []string `json:"hosts"`
}

// ParseBindings decodes KAPE_CREDENTIAL_BINDINGS ([{"provider","hosts"}]) into
// bindings. An empty value means "no bindings" (nil, no error) — the seam then
// stays byte-identical to the pre-#264 posture. Malformed input is an error,
// never a silent empty table: a controller that cannot read its binding table
// must not start (fail closed, ADR-0088).
func ParseBindings(raw string) ([]Binding, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var bindings []Binding
	if err := dec.Decode(&bindings); err != nil {
		return nil, fmt.Errorf("KAPE_CREDENTIAL_BINDINGS: %w (want [{\"provider\":\"<name>\",\"hosts\":[\"<host>:<port>\"]}])", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("KAPE_CREDENTIAL_BINDINGS: trailing data after the binding array")
	}
	for i, b := range bindings {
		if !providerRe.MatchString(b.Provider) {
			return nil, fmt.Errorf("KAPE_CREDENTIAL_BINDINGS[%d].provider: %q must be a provider name ([A-Za-z0-9._-]) naming an attached profile-backed provider", i, b.Provider)
		}
		if len(b.Hosts) == 0 {
			return nil, fmt.Errorf("KAPE_CREDENTIAL_BINDINGS[%d].hosts: provider %q needs at least one host pattern", i, b.Provider)
		}
		for _, spec := range b.Hosts {
			if _, _, err := splitHostSpec(spec); err != nil {
				return nil, fmt.Errorf("KAPE_CREDENTIAL_BINDINGS[%d].hosts: provider %q: %w", i, b.Provider, err)
			}
		}
	}
	return bindings, nil
}

// splitHostSpec splits a binding host pattern "host:port" into the endpoint
// host and port. The host half may be an exact name or a DNS glob and is
// returned verbatim (globs are the gateway's to interpret). A missing,
// non-numeric or out-of-range port is an error: an endpoint without a port
// cannot carry an egress rule.
func splitHostSpec(spec string) (string, uint32, error) {
	host, portPart, found := strings.Cut(spec, ":")
	if !found || host == "" || portPart == "" {
		return "", 0, fmt.Errorf("host pattern %q must be host:port (e.g. \"github.com:443\"; DNS globs like \"*.github.com:443\" are allowed)", spec)
	}
	if strings.ContainsRune(portPart, ':') {
		return "", 0, fmt.Errorf("host pattern %q: IPv6 literals are not supported, want host:port", spec)
	}
	if !bindingHostRe.MatchString(host) {
		return "", 0, fmt.Errorf("host pattern %q: host %q must be a DNS name or DNS glob ([A-Za-z0-9._*-])", spec, host)
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("host pattern %q: port %q must be an integer in 1..65535", spec, portPart)
	}
	return host, uint32(port), nil
}

// MergePolicy returns policy with the credential bindings merged in, and never
// mutates its input. The merge is additive only (issue #264 invariant): the
// input's rule name, endpoints and binaries are carried over verbatim and in
// order, the binding endpoints and binaries are appended, and nothing is
// rewritten or removed — a model-gateway egress rule passed through here
// survives byte-identical.
//
// Each bound endpoint carries NetworkEndpoint.credential_binding so the egress
// proxy resolves the provider's static credential for that egress, and the
// calling binaries are allowlisted on the same rule (OPA denies unlisted
// binaries). Duplicate host patterns keep their first binding and duplicate
// binaries are added once, so re-applying the merge is idempotent. No bindings
// returns the policy unchanged.
func MergePolicy(policy *v1.NetworkPolicyRule, bindings []Binding) (*v1.NetworkPolicyRule, error) {
	if policy == nil {
		return nil, fmt.Errorf("credential binding merge: nil policy")
	}
	merged := &v1.NetworkPolicyRule{
		Name:      policy.GetName(),
		Endpoints: append([]*v1.NetworkEndpoint(nil), policy.GetEndpoints()...),
		Binaries:  append([]*v1.NetworkBinary(nil), policy.GetBinaries()...),
	}
	if len(bindings) == 0 {
		return merged, nil
	}
	seenEndpoint := make(map[string]bool, len(merged.Endpoints))
	for _, ep := range merged.Endpoints {
		seenEndpoint[endpointKey(ep.GetHost(), ep.GetPort())] = true
	}
	seenBinary := make(map[string]bool, len(merged.Binaries))
	for _, b := range merged.Binaries {
		seenBinary[b.GetPath()] = true
	}
	for _, binding := range bindings {
		for _, spec := range binding.Hosts {
			host, port, err := splitHostSpec(spec)
			if err != nil {
				return nil, fmt.Errorf("credential binding %q: %w", binding.Provider, err)
			}
			key := endpointKey(host, port)
			if seenEndpoint[key] {
				continue
			}
			seenEndpoint[key] = true
			merged.Endpoints = append(merged.Endpoints, &v1.NetworkEndpoint{
				Host:     host,
				Port:     port,
				Protocol: "rest",
				// The gateway rejects a "rest" endpoint without allowed
				// traffic (verified live on v0.1.0: "protocol requires rules
				// or access to define allowed traffic"). git and gh reach
				// arbitrary paths on the bound host, so the L7 rule admits any
				// method and path — the host pattern, not the path, is the
				// credential boundary.
				Rules: []*v1.L7Rule{{
					Allow: &v1.L7Allow{Method: "*", Path: "**"},
				}},
				CredentialBinding: &v1.NetworkCredentialBinding{Provider: binding.Provider},
			})
		}
	}
	for _, path := range credentialBinaryPaths {
		if seenBinary[path] {
			continue
		}
		seenBinary[path] = true
		merged.Binaries = append(merged.Binaries, &v1.NetworkBinary{Path: path})
	}
	return merged, nil
}

// endpointKey is the dedupe key for a bound egress target: host globs are
// matched case-insensitively by the gateway, so the key folds case.
func endpointKey(host string, port uint32) string {
	return strings.ToLower(host) + ":" + strconv.FormatUint(uint64(port), 10)
}
