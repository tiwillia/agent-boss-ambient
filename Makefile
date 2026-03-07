# Agent Boss - Makefile
# Zero-dependency coordinator for ambient code sessions

# Variables
BINARY_NAME := boss
BINARY_PATH := /tmp/$(BINARY_NAME)
CMD_PATH := ./cmd/boss
PKG_PATH := ./internal/coordinator/

# Docker/Registry settings
REGISTRY := image-registry.openshift-image-registry.svc:5000
NAMESPACE := ambient-code
IMAGE_NAME := boss-coordinator
IMAGE_TAG := latest
FULL_IMAGE := $(REGISTRY)/$(NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

# Kubernetes settings
K8S_DIR := deploy/k8s

# Go settings
GO := go
GOFLAGS := -v
GO_BUILD_FLAGS := CGO_ENABLED=0 GOOS=linux
TEST_FLAGS := -race -v

# Colors for output
RED := \033[0;31m
GREEN := \033[0;32m
YELLOW := \033[0;33m
NC := \033[0m # No Color

.PHONY: help
help: ## Show this help message
	@echo "Agent Boss - Build Targets"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the boss binary
	@echo "$(GREEN)Building $(BINARY_NAME)...$(NC)"
	$(GO) build $(GOFLAGS) -o $(BINARY_PATH) $(CMD_PATH)
	@echo "$(GREEN)✓ Binary built: $(BINARY_PATH)$(NC)"

.PHONY: test
test: ## Run tests with race detection
	@echo "$(GREEN)Running tests with race detection...$(NC)"
	$(GO) test $(TEST_FLAGS) $(PKG_PATH)
	@echo "$(GREEN)✓ Tests passed$(NC)"

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	@echo "$(GREEN)Running tests with coverage...$(NC)"
	$(GO) test $(TEST_FLAGS) -coverprofile=coverage.out $(PKG_PATH)
	$(GO) tool cover -func=coverage.out
	@echo "$(GREEN)✓ Coverage report generated$(NC)"

.PHONY: test-e2e
test-e2e: build ## Run end-to-end tests with Playwright (requires Node.js >=18, npm >=9)
	@echo "$(GREEN)Running e2e tests with Playwright...$(NC)"
	@# Check for required Node.js and npm versions
	@command -v node >/dev/null 2>&1 || { echo "$(RED)✗ Error: node is not installed. Please install Node.js >=18.0.0$(NC)"; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "$(RED)✗ Error: npm is not installed. Please install npm >=9.0.0$(NC)"; exit 1; }
	@NODE_VERSION=$$(node --version | sed 's/v//'); \
		if [ "$$(printf '%s\n' "18.0.0" "$$NODE_VERSION" | sort -V | head -n1)" != "18.0.0" ]; then \
			echo "$(RED)✗ Error: Node.js version $$NODE_VERSION is too old. Requires >=18.0.0$(NC)"; \
			exit 1; \
		fi
	@echo "$(GREEN)✓ Node.js $$(node --version) and npm $$(npm --version) detected$(NC)"
	@# Install Playwright dependencies if needed
	@if [ ! -d e2e/node_modules ]; then \
		echo "$(YELLOW)Installing Playwright dependencies...$(NC)"; \
		cd e2e && npm install; \
	fi
	@# Ensure Playwright browsers are installed (Playwright skips if already present)
	@echo "$(YELLOW)Checking Playwright browsers...$(NC)"
	@cd e2e && npx playwright install chromium 2>&1 | grep -v "is already installed" || true
	@# Setup cleanup trap to ensure server shutdown and data cleanup even on failure
	@trap 'EXIT_CODE=$$?; \
		echo "$(YELLOW)Cleaning up test environment...$(NC)"; \
		if [ -n "$$SERVER_PID" ] && kill -0 $$SERVER_PID 2>/dev/null; then \
			kill $$SERVER_PID 2>/dev/null || true; \
			wait $$SERVER_PID 2>/dev/null || true; \
		fi; \
		rm -rf e2e-data 2>/dev/null || true; \
		if [ $$EXIT_CODE -eq 0 ]; then \
			echo "$(GREEN)✓ E2E tests passed and cleanup complete$(NC)"; \
		else \
			echo "$(RED)✗ E2E tests failed (cleanup complete)$(NC)"; \
		fi; \
		exit $$EXIT_CODE' EXIT; \
	DATA_DIR=./e2e-data $(BINARY_PATH) serve >/dev/null 2>&1 & \
	SERVER_PID=$$!; \
	echo "$(YELLOW)Started test server (PID: $$SERVER_PID)$(NC)"; \
	echo "$(YELLOW)Waiting for server to be ready (timeout: 30s)...$(NC)"; \
	TIMEOUT=30; \
	ELAPSED=0; \
	while ! curl -sf http://localhost:8899 >/dev/null 2>&1; do \
		if [ $$ELAPSED -ge $$TIMEOUT ]; then \
			echo "$(RED)✗ Server failed to start within $${TIMEOUT}s$(NC)"; \
			exit 1; \
		fi; \
		sleep 1; \
		ELAPSED=$$((ELAPSED + 1)); \
	done; \
	echo "$(GREEN)✓ Server ready after $${ELAPSED}s$(NC)"; \
	echo "$(GREEN)Running Playwright tests...$(NC)"; \
	cd e2e && npx playwright test

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "$(GREEN)Building Docker image: $(FULL_IMAGE)$(NC)"
	docker build -t $(FULL_IMAGE) -f deploy/Dockerfile .
	@echo "$(GREEN)✓ Docker image built$(NC)"

.PHONY: docker-push
docker-push: ## Push Docker image to registry
	@echo "$(GREEN)Pushing image: $(FULL_IMAGE)$(NC)"
	docker push $(FULL_IMAGE)
	@echo "$(GREEN)✓ Image pushed to registry$(NC)"

.PHONY: docker-all
docker-all: docker-build docker-push ## Build and push Docker image

.PHONY: deploy
deploy: ## Deploy to Kubernetes
	@echo "$(GREEN)Deploying to Kubernetes (namespace: $(NAMESPACE))...$(NC)"
	kubectl apply -f $(K8S_DIR)/pvc.yaml
	kubectl apply -f $(K8S_DIR)/configmap.yaml
	kubectl apply -f $(K8S_DIR)/secret.yaml
	kubectl apply -f $(K8S_DIR)/service.yaml
	kubectl apply -f $(K8S_DIR)/deployment.yaml
	@echo "$(GREEN)✓ Deployment applied$(NC)"
	@echo "$(YELLOW)Waiting for rollout...$(NC)"
	kubectl rollout status deployment/boss-coordinator -n $(NAMESPACE)

.PHONY: deploy-restart
deploy-restart: ## Restart the deployment (force pod recreation)
	@echo "$(GREEN)Restarting deployment...$(NC)"
	kubectl rollout restart deployment/boss-coordinator -n $(NAMESPACE)
	kubectl rollout status deployment/boss-coordinator -n $(NAMESPACE)
	@echo "$(GREEN)✓ Deployment restarted$(NC)"

.PHONY: logs
logs: ## Show logs from the deployed pod
	@echo "$(GREEN)Fetching logs...$(NC)"
	kubectl logs -n $(NAMESPACE) -l app=boss-coordinator --tail=100 -f

.PHONY: status
status: ## Show deployment status
	@echo "$(GREEN)Deployment status:$(NC)"
	kubectl get deployment,pod,svc -n $(NAMESPACE) -l app=boss-coordinator

.PHONY: clean
clean: ## Clean build artifacts
	@echo "$(GREEN)Cleaning build artifacts...$(NC)"
	rm -f $(BINARY_PATH)
	rm -f coverage.out
	@echo "$(GREEN)✓ Clean complete$(NC)"

.PHONY: run
run: build ## Build and run locally
	@echo "$(GREEN)Starting boss server...$(NC)"
	DATA_DIR=./data $(BINARY_PATH) serve

.PHONY: dev
dev: ## Run in development mode (auto-reload on changes - requires entr)
	@echo "$(YELLOW)Development mode - watching for changes...$(NC)"
	@find . -name '*.go' | entr -r make run

.PHONY: fmt
fmt: ## Format Go code
	@echo "$(GREEN)Formatting code...$(NC)"
	$(GO) fmt ./...
	@echo "$(GREEN)✓ Code formatted$(NC)"

.PHONY: vet
vet: ## Run go vet
	@echo "$(GREEN)Running go vet...$(NC)"
	$(GO) vet ./...
	@echo "$(GREEN)✓ Vet complete$(NC)"

.PHONY: check
check: fmt vet test ## Run all checks (fmt, vet, test)
	@echo "$(GREEN)✓ All checks passed$(NC)"

.PHONY: all
all: clean check build ## Run full build pipeline
	@echo "$(GREEN)✓ Build pipeline complete$(NC)"

# Default target
.DEFAULT_GOAL := help
