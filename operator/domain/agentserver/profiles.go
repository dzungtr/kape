package agentserver

import "sort"

// ProviderProfile is an OpenShell provider-type profile document (v0.1.0
// provider profiles are authoritative — legacy `generic` creation is retired).
// The operator imports these before it creates any provider, and the YAML form
// of the git-https document is the H5 debugging reference (#261, #265).
//
// The documents are endpointless on purpose: the credential boundary is the
// KapeAgentServer's own hostPatterns, carried to the controller in
// KAPE_CREDENTIAL_BINDINGS and merged into each sandbox policy as
// NetworkEndpoint.credential_binding. A profile that declared its own endpoints
// would collide with those endpoints ("network endpoint ambiguity" — #269
// finding 3), so the profile contributes credentials and binaries only.
type ProviderProfile struct {
	ID          string              `json:"id" yaml:"id"`
	DisplayName string              `json:"display_name" yaml:"display_name"`
	Description string              `json:"description" yaml:"description"`
	Category    string              `json:"category" yaml:"category"`
	Credentials []ProfileCredential `json:"credentials" yaml:"credentials"`
	Binaries    []ProfileBinary     `json:"binaries" yaml:"binaries"`
}

// ProfileCredential is one named credential a profile declares. The name is the
// key the provider record must carry; auth style and header name describe how
// the egress proxy applies it (proxy-layer auth-header injection).
type ProfileCredential struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Required    bool   `json:"required" yaml:"required"`
	AuthStyle   string `json:"auth_style" yaml:"auth_style"`
	HeaderName  string `json:"header_name" yaml:"header_name"`
}

// ProfileBinary is one binary the profile authorises against its credential.
type ProfileBinary struct {
	Path string `json:"path" yaml:"path"`
}

// CategorySourceControl is the profile category string for source-control
// providers. The writer maps it to the gateway's ProviderProfileCategory enum.
const CategorySourceControl = "source_control"

// builtinProfiles are the provider-type profile documents the operator ships.
// The registry is generic (D2): a new credential class is a document here plus a
// credentials[] entry in a KapeAgentServer, never new reconcile code.
var builtinProfiles = map[string]ProviderProfile{
	"git-https": {
		ID:          "git-https",
		DisplayName: "Git over HTTPS (GitHub)",
		Description: "GitHub git and REST API over HTTPS with proxy-layer auth-header injection",
		Category:    CategorySourceControl,
		Credentials: []ProfileCredential{{
			Name:        "token",
			Description: "GitHub token injected at the egress proxy",
			Required:    true,
			AuthStyle:   "bearer",
			HeaderName:  "Authorization",
		}},
		Binaries: []ProfileBinary{
			{Path: "/usr/bin/git"},
			{Path: "/usr/bin/gh"},
		},
	},
}

// Profile returns a copy of the provider-type profile document with the given
// id. ok is false for an unknown provider type: the credential cannot be pushed
// and the writer reports it (degrade-loud, D9) rather than guessing a profile.
func Profile(providerType string) (ProviderProfile, bool) {
	profile, ok := builtinProfiles[providerType]
	if !ok {
		return ProviderProfile{}, false
	}
	return cloneProfile(profile), true
}

// ProfileTypes lists the provider types the operator ships profile documents
// for, sorted. Used by startup logging and tests.
func ProfileTypes() []string {
	types := make([]string, 0, len(builtinProfiles))
	for id := range builtinProfiles {
		types = append(types, id)
	}
	sort.Strings(types)
	return types
}

// MissingCredentialKeys reports the required credential keys the resolved Secret
// material does not provide, sorted. Names only — never values.
func (p ProviderProfile) MissingCredentialKeys(values map[string]string) []string {
	var missing []string
	for _, cred := range p.Credentials {
		if !cred.Required {
			continue
		}
		if _, ok := values[cred.Name]; !ok {
			missing = append(missing, cred.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// cloneProfile deep-copies a profile so callers cannot mutate the registry.
func cloneProfile(p ProviderProfile) ProviderProfile {
	out := p
	out.Credentials = append([]ProfileCredential(nil), p.Credentials...)
	out.Binaries = append([]ProfileBinary(nil), p.Binaries...)
	return out
}
