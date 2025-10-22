APPNAME=buildkite-cache
STAGE=dev

COVERAGE_FILE := coverage.out

# Help target
.PHONY: help
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

.PHONY: cache-bucket
cache-bucket: ## Deploy the cache bucket stack
	@echo "--- deploy stack $(APPNAME)-$(STAGE)-cache-bucket"
	@sam deploy \
		--no-fail-on-empty-changeset \
		--template-file sam/app/cache-bucket.cfn.yml \
		--capabilities CAPABILITY_IAM \
		--tags "environment=$(STAGE)" "application=$(APPNAME)" \
		--stack-name $(APPNAME)-$(STAGE)-cache-bucket \
		--parameter-overrides AppName=$(APPNAME) Stage=$(STAGE) CachePrefix=cache

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run linter with auto-fix
	golangci-lint run --fix ./...

.PHONY: snapshot
snapshot: ## Build snapshot with goreleaser
	goreleaser build --snapshot --clean --single-target

.PHONY: test
test: ## Run tests with coverage
	go test -coverprofile $(COVERAGE_FILE) -covermode atomic -v ./...
