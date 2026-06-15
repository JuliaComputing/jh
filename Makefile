.DEFAULT_GOAL := help

.PHONY: help build test fmt vet check e2e e2e-fast e2e-compile coverage

# Extra flags forwarded to the compiled e2e test binary (e.g. E2E_FLAGS=-test.v).
E2E_FLAGS ?=

help: ## List available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the jh binary
	go build -o jh .

test: ## Run unit tests
	go test ./...

fmt: ## Format the code (gofmt -s)
	gofmt -s -w .

vet: ## Run go vet (incl. the e2e build tag)
	go vet ./...
	go vet -tags e2e ./e2e/...

check: fmt vet test e2e-compile ## Pre-commit checks (mirrors CI)

# --- end-to-end tests (run against a live JuliaHub instance) ---
# These need a `jh auth login`, or JULIAHUB_SERVER + JULIAHUB_ID_TOKEN/JULIAHUB_TOKEN.
# See e2e/README.md.

e2e: ## Run the live e2e suite (uses your ~/.juliahub login)
	go test -tags e2e -v ./e2e/...

e2e-fast: ## Run the e2e suite, skipping the slow project-list tests
	go test -tags e2e -v -skip 'TestProjectList' ./e2e/...

e2e-compile: ## Compile-check the e2e suite without running it (what CI does)
	go test -tags e2e -run '^$$' ./e2e/...

# --- coverage ---
# Full coverage of package main: unit tests + the live e2e suite, merged.
# Builds an instrumented `jh` (go build -cover) and runs the compiled e2e binary
# so the subprocess emits runtime coverage via GOCOVERDIR — plain `go test`
# swallows it. Needs a login for the e2e portion (see e2e/README.md); without one
# the e2e tests skip and you still get unit coverage. Reports even if some live
# tests fail/skip. Forward flags with E2E_FLAGS (e.g. E2E_FLAGS=-test.v).

coverage: ## Full coverage (unit + live e2e, merged) -> cover.out + cover.html
	go build -cover -o jh .
	go test -tags e2e -c -o e2e.test ./e2e/
	@u=$$(mktemp -d); e=$$(mktemp -d); m=$$(mktemp -d); \
	GOCOVERDIR=$$u go test -cover ./... -args -test.gocoverdir=$$u >/dev/null; \
	GOCOVERDIR=$$e JH_BIN=$(CURDIR)/jh ./e2e.test $(E2E_FLAGS) || true; \
	go tool covdata merge -i=$$u,$$e -o $$m; \
	go tool covdata textfmt -i=$$m -o cover.out; \
	go tool cover -func=cover.out | tail -1; \
	go tool cover -html=cover.out -o cover.html; \
	rm -rf $$u $$e $$m e2e.test; \
	echo "wrote cover.out, cover.html"
