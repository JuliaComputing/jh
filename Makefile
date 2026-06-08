.DEFAULT_GOAL := help

.PHONY: help build test fmt vet check e2e e2e-fast e2e-compile

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
