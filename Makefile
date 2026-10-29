# Local environment: .env is optional and overrides the defaults below.
-include .env
export

DATABASE_URL ?= postgres://sport:sport@localhost:5432/sport_challenge?sslmode=disable

COMPOSE_FILE   := deploy/docker-compose.yml
MIGRATIONS_DIR := migrations
TOOLS_BIN      := $(CURDIR)/bin

MIGRATE_VERSION       := v4.20.1
GOLANGCI_LINT_VERSION := v2.15.0
GOFUMPT_VERSION       := v0.12.0
OAPI_CODEGEN_VERSION  := v2.8.1

REDOCLY_IMAGE    := redocly/cli:2.72.1@sha256:d39a229ebd8250fea62f44c377ddb04797e8a155140eda49db17f1caaa9334ff
SWAGGER_UI_IMAGE := swaggerapi/swagger-ui:v5.34.0@sha256:58c1fbf5f5cf6d2ae857e4d1296cb02192fa5de472c1cd76f58b2be99523e1d0
DOC_PORT         := 8082

MIGRATE       := $(TOOLS_BIN)/migrate
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOFUMPT       := $(TOOLS_BIN)/gofumpt
OAPI_CODEGEN  := $(TOOLS_BIN)/oapi-codegen

# Tools are built with the Go version of go.mod: bumping it rebuilds them
# (golangci-lint refuses to lint code newer than the Go that built it).
GO_VERSION := $(shell awk '/^go /{print $$2}' go.mod)

MIGRATE_STAMP       := $(TOOLS_BIN)/.migrate-$(MIGRATE_VERSION)-go$(GO_VERSION)
GOLANGCI_LINT_STAMP := $(TOOLS_BIN)/.golangci-lint-$(GOLANGCI_LINT_VERSION)-go$(GO_VERSION)
GOFUMPT_STAMP       := $(TOOLS_BIN)/.gofumpt-$(GOFUMPT_VERSION)-go$(GO_VERSION)
OAPI_CODEGEN_STAMP  := $(TOOLS_BIN)/.oapi-codegen-$(OAPI_CODEGEN_VERSION)-go$(GO_VERSION)

# Lint and test are skipped only while the repository has no Go file at all.
GO_FILES := $(shell find . -name '*.go' -not -path './bin/*' -print -quit)

# Hosts migrate-up and migrate-down accept: this machine or the compose service.
LOCAL_DB_HOSTS := localhost 127.0.0.1 postgres

.DEFAULT_GOAL := help

.PHONY: help run build tools fmt lint test check generate openapi-lint doc \
	migrate-new migrate-up migrate-down migrate-version \
	docker-up docker-down guard-local-db

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(firstword $(MAKEFILE_LIST))

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the binaries into bin/
	go build -o $(TOOLS_BIN)/ ./cmd/...

tools: $(MIGRATE_STAMP) $(GOLANGCI_LINT_STAMP) $(GOFUMPT_STAMP) $(OAPI_CODEGEN_STAMP) ## Install pinned dev tools into bin/

# Stamp files carry the tool and Go versions: changing either reinstalls the tool.
$(MIGRATE_STAMP):
	GOBIN=$(TOOLS_BIN) go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
	@touch $@

$(GOLANGCI_LINT_STAMP):
	GOBIN=$(TOOLS_BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@touch $@

$(GOFUMPT_STAMP):
	GOBIN=$(TOOLS_BIN) go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	@touch $@

$(OAPI_CODEGEN_STAMP):
	GOBIN=$(TOOLS_BIN) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	@touch $@

fmt: $(GOFUMPT_STAMP) ## Format the code (gofumpt, a stricter gofmt)
	$(GOFUMPT) -w .

lint: $(GOLANGCI_LINT_STAMP) ## Run golangci-lint
ifeq ($(GO_FILES),)
	@echo "No Go package yet: nothing to lint."
else
	$(GOLANGCI_LINT) run ./...
endif

test: ## Run unit tests
ifeq ($(GO_FILES),)
	@echo "No Go package yet: nothing to test."
else
	go test ./...
endif

check: fmt lint test ## Run fmt, lint and test

generate: $(OAPI_CODEGEN_STAMP) ## Regenerate internal/api from api/openapi.yaml
	$(OAPI_CODEGEN) -config api/oapi-codegen.yaml api/openapi.yaml

openapi-lint: ## Validate api/openapi.yaml (Redocly, in Docker)
	docker run --rm -v $(CURDIR)/api:/spec:ro -w /spec $(REDOCLY_IMAGE) lint openapi.yaml

doc: ## Serve Swagger UI for api/openapi.yaml on http://localhost:8082
	@echo "Swagger UI: http://localhost:$(DOC_PORT) (Ctrl+C to stop)"
	docker run --rm -p 127.0.0.1:$(DOC_PORT):8080 -e SWAGGER_JSON=/spec/openapi.yaml -v $(CURDIR)/api:/spec:ro $(SWAGGER_UI_IMAGE)

migrate-new: $(MIGRATE_STAMP) ## Create a migration pair: make migrate-new NAME=x
	@test -n "$(NAME)" || { echo "Usage: make migrate-new NAME=<name>"; exit 1; }
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq -digits 6 $(NAME)

migrate-up: guard-local-db $(MIGRATE_STAMP) ## Apply all migrations (local database only)
	@$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$$DATABASE_URL" up

migrate-down: guard-local-db $(MIGRATE_STAMP) ## Roll back the last migration (local database only)
	@$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$$DATABASE_URL" down 1

migrate-version: $(MIGRATE_STAMP) ## Show the current migration version
	@$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$$DATABASE_URL" version

# Extracts the host from DATABASE_URL and refuses anything that is not local.
# Only sslmode is allowed in the query: libpq parameters such as host= would
# override the host checked here. DATABASE_URL is read from the environment so
# that make never expands it, and it is never printed: it contains the password.
guard-local-db:
	@host=$$(printf '%s' "$$DATABASE_URL" | sed -E 's#^[^:]+://##; s#[/?].*$$##; s#^.*@##; s#:[0-9]*$$##'); \
	case " $(LOCAL_DB_HOSTS) " in \
		*" $$host "*) ;; \
		*) echo "Refusing to migrate: DATABASE_URL host '$$host' is not local (allowed: $(LOCAL_DB_HOSTS))."; exit 1 ;; \
	esac; \
	case "$$DATABASE_URL" in *\?*) query=$${DATABASE_URL#*\?} ;; *) query= ;; esac; \
	for param in $$(printf '%s' "$$query" | tr '&' ' '); do \
		case "$$param" in \
			sslmode=*) ;; \
			*) echo "Refusing to migrate: DATABASE_URL query parameter '$${param%%=*}' is not allowed (only sslmode)."; exit 1 ;; \
		esac; \
	done

docker-up: ## Start the local PostgreSQL
	docker compose -f $(COMPOSE_FILE) up -d --wait

docker-down: ## Stop the local PostgreSQL (data is kept)
	docker compose -f $(COMPOSE_FILE) down
