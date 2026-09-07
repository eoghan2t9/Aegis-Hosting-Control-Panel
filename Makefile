# Aegis — Linux hosting control panel
# Targets are safe to run from a checked-out tree; they never touch production.

GO      ?= go
BIN_DIR  = bin
GOFLAGS ?=

.PHONY: all build server cli test lint vet fmt tidy dev docker-up docker-down clean

all: build

build: server cli

server:
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/aegis ./cmd/aegis

cli:
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/aegisctl ./cmd/aegisctl

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint: vet

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

tidy:
	$(GO) mod tidy

# Development environment (no rebuild needed for code changes — source is bind-mounted).
dev: docker-up

docker-up:
	docker compose -f docker/docker-compose.yml up -d --build

docker-down:
	docker compose -f docker/docker-compose.yml down

docker-shell:
	docker compose -f docker/docker-compose.yml exec aegis bash

clean:
	rm -rf $(BIN_DIR)