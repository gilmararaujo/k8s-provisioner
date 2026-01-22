.PHONY: build build-all clean deps test help release tag

BINARY_NAME=k8s-provisioner
BUILD_DIR=build

# Versão do Git (tag ou commit)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

# Pacote de versão
VERSION_PKG=github.com/techiescamp/k8s-provisioner/internal/version

# Build flags com injeção de versão
LDFLAGS=-ldflags "\
	-s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).GitCommit=$(GIT_COMMIT) \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)"

help: ## Show this help
	@echo "k8s-provisioner $(VERSION)"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build for current OS
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .

build-darwin-arm64: ## Build for macOS Apple Silicon
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 .

build-darwin-amd64: ## Build for macOS Intel
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 .

build-linux-amd64: ## Build for Linux x64
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .

build-linux-arm64: ## Build for Linux ARM64
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 .

build-windows-amd64: ## Build for Windows x64
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe .

build-all: build-darwin-arm64 build-darwin-amd64 build-linux-arm64 build-linux-amd64 build-windows-amd64 ## Build for all platforms
	@echo ""
	@echo "Build complete! Version: $(VERSION)"
	@ls -la $(BUILD_DIR)/

clean: ## Clean build artifacts
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

test: ## Run tests
	$(GOTEST) -v ./...

# Release targets
tag: ## Create a new version tag (usage: make tag v=1.0.0)
	@if [ -z "$(v)" ]; then echo "Usage: make tag v=1.0.0"; exit 1; fi
	@echo "Creating tag $(v)..."
	git tag -a $(v) -m "Release $(v)"
	@echo "Tag $(v) created. Push with: git push origin $(v)"

release: clean build-all ## Build release for all platforms
	@echo ""
	@echo "==========================================="
	@echo "  Release $(VERSION) ready!"
	@echo "==========================================="
	@echo ""
	@echo "Binaries:"
	@ls -la $(BUILD_DIR)/
	@echo ""
	@echo "Next steps:"
	@echo "  1. git add ."
	@echo "  2. git commit -m 'Release $(VERSION)'"
	@echo "  3. git tag -a $(VERSION) -m 'Release $(VERSION)'"
	@echo "  4. git push origin main --tags"

version: ## Show current version
	@echo "Version:    $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"