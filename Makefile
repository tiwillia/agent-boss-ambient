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
