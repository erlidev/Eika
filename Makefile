GO ?= go
NPM ?= npm
BIN := bin
WEB := web
SANDBOX_IMAGE ?= eika-sandbox:latest
# Explicit package list: ./... would walk into web/node_modules, which ships
# Go files of its own.
GOPKGS := ./cmd/... ./internal/...
GODIRS := cmd internal
# DEV holds the state a harness running on the host keeps: the hub and the
# key that seals credentials. It is ignored by git.
DEV := .dev
COMPOSE := docker compose
DEV_COMPOSE := docker compose -f compose.yaml -f compose.dev.yaml

.PHONY: all build build-go build-web test test-go test-web lint lint-go lint-web \
	fmt fmt-check check typecheck dev dev-go dev-web local local-down local-logs \
	sandbox web-install clean visual visual-update shot playwright-browser

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

## check: every check this project has, the visual suite included. Requires `make
## web-install` once; the first run also downloads Chromium. It takes about
## five minutes, four of them the visual suite; while iterating, run the
## targets it lists one at a time.
check: fmt-check lint test-go typecheck test-web visual

typecheck: web-install
	cd $(WEB) && $(NPM) run typecheck

## visual: compare every screen with its baseline in web/e2e/__screenshots__,
## one browser at a time. About four minutes. It installs the Chromium the
## pinned Playwright expects when it is missing; a bare machine adds its
## system libraries with PLAYWRIGHT_DEPS=--with-deps.
visual: web-install playwright-browser
	cd $(WEB) && $(NPM) run visual

playwright-browser: web-install
	cd $(WEB) && npx playwright install $(PLAYWRIGHT_DEPS) chromium

## visual-update: accept the current rendering as the new baselines.
visual-update: web-install playwright-browser
	cd $(WEB) && $(NPM) run visual:update

## shot: screenshot the UI against the mock harness, e.g.
##   make shot ARGS='-s workbench --step "click Settings"'
shot: web-install
	cd $(WEB) && $(NPM) run -s shot -- $(ARGS)

## dev: run the harness and the Vite dev server against the compose services.
dev:
	$(DEV_COMPOSE) up -d postgres searxng
	$(MAKE) -j2 dev-go dev-web

# The harness runs on the host in dev, so it reaches the compose services on
# their published 127.0.0.1 ports instead of their internal hostnames, keeps
# its state in $(DEV), publishes each sandbox's daemon on loopback, and lets
# the Vite dev server's origin open the event stream. A .env in the
# repository root is read for the optional values, as compose reads it.
dev-go:
	@mkdir -p $(DEV)
	CGO_ENABLED=0 $(GO) build -o $(BIN)/eikad ./cmd/eikad
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	EIKA_ALLOWED_ORIGINS="http://localhost:5173,http://127.0.0.1:5173" \
	EIKA_DATABASE_URL="postgres://eika:$${POSTGRES_PASSWORD:-eika}@127.0.0.1:$${POSTGRES_PORT:-5432}/eika?sslmode=disable" \
	EIKA_SEARXNG_URL="http://127.0.0.1:$${SEARXNG_PORT:-8888}" \
	EIKA_SANDBOX_NETWORK="" \
	EIKA_EIKAD_BINARY="$(CURDIR)/$(BIN)/eikad" \
	EIKA_HUB_ROOT="$(CURDIR)/$(DEV)/hub" \
	EIKA_SECRET_KEY_FILE="$(CURDIR)/$(DEV)/secret.key" \
	$(GO) run ./cmd/eika

dev-web: web-install
	cd $(WEB) && $(NPM) run dev

## local: build and run the whole stack in Docker, as a deployment does.
## Open the printed URL and follow the setup.
local:
	$(COMPOSE) up -d --build
	@echo "Eika is starting at http://localhost:$${EIKA_PORT:-8080}; open it to finish the setup."

## local-down: stop the stack. Add `-v` manually to delete its data.
local-down:
	$(COMPOSE) down

## local-logs: follow logs from the stack.
local-logs:
	$(COMPOSE) logs -f

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
