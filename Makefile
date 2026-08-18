PKG := github.com/theoutdoorprogrammer/fledge

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build both binaries into bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/fledged ./cmd/fledged
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/fledge ./cmd/fledge

.PHONY: install
install: ## Install the CLI into ~/bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(HOME)/bin/fledge ./cmd/fledge

.PHONY: test
test: ## Run the tests
	go test -race -shuffle=on ./...

.PHONY: lint
lint: ## Vet and, when available, staticcheck
	go vet ./...
	@command -v staticcheck >/dev/null 2>&1 \
		&& staticcheck ./... \
		|| echo "staticcheck not installed, skipping"

.PHONY: fmt
fmt: ## Format the tree
	go fmt ./...

.PHONY: icons
icons: ## Regenerate the icon set
	go generate ./internal/web/...

.PHONY: run
run: ## Serve on :8080 against ./tmp, with enrolment on
	FLEDGE_ADDR=127.0.0.1:8080 \
	FLEDGE_DATA_DIR=./tmp/fledge \
	FLEDGE_PUBLIC_URL=https://fledge.example \
	FLEDGE_UPLOAD_TOKEN=dev-token \
	FLEDGE_ENROLL_TOKEN=dev-enroll \
	go run ./cmd/fledged

.PHONY: token
token: ## Generate a token suitable for FLEDGE_UPLOAD_TOKEN
	@openssl rand -hex 32

.PHONY: clean
clean: ## Remove build output
	rm -rf bin tmp
