.PHONY: build frontend-build test vet run clean

BINARY_NAME ?= mcm
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFLAGS ?=
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build:
	$(MAKE) frontend-build
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/mcm

frontend/node_modules: frontend/package-lock.json frontend/package.json
	npm --prefix frontend ci

frontend-build: frontend/node_modules
	npm --prefix frontend run build

test:
	$(MAKE) frontend-build
	go test $(GOFLAGS) ./...

vet:
	$(MAKE) frontend-build
	go vet ./...

run:
	$(MAKE) frontend-build
	go run ./cmd/mcm

clean:
	rm -rf bin/
