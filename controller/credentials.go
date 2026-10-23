package main

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/dzungtr/kape/controller/gen"
	"google.golang.org/protobuf/proto"
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

// CredentialPolicyOps returns the policy-merge operations that carry the
// credential bindings into an EXISTING sandbox policy (issue #274).
//
// The additive-only merge above is right for hosts the base policy does not
// declare, and wrong for the ones it does. The sandbox image already allows
// the bound hosts — pi-openshell declares github.com:443 (github-ssh-over-https
// and pi) and api.github.com:443 (github-rest-api) — so appending a second
// declaration of the same endpoint carrying credential metadata trips the
// gateway's endpoint-ambiguity check:
//
//	network policies 'm1_credential_bindings' endpoint[0] (github.com:443) and
//	'github-ssh-over-https' endpoint[0] (github.com:443) overlap on port(s) 443
//	with conflicting metadata: credential_binding.provider="..." vs ""
//
// That rejects the whole UpdateConfig, so the sandbox is rolled back. Bound
// endpoints that already exist are therefore STAMPED IN PLACE: the declaring
// rule keeps its own L7 restrictions (github stays read-only git smart-HTTP)
// and only gains credential_binding.provider. Bound hosts absent from the
// policy fall back to the additive m1_credential_bindings rule, unchanged.
//
// Every rule that declares a bound endpoint is stamped, not just the first:
// the ambiguity check compares metadata across all rules, so leaving `pi`'s
// github.com:443 bare while stamping github-ssh-over-https's would recreate
// the same conflict.
func CredentialPolicyOps(policy *v1.SandboxPolicy, bindings []Binding) ([]*v1.PolicyMergeOperation, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if policy == nil {
		return nil, fmt.Errorf("credential binding policy: nil sandbox policy")
	}

	// Binding lookup: host:port -> provider. First binding wins, matching
	// MergePolicy's duplicate rule.
	providers := map[string]string{}
	var order []string
	for _, binding := range bindings {
		for _, spec := range binding.Hosts {
			host, port, err := splitHostSpec(spec)
			if err != nil {
				return nil, fmt.Errorf("credential binding %q: %w", binding.Provider, err)
			}
			key := endpointKey(host, port)
			if _, seen := providers[key]; seen {
				continue
			}
			providers[key] = binding.Provider
			order = append(order, key)
		}
	}

	var ops []*v1.PolicyMergeOperation
	stamped := map[string]bool{}
	// Sorted keys keep the emitted operation order deterministic.
	ruleKeys := make([]string, 0, len(policy.GetNetworkPolicies()))
	for key := range policy.GetNetworkPolicies() {
		ruleKeys = append(ruleKeys, key)
	}
	sort.Strings(ruleKeys)

	for _, key := range ruleKeys {
		rule := policy.GetNetworkPolicies()[key]
		if rule == nil {
			continue
		}
		changed := false
		endpoints := make([]*v1.NetworkEndpoint, 0, len(rule.GetEndpoints()))
		for _, ep := range rule.GetEndpoints() {
			if ep == nil {
				continue
			}
			key := endpointKey(ep.GetHost(), ep.GetPort())
			provider, bound := providers[key]
			if !bound {
				endpoints = append(endpoints, ep)
				continue
			}
			// Declared by the policy: the endpoint is accounted for even when
			// it already carries the binding, so no additive rule is emitted.
			stamped[key] = true
			if cur := ep.GetCredentialBinding().GetProvider(); cur != "" {
				if cur != provider {
					// Two sources of truth for one endpoint: refuse rather than
					// silently pick a winner (fail closed, ADR-0088).
					return nil, fmt.Errorf("credential binding: %s already bound to provider %q, bindings ask for %q", endpointKey(ep.GetHost(), ep.GetPort()), cur, provider)
				}
				endpoints = append(endpoints, ep)
				continue
			}
			clone := proto.Clone(ep).(*v1.NetworkEndpoint)
			clone.CredentialBinding = &v1.NetworkCredentialBinding{Provider: provider}
			// A credentialed endpoint must be L7-inspected. OpenShell v0.1.5
			// rejects the whole policy otherwise: "credentialed endpoint
			// 'host:port' in rule '<rule>' uses L4-only; configure L7
			// inspection or explicitly set allow_uninspected_credentials".
			// Image policies routinely list a host as bare L4 (the pi rule
			// lists github.com that way), so the stamped copy upgrades it
			// rather than failing the apply.
			ensureL7(clone)
			endpoints = append(endpoints, clone)
			changed = true
		}
		if !changed {
			continue
		}
		// AddRule upserts by POLICY KEY, not by the rule's `name` field —
		// verified live on v0.1.0: sending a rule whose `name` differed from
		// its key created a second rule ("kept add-rule 'github-ssh-over-https'
		// on its own rule instead of folding it into overlapping rule
		// 'github_ssh_over_https'"). So the key addresses the existing entry and
		// the body keeps the policy's own `name` and binary set.
		ops = append(ops, &v1.PolicyMergeOperation{
			Operation: &v1.PolicyMergeOperation_AddRule{
				AddRule: &v1.AddNetworkRule{
					RuleName: key,
					Rule: &v1.NetworkPolicyRule{
						Name:      rule.GetName(),
						Endpoints: endpoints,
						Binaries:  rule.GetBinaries(),
					},
				},
			},
		})
	}

	// Hosts no rule declares keep the original additive shape.
	var missing []Binding
	for _, key := range order {
		if stamped[key] {
			continue
		}
		host, port, _ := strings.Cut(key, ":")
		missing = append(missing, Binding{Provider: providers[key], Hosts: []string{host + ":" + port}})
	}
	if len(missing) > 0 {
		rule, err := MergePolicy(&v1.NetworkPolicyRule{Name: CredentialBindingRuleName}, missing)
		if err != nil {
			return nil, fmt.Errorf("credential binding policy: %w", err)
		}
		ops = append(ops, &v1.PolicyMergeOperation{
			Operation: &v1.PolicyMergeOperation_AddRule{
				AddRule: &v1.AddNetworkRule{RuleName: rule.GetName(), Rule: rule},
			},
		})
	}
	return ops, nil
}

// ensureL7 turns a bare L4 endpoint into an L7-inspected one so it may carry a
// credential binding. An endpoint that already declares L7 (protocol, an access
// preset or explicit rules) is left exactly as it is: the credential boundary
// stays the host pattern, and any path restriction the policy author wrote is
// preserved.
func ensureL7(ep *v1.NetworkEndpoint) {
	if ep == nil {
		return
	}
	if ep.GetProtocol() != "" && ep.GetProtocol() != "tcp" {
		return
	}
	if ep.GetAccess() != v1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_UNSPECIFIED || len(ep.GetRules()) > 0 {
		return
	}
	ep.Protocol = "rest"
	ep.Rules = []*v1.L7Rule{{Allow: &v1.L7Allow{Method: "*", Path: "**"}}}
}
