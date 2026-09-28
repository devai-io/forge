# Forge — one image: the Go server with the web app built in, SQLite inside.
#
#   docker build -t forge .
#   docker run -d -p 127.0.0.1:8080:8080 -v forge:/data forge
#
# Multi-arch without emulation: the web app and the Go build run on the
# build machine's own platform and cross-compile (pure Go, no cgo).

# 1. Web app
FROM --platform=$BUILDPLATFORM node:22-bookworm AS web
WORKDIR /web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run typecheck && npm run lint && npm test && npm run build

# 2. Server (vet + tests gate the build)
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/webui/dist/
ENV CGO_ENABLED=0
RUN go vet ./... && go test ./...
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/forge ./cmd/forge \
 && mkdir -p /out/data

# 3. Runtime: no shell, no package manager, not root
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/forge /usr/local/bin/forge
COPY --from=server --chown=65532:65532 /out/data /data
# The workspace: config.json, forge.db, vault.key, backups/, projects/.
ENV FORGE_HOME=/data
VOLUME /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["forge"]
CMD ["server"]
