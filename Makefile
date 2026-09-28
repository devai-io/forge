# Forge — build, test, run.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help ui build test test-db web-test lint dev-db run docker clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

ui: ## Build the web app and copy it into the server's embed directory
	cd web && npm ci --no-audit --no-fund && npm run build
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
	cp -r web/dist/. internal/webui/dist/

build: ## Build bin/forge (run `make ui` first to include the web app)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/forge ./cmd/forge

test: ## Go unit tests (database tests skip without TEST_DATABASE_URL)
	go vet ./... && go test ./...

test-db: ## All Go tests against TEST_DATABASE_URL (that database is wiped)
	go test ./... -count=1 -p 1

web-test: ## Web app typecheck, lint and tests
	cd web && npm run typecheck && npm run lint && npm test

dev-db: ## Start a throwaway local Postgres on :5432 (user/password/db: forge)
	docker run -d --name forge-dev-db -e POSTGRES_USER=forge -e POSTGRES_PASSWORD=forge -e POSTGRES_DB=forge -p 127.0.0.1:5432:5432 postgres:17-alpine

run: build ## Run the server against the local dev database
	DATABASE_URL=postgres://forge:forge@127.0.0.1:5432/forge?sslmode=disable FORGE_DATA_DIR=./data ./bin/forge server

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t forge:$(VERSION) .

clean:
	rm -rf bin web/dist
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
