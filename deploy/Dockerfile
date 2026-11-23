# Build from the repo root:  docker build -f deploy/Dockerfile -t vps-panel .
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY scripts ./scripts
COPY --from=web /src/internal/panel/webui/dist ./internal/panel/webui/dist
ARG VERSION=docker
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -tags nomsgpack -ldflags "-s -w -X vpspanel/internal/common/version.Version=${VERSION} -X vpspanel/internal/common/version.Commit=${COMMIT}" -o /out/vps-panel ./cmd/panel \
 && for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X vpspanel/internal/common/version.Version=${VERSION} -X vpspanel/internal/common/version.Commit=${COMMIT}" -o /out/agent/vpsagent-linux-$arch ./cmd/agent \
      && gzip -9 /out/agent/vpsagent-linux-$arch; \
    done

FROM alpine:3.22
RUN apk add --no-cache ca-certificates su-exec \
 && adduser -D -H -u 10001 vpspanel \
 && mkdir -p /data \
 && chown vpspanel:vpspanel /data
COPY --from=go /out/ /opt/vps-panel/
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
 && ln -s /usr/local/bin/docker-entrypoint.sh /usr/local/bin/vps-panel
ENV VPSPANEL_DATA=/data VPSPANEL_LISTEN=:8080 VPSPANEL_AGENT_DIR=/opt/vps-panel/agent TZ=Asia/Shanghai
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["docker-entrypoint.sh"]
