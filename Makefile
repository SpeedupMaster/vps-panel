VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X vpspanel/internal/common/version.Version=$(VERSION) -X vpspanel/internal/common/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null)

.PHONY: all web panel agent test clean

all: web panel agent

web:
	cd web && pnpm install && pnpm build

panel:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vps-panel-linux-amd64 ./cmd/panel
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vps-panel-linux-arm64 ./cmd/panel

agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vpsagent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/vpsagent-linux-arm64 ./cmd/agent

test:
	go vet ./... && go test ./...

clean:
	rm -rf dist
