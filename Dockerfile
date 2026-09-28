# Forge — one image: the Go server with the web app built in.
#
#   docker build -t forge .
#   docker run --rm forge version

# 1. Web app
FROM node:22-bookworm AS web
WORKDIR /web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run typecheck && npm run lint && npm test && npm run build

# 2. Server (vet + unit tests gate the build; DB-backed tests run in CI)
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/webui/dist/
ENV CGO_ENABLED=0
RUN go vet ./... && go test ./...
ARG VERSION=dev
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/forge ./cmd/forge \
 && mkdir -p /out/data

# 3. Runtime: no shell, no package manager, not root
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/forge /forge
COPY --from=server --chown=65532:65532 /out/data /data
ENV FORGE_DATA_DIR=/data
VOLUME /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/forge"]
CMD ["server"]
