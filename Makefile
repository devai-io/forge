# Forge — build, test, run.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: help ui build test web-test run dist docker clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

ui: ## Build the web app and copy it into the server's embed directory
	cd web && npm ci --no-audit --no-fund && npm run build
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
	cp -r web/dist/. internal/webui/dist/

build: ## Build bin/forge (run `make ui` first to include the web app)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/forge ./cmd/forge

test: ## Go vet + tests (SQLite in temp dirs; nothing to set up)
	go vet ./... && go test ./...

web-test: ## Web app typecheck, lint and tests
	cd web && npm run typecheck && npm run lint && npm test

run: build ## Run the server with its workspace in ./.forge-dev
	FORGE_HOME=./.forge-dev ./bin/forge server

dist: ui ## Release archives for every platform into dist/
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; d=dist/forge_$${os}_$${arch}; mkdir -p $$d; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o $$d/forge ./cmd/forge || exit 1; \
	  cp LICENSE README.md INSTALL.md $$d/; tar -C dist -czf $$d.tar.gz $$(basename $$d); rm -rf $$d; \
	done
	cp install.sh dist/
	cd dist && sha256sum *.tar.gz install.sh > checksums.txt

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t forge:$(VERSION) .

clean:
	rm -rf bin dist web/dist
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
