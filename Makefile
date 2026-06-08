.DEFAULT_GOAL := help
.PHONY: generate build test test-integration lint build-images docker-build podman-build playground-up playground-down playground-operator playground-logs fire-adapter clean help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

generate: ## Regenerate CRD manifests and TypeScript API types
	controller-gen rbac:roleName=kape-operator crd:allowDangerousTypes=true webhook \
		paths=./operator/infra/... \
		output:crd:artifacts:config=./crds
	bunx openapi-typescript task-service/openapi/openapi.yaml \
		-o dashboard/app/types/generated/task-service.ts

build: ## Build all Go binaries, Python wheel, and dashboard
	go build ./operator/cmd/...
	go build -o ./task-service/task-service ./task-service/cmd/main
	go build ./adapters/cmd/...
	go build ./kapeproxy/cmd/...
	cd runtime && uv build
	cd dashboard && bun run build

PODMAN_ENV := DOCKER_HOST=unix://$(XDG_RUNTIME_DIR)/podman/podman.sock TESTCONTAINERS_RYUK_DISABLED=true

test: ## Run all tests (Go, Python, dashboard)
	$(PODMAN_ENV) go test ./operator/...
	$(PODMAN_ENV) go test ./task-service/...
	$(PODMAN_ENV) go test ./adapters/...
	$(PODMAN_ENV) go test ./kapeproxy/...
	cd runtime && conda run -n kape-runtime pytest
	cd dashboard && bun run test -- --passWithNoTests

lint: ## Run golangci-lint and ruff across all modules
	golangci-lint run ./operator/... ./task-service/... ./adapters/... ./kapeproxy/...
	cd runtime && uv run ruff check . && uv run ruff format --check .
	cd dashboard && bun run lint

build-images: ## Build all container images with podman
	podman build -t kape-operator:dev -f operator/Dockerfile .
	podman build -t kape-task-service:dev -f task-service/Dockerfile .
	podman build -t kape-runtime:dev -f runtime/Dockerfile .
	podman build -t kape-dashboard:dev -f dashboard/Dockerfile .
	podman build -t kape-adapter-falco:dev -f adapters/Dockerfile.falco .
	podman build -t kape-adapter-alertmanager:dev -f adapters/Dockerfile.alertmanager .
	podman build -t kape-adapter-audit:dev -f adapters/Dockerfile.audit .

docker-build: ## Build all container images using docker
	docker build -t kape-operator:dev -f operator/Dockerfile .
	docker build -t kape-task-service:dev -f task-service/Dockerfile .
	docker build -t kape-runtime:dev -f runtime/Dockerfile .
	docker build -t kape-dashboard:dev -f dashboard/Dockerfile .
	docker build -t kape-adapter-falco:dev -f adapters/Dockerfile.falco .
	docker build -t kape-adapter-alertmanager:dev -f adapters/Dockerfile.alertmanager .
	docker build -t kape-adapter-audit:dev -f adapters/Dockerfile.audit .

podman-build: ## Build all images using podman (local dev)
	podman build -t kape-operator:dev -f operator/Dockerfile .
	podman build -t kape-task-service:dev -f task-service/Dockerfile .
	podman build -t kape-runtime:dev -f runtime/Dockerfile .
	podman build -t kape-dashboard:dev -f dashboard/Dockerfile .
	podman build -t kape-adapter-falco:dev -f adapters/Dockerfile.falco .
	podman build -t kape-adapter-alertmanager:dev -f adapters/Dockerfile.alertmanager .
	podman build -t kape-adapter-audit:dev -f adapters/Dockerfile.audit .

playground-up: ## Start the playground stack (copies example configs if absent)
	@if [ ! -f playground/runtime/settings.toml ]; then \
	  cp playground/runtime/settings.toml.example playground/runtime/settings.toml; \
	  echo "Created playground/runtime/settings.toml from example — edit before firing events."; \
	fi
	@if [ ! -f playground/.env ]; then \
	  cp playground/.env.example playground/.env; \
	  echo "Created playground/.env — set ANTHROPIC_API_KEY before starting runtime."; \
	fi
	podman compose -f playground/docker-compose.playground.yml --env-file playground/.env up -d --build

playground-down: ## Tear down the playground stack and remove volumes
	podman compose -f playground/docker-compose.playground.yml down -v

playground-operator: ## Run the operator locally against the playground cluster
	go run ./operator/cmd/playground/...

playground-logs: ## Follow logs from the playground stack
	podman compose -f playground/docker-compose.playground.yml logs -f

fire-adapter: ## Fire a test event via an adapter — usage: make fire-adapter ADAPTER=alertmanager
	@test -n "$(ADAPTER)" || (echo "Usage: make fire-adapter ADAPTER=alertmanager" && exit 1)
	go run ./adapters/cmd/$(ADAPTER)/... --playground

clean: ## Remove compiled Go binaries
	@rm -f ./cmd
	@rm -f ./operator/cmd/cmd
	@rm -f ./task-service/task-service
	@rm -f ./examples/sre-alertmanager/src/mock-api/mock-api
	@rm -f ./examples/sre-alertmanager/src/mock-webhook/mock-webhook
	@echo "Cleaned compiled binaries."

ENVTEST_K8S_VERSION ?= 1.32.0
ENVTEST_BIN_DIR     ?= /tmp/envtest-bins

test-integration: ## Run operator envtest integration suite (requires setup-envtest)
	KUBEBUILDER_ASSETS=$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_BIN_DIR) -p path) \
		go test -tags envtest -v -count=1 ./operator/test/integration/...
