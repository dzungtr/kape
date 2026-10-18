package main

// Credential bridge for the daemon's pinned pi (issue #252 blocker; proper
// sandbox-level credential injection is #260). The daemon's autostart pi is
// the session-pinned instance the whole Epic 3 contract (E3/E4, smoke
// assertions) is built on, but nothing gave it model credentials: auth.json
// is empty and no provider key reaches its env, so every prompt failed with
// "No API key found for the selected model".
//
// The working recipe already exists in controller/gateway.go piWrapperCommand
// (spike #246): the model gateway is unauthenticated, so a dummy key satisfies
// pi's key lookup, and pi env-interpolates apiKey ($OPENROUTER_API_KEY) but
// NOT baseUrl — so the workspace models.json baseUrl must be patched to the
// model-gateway URL before pi reads it. This file applies the same recipe to
// the daemon-spawned pi. If KAPE_MODEL_GATEWAY_URL/KAPE_PI_MODEL are set they
// win; otherwise the smoke defaults apply.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultModelGatewayURL = "http://model-gateway-http.aperture.svc.cluster.local/v1"
	defaultPinnedPiModel   = "z-ai/glm-5.3-flash"
	defaultPiAgentDir      = "/sandbox/.pi/agent"
)

var piGatewayOnce sync.Once

// preparePiGateway is invoked once before the first real-pi spawn: it exports
// the dummy key/model env (children inherit the daemon env) and patches the
// workspace models.json baseUrl to the model gateway. Best-effort: failures
// are logged, never fatal — a misconfigured gateway must not crash the daemon.
func preparePiGateway() {
	piGatewayOnce.Do(func() {
		if os.Getenv("OPENROUTER_API_KEY") == "" {
			os.Setenv("OPENROUTER_API_KEY", "dummy")
		}
		if os.Getenv("PI_PROVIDER") == "" {
			os.Setenv("PI_PROVIDER", "openrouter")
		}
		if os.Getenv("PI_MODEL") == "" {
			os.Setenv("PI_MODEL", defaultPinnedPiModel)
		}
		url := os.Getenv("KAPE_MODEL_GATEWAY_URL")
		if url == "" {
			url = defaultModelGatewayURL
		}
		model := os.Getenv("PI_MODEL")
		dir := os.Getenv("PI_CODING_AGENT_DIR")
		if dir == "" {
			dir = defaultPiAgentDir
		}
		if err := patchModelsJSONBaseURL(filepath.Join(dir, "models.json"), url, model); err != nil {
			log.Printf("[pi-gateway] models.json patch failed (pi prompts will fail auth): %v", err)
			return
		}
		log.Printf("[pi-gateway] models.json baseUrl -> %s (model %s)", url, model)
	})
}

// patchModelsJSONBaseURL rewrites providers.openrouter.baseUrl and ensures the
// model id exists under providers.openrouter.models, mirroring the StartPi
// wrapper's python patch byte-for-byte in spirit.
func patchModelsJSONBaseURL(path, url, model string) error {
	// The workspace init materialises models.json at container start; the
	// daemon may boot before it lands. Wait bounded — the smoke's own ready
	// timeouts are minutes, and pi autostart would otherwise read a file
	// pointing at openrouter.ai with no reachable credentials.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("models.json not found at %s after 60s", path)
		}
		time.Sleep(500 * time.Millisecond)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read models.json: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("decode models.json: %w", err)
	}
	provs, ok := m["providers"].(map[string]any)
	if !ok {
		provs = map[string]any{}
		m["providers"] = provs
	}
	prov, ok := provs["openrouter"].(map[string]any)
	if !ok {
		prov = map[string]any{}
		provs["openrouter"] = prov
	}
	prov["baseUrl"] = url
	models, ok := prov["models"].([]any)
	if !ok {
		models = []any{}
		prov["models"] = models
	}
	found := false
	for _, e := range models {
		if em, ok := e.(map[string]any); ok && em["id"] == model {
			found = true
			break
		}
	}
	if !found && model != "" {
		prov["models"] = append(models, map[string]any{"id": model})
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode models.json: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write models.json: %w", err)
	}
	return nil
}
