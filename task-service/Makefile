.PHONY: generate mock test test-e2e migrate

generate:
	openapi-generator-cli generate \
		-i openapi/openapi.yaml \
		-g go-chi-server \
		-o internal/interfaces/http/gen \
		--package-name gen \
		--additional-properties=packageName=gen,sourceFolder=.

mock:
	go run github.com/vektra/mockery/v2 \
		--name=Repository \
		--dir=internal/domain/task \
		--output=internal/domain/task/mocks \
		--outpkg=mocks \
		--filename=mock_repository.go
	go run github.com/vektra/mockery/v2 \
		--name=Stream \
		--dir=internal/domain/task \
		--output=internal/domain/task/mocks \
		--outpkg=mocks \
		--filename=mock_stream.go

test:
	go test ./...

test-e2e:
	go test ./test/e2e/... -tags e2e -v -timeout 120s

migrate:
	go run ./cmd/task-service --migrate-only
