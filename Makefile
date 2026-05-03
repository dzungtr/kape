.PHONY: generate build test lint docker-build playground-up playground-down playground-operator playground-logs fire-adapter

generate:
	controller-gen rbac:roleName=kape-operator crd:allowDangerousTypes=true webhook \
		paths=./operator/infra/... \
		output:crd:artifacts:config=./crds
	npx openapi-typescript task-service/openapi/openapi.yaml \
		-o dashboard/app/types/generated/task-service.ts

build:
	go build ./operator/cmd/...
	go build ./task-service/cmd/...
	go build ./adapters/cmd/...
	cd runtime && uv build
	cd dashboard && npm run build

test:
	go test ./operator/...
	go test ./task-service/...
	go test ./adapters/...
	cd runtime && uv run pytest
	cd dashboard && npm test -- --passWithNoTests

lint:
	golangci-lint run ./operator/... ./task-service/... ./adapters/...
	cd runtime && uv run ruff check . && uv run ruff format --check .
	cd dashboard && npm run lint

docker-build:
	docker build -t kape-operator:dev -f operator/Dockerfile .
	docker build -t kape-task-service:dev -f task-service/Dockerfile .
	docker build -t kape-runtime:dev -f runtime/Dockerfile .
	docker build -t kape-dashboard:dev -f dashboard/Dockerfile .
	docker build -t kape-adapter-falco:dev -f adapters/Dockerfile.falco .
	docker build -t kape-adapter-alertmanager:dev -f adapters/Dockerfile.alertmanager .
	docker build -t kape-adapter-audit:dev -f adapters/Dockerfile.audit .

playground-up:
	@if [ ! -f playground/runtime/settings.toml ]; then \
	  cp playground/runtime/settings.toml.example playground/runtime/settings.toml; \
	  echo "Created playground/runtime/settings.toml from example — edit before firing events."; \
	fi
	@if [ ! -f playground/.env ]; then \
	  cp playground/.env.example playground/.env; \
	  echo "Created playground/.env — set ANTHROPIC_API_KEY before starting runtime."; \
	fi
	podman compose -f playground/docker-compose.playground.yml --env-file playground/.env up -d --build

playground-down:
	podman compose -f playground/docker-compose.playground.yml down -v

playground-operator:
	go run ./operator/cmd/playground/...

playground-logs:
	podman compose -f playground/docker-compose.playground.yml logs -f

fire-adapter:
	@test -n "$(ADAPTER)" || (echo "Usage: make fire-adapter ADAPTER=alertmanager" && exit 1)
	go run ./adapters/cmd/$(ADAPTER)/... --playground
