.PHONY: all build build-ui test clean run dev dev-backend dev-ui

BINARY ?= bin/finorama
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo "$$(git rev-parse --abbrev-ref HEAD)-$$(git rev-parse --short HEAD)")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

all: build

# Run both backend and frontend concurrently for hot-reload dev
dev:
	@which bun > /dev/null || (echo "bun is required for frontend dev" && exit 1)
	@make -j 2 dev-backend dev-ui

dev-backend:
	go run main.go serve -port 8080 -data-dir ./data

dev-ui:
	cd web && bun install && bun run dev

build: build-ui
	@mkdir -p $(dir $(BINARY))
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-ui:
	cd web && bun install && bun run build

test:
	go test -v ./...

clean:
	rm -rf bin/ web/dist

run: build
	./$(BINARY) serve
