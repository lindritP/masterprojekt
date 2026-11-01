# CCSRA developer entry points (SRS §9.5, DR-013).
# Stage B0: services are stubs; `dev` starts the local stack only and `test`
# runs unit tests only. Port-conformance and E2E suites join at B2/B3.

GO_MODULES := app/v0 app/v1 tools
COMPOSE    := docker compose -f deploy/local/docker-compose.yaml

.DEFAULT_GOAL := help
.PHONY: help dev stack-up stack-down stack-reset corpus test test-conformance fmt fmt-check vet \
        check-boundaries check-register ci docs

help: ## List targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

dev: stack-up ## Start the local stack (services join at B3)
	@echo "Local stack is up. The four services are wired in stage B3 (SRS §11)."

stack-up: ## Start PostgreSQL, SeaweedFS (S3), RabbitMQ, Keycloak; migrate; create the bucket
	$(COMPOSE) up -d --wait postgres seaweedfs rabbitmq keycloak
	$(COMPOSE) run --rm db-migrate
	$(COMPOSE) run --rm s3-init

stack-down: ## Stop the local stack, keep its data
	$(COMPOSE) down

stack-reset: ## Stop the local stack and delete its data
	$(COMPOSE) down -v

corpus: ## Generate the synthetic PDF corpus and check it against its manifest (FR-048)
	@cd tools && go run ./corpusgen -out ../test/corpus
	@cd test/corpus && shasum -a 256 -c --quiet corpus.sha256 && echo "corpus matches corpus.sha256"

test: corpus ## Run unit tests in every Go module (includes the FR-410 determinism check)
	@for m in $(GO_MODULES); do echo "== $$m"; (cd $$m && go test ./...) || exit 1; done

test-conformance: ## Run the port conformance suite against the local stack (after make stack-up)
	@mkdir -p build
	@cd app/v1 && CCSRA_CONFORMANCE=1 go test -count=1 -json ./internal/adapters/... > ../../build/conformance-local.json || true
	@cd tools && go run ./conformancereport < ../build/conformance-local.json

fmt: ## Format all Go code
	@gofmt -w $(GO_MODULES)

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out=$$(gofmt -l $(GO_MODULES)); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## Run go vet in every Go module
	@for m in $(GO_MODULES); do (cd $$m && go vet ./...) || exit 1; done

check-boundaries: ## Enforce AR-004 and FR-003–FR-005 import boundaries
	@scripts/check-boundaries.sh

check-register: ## Validate coupling-register.yaml against its schema (CR-001, CR-011)
	@cd tools && go run ./registercheck -srs ../SRS.md ../coupling-register.yaml

ci: fmt-check vet test check-boundaries check-register ## Everything CI runs

docs: ## Regenerate SRS.docx and Research-Design.docx from the Markdown sources
	cd tools && npm install --no-audit --no-fund && \
	node md2docx.js ../SRS.md ../SRS.docx && \
	node md2docx.js ../Research-Design.md ../Research-Design.docx
