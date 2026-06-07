package reconcile

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// computeRolloutHash hashes (in this fixed order, per spec §2.1):
//
//  1. handler.Spec
//  2. schema.Spec
//  3. for each tool in deps.Tools (sorted by Name): tool.Spec
//  4. for each skill in deps.Skills (declaration order): skill.Spec
//  5. cfg.KapeproxyImage + cfg.KapeproxyImageVersion
//
// Skills are NOT sorted (D13): reordering handler.spec.skills[] changes the
// system prompt assembly order, so the hash must reflect order, not just
// set membership.
// kapeproxy image fields are included so kape-config changes trigger rollouts.
func computeRolloutHash(handler *v1alpha1.KapeHandler, deps *resolvedDependencies, cfg domainconfig.KapeConfig) (string, error) {
	h := sha256.New()
	for _, item := range []interface{}{handler.Spec, deps.Schema.Spec} {
		b, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, t := range deps.Tools {
		b, err := json.Marshal(t.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, s := range deps.Skills {
		b, err := json.Marshal(s.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	h.Write([]byte(cfg.KapeproxyImage))
	h.Write([]byte(cfg.KapeproxyImageVersion))
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
