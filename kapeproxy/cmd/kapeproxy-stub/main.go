// DEPRECATED: removed in Phase 6 slice 7
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"gopkg.in/yaml.v3"
)

type upstreamCfg struct {
	AllowedTools []string `yaml:"allowedTools"`
}

type config struct {
	Upstreams map[string]upstreamCfg `yaml:"upstreams"`
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func loadConfig(path string) (config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return config{}, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func buildToolsList(cfg config) []toolEntry {
	var tools []toolEntry
	for upstreamName, u := range cfg.Upstreams {
		if len(u.AllowedTools) == 0 {
			// No allowedTools filter: advertise a sentinel indicating all tools are exposed.
			tools = append(tools, toolEntry{
				Name:        upstreamName + "__*",
				Description: "all tools from " + upstreamName,
			})
			continue
		}
		for _, t := range u.AllowedTools {
			tools = append(tools, toolEntry{
				Name:        upstreamName + "__" + t,
				Description: t + " via " + upstreamName,
			})
		}
	}
	return tools
}

func main() {
	cfgPath := os.Getenv("KAPEPROXY_CONFIG")
	if cfgPath == "" {
		cfgPath = "/etc/kapeproxy/config.yaml"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	tools := buildToolsList(cfg)

	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		var resp jsonRPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "tools/list":
			resp.Result = map[string]interface{}{"tools": tools}
		default:
			resp.Error = &rpcError{Code: -32603, Message: "server not yet available"}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	log.Printf("kapeproxy stub listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
