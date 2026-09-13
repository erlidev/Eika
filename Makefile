GO ?= go
NPM ?= npm
BIN := bin
WEB := web
SANDBOX_IMAGE ?= eika-sandbox:latest
# Explicit package list: ./... would walk into web/node_modules, which ships
# Go files of its own.
GOPKGS := ./cmd/... ./internal/...
GODIRS := cmd internal
ENV_FILE := deploy/.env
COMPOSE := docker compose -f deploy/docker-compose.yml --env-file $(ENV_FILE)
LOCAL_COMPOSE := EIKA_AUTH_TOKEN="$${EIKA_AUTH_TOKEN:-dev-token}" \
	POSTGRES_PASSWORD="$${POSTGRES_PASSWORD:-eika-local}" \
	SEARXNG_SECRET="$${SEARXNG_SECRET:-eika-local}" \
	DOCKER_GID="$${DOCKER_GID:-$$(stat -c %g /var/run/docker.sock)}" \
	docker compose --project-name eika-local -f deploy/docker-compose.yml --env-file /dev/null

.PHONY: all build build-go build-web test test-go test-web lint lint-go lint-web \
	fmt fmt-check check typecheck dev dev-go dev-web local local-down local-logs \
	sandbox web-install clean

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
	$(GO) tool goimports -w $(GODIRS)
	cd $(WEB) && $(NPM) run format

fmt-check:
	@unformatted=$$(gofmt -l $(GODIRS)); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	@unsorted=$$($(GO) tool goimports -l $(GODIRS)); \
	if [ -n "$$unsorted" ]; then \
		echo "goimports needed:"; echo "$$unsorted"; exit 1; \
	fi
	cd $(WEB) && $(NPM) run format:check

## check: everything CI runs. Requires `make web-install` once.
check: fmt-check lint test-go typecheck test-web

typecheck: web-install
	cd $(WEB) && $(NPM) run typecheck

## dev: run the harness and the Vite dev server against the compose services.
## Needs deploy/.env; copy deploy/.env.example and fill it in first.
dev: $(ENV_FILE)
	$(COMPOSE) up -d postgres searxng
	$(MAKE) -j2 dev-go dev-web

$(ENV_FILE):
	@echo "$(ENV_FILE) is missing: cp deploy/.env.example $(ENV_FILE) and fill it in"; exit 1

# The harness runs on the host in dev, so it reaches the compose services on
# their published 127.0.0.1 ports instead of their internal hostnames, and the
# event stream has to accept the Vite dev server's origin.
dev-go: $(ENV_FILE)
	@set -a; . ./$(ENV_FILE); set +a; \
	EIKA_AUTH_TOKEN="$${EIKA_AUTH_TOKEN:-dev-token}" \
	EIKA_ALLOWED_ORIGINS="http://localhost:5173,http://127.0.0.1:5173" \
	EIKA_DATABASE_URL="postgres://$${POSTGRES_USER:-eika}:$${POSTGRES_PASSWORD}@127.0.0.1:$${POSTGRES_PORT:-5432}/$${POSTGRES_DB:-eika}?sslmode=disable" \
	EIKA_SEARXNG_URL="http://127.0.0.1:$${SEARXNG_PORT:-8888}" \
	$(GO) run ./cmd/eika -config deploy/eika.yaml

dev-web: web-install
	cd $(WEB) && $(NPM) run dev

## local: run the complete stack without creating files in the repository.
## Set OPENAI_API_KEY in the shell to run agents. The UI uses dev-token.
local: sandbox
	$(LOCAL_COMPOSE) up -d --build
	@echo "Eika is ready at http://localhost:$${EIKA_PORT:-8080} (token: $${EIKA_AUTH_TOKEN:-dev-token})"

## local-down: stop the local stack. Add `-v` manually to delete its data.
local-down:
	$(LOCAL_COMPOSE) down

## local-logs: follow logs from the local stack.
local-logs:
	$(LOCAL_COMPOSE) logs -f

## sandbox: build the default sandbox image. The context is the repository
## root because the image builds eikad from source.
sandbox:
	docker build -f sandbox/Dockerfile -t $(SANDBOX_IMAGE) .

## web-install: install frontend dependencies when the lockfile is newer than
## what is on disk.
web-install:
	@if [ ! -f $(WEB)/node_modules/.package-lock.json ] || \
		[ $(WEB)/package-lock.json -nt $(WEB)/node_modules/.package-lock.json ]; then \
		cd $(WEB) && $(NPM) ci; \
	fi

clean:
	rm -rf $(BIN) $(WEB)/dist
