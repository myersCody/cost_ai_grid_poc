.PHONY: build generate lint test vet clean

OAPI_CODEGEN = go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

build: generate
	go build -o inventory-watcher ./cmd/consumer/
	go build -o maas-simulator ./cmd/maas-simulator/

generate:
	$(OAPI_CODEGEN) --config=oapi-codegen.yaml ../docs/openapi.yaml

check-generated: generate
	@if ! git diff --quiet internal/api/server.gen.go; then \
		echo "ERROR: generated code is stale. Run 'make generate' and commit."; \
		git diff --stat internal/api/server.gen.go; \
		exit 1; \
	fi

lint: generate
	go vet ./...
	golangci-lint run

test: generate
	go test -race -count=1 -timeout 120s ./...

vet:
	go vet ./...

validate-spec:
	python3 -c "from openapi_spec_validator import validate; import yaml; validate(yaml.safe_load(open('../docs/openapi.yaml'))); print('OpenAPI spec valid')"

clean:
	rm -f inventory-watcher maas-simulator
