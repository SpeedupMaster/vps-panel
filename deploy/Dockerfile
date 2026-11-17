# Build from the repo root:  docker build -f deploy/Dockerfile -t vps-panel .
FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/panel/webui/dist ./internal/panel/webui/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X vpspanel/internal/common/version.Version=${VERSION}" -o /out/vps-panel ./cmd/panel \
 && for arch in amd64 arm64; do CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X vpspanel/internal/common/version.Version=${VERSION}" -o /out/agent/vpsagent-linux-$arch ./cmd/agent; done

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 vpspanel
COPY --from=go /out/ /opt/vps-panel/
ENV VPSPANEL_DATA=/data VPSPANEL_LISTEN=:8080 VPSPANEL_AGENT_DIR=/opt/vps-panel/agent TZ=Asia/Shanghai
VOLUME /data
EXPOSE 8080
USER vpspanel
ENTRYPOINT ["/opt/vps-panel/vps-panel"]
