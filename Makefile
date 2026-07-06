# Cache-Pot — common developer tasks.
# Run `make help` for a list.

BINARY      := cache-pot
PKG         := ./cmd/cache-pot
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X github.com/subh05sus/cache-pot/internal/server.Version=$(VERSION)
DOCKER_IMG  ?= cache-pot:$(VERSION)

.PHONY: help build run test vet fmt cover docker clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the cache-pot binary
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

run: ## Run the server (foreground)
	go run $(PKG)

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the code
	gofmt -w .

cover: ## Run tests with a coverage summary
	go test -cover ./...

docker: ## Build the Docker image
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMG) .

clean: ## Remove build artifacts
	rm -f $(BINARY) $(BINARY).exe *.snapshot
	rm -rf dist/
