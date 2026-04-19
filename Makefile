.PHONY: generate build test lint docker-build

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
