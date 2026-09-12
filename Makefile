GO ?= go
NPM ?= npm
BIN := bin
WEB := web
SANDBOX_IMAGE ?= eika-sandbox:latest
# Explicit package list: ./... would walk into web/node_modules, which ships
# Go files of its own.
GOPKGS := ./cmd/... ./internal/...
COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: all build build-go build-web test test-go test-web lint lint-go lint-web \
	fmt fmt-check check typecheck dev dev-go dev-web sandbox web-install clean

all: build

## build: compile both binaries and the frontend.
build: build-go build-web

build-go:
	$(GO) build -o $(BIN)/eika ./cmd/eika
	$(GO) build -o $(BIN)/eikad ./cmd/eikad

build-web: web-install
	cd $(WEB) && $(NPM) run build

## test: run Go and frontend tests.
test: test-go test-web

test-go:
	$(GO) test $(GOPKGS)

test-web: web-install
	cd $(WEB) && $(NPM) test

## lint: vet and lint Go and the frontend.
lint: lint-go lint-web

lint-go:
	$(GO) vet $(GOPKGS)
	$(GO) tool staticcheck $(GOPKGS)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(GOPKGS); \
	else \
		echo "golangci-lint not installed, skipping"; \
	fi

lint-web: web-install
	cd $(WEB) && $(NPM) run lint

## fmt: format Go and frontend sources in place.
fmt:
	$(GO) fmt $(GOPKGS)
	cd $(WEB) && $(NPM) run format

fmt-check:
	@unformatted=$$(gofmt -l $$($(GO) list -f '{{.Dir}}' $(GOPKGS))); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	cd $(WEB) && $(NPM) run format:check

## check: everything CI runs. Requires `make web-install` once.
check: fmt-check lint test-go typecheck test-web

typecheck: web-install
	cd $(WEB) && $(NPM) run typecheck

## dev: run the harness and the Vite dev server against the compose services.
dev:
	$(COMPOSE) up -d postgres searxng
	$(MAKE) -j2 dev-go dev-web

dev-go:
	EIKA_DATABASE_URL="postgres://eika:eika@127.0.0.1:5432/eika?sslmode=disable" \
	EIKA_SEARXNG_URL="http://127.0.0.1:8888" \
	$(GO) run ./cmd/eika -config deploy/eika.yaml

dev-web: web-install
	cd $(WEB) && $(NPM) run dev

## sandbox: build the default sandbox image.
sandbox:
	docker build -t $(SANDBOX_IMAGE) sandbox

## web-install: install frontend dependencies from the lockfile.
web-install:
	@if [ ! -d $(WEB)/node_modules ]; then cd $(WEB) && $(NPM) ci; fi

clean:
	rm -rf $(BIN) $(WEB)/dist
