.PHONY: demo-up demo-incident demo-reset demo-down help dev dev-server dev-agent dev-web build build-web test vet lint docker-build kind-deploy minikube-deploy clean

BIN_DIR := bin
IMAGE_PREFIX ?= ghcr.io/emreoztoprak/kentinel

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev: ## Run server + agent + Vite dev server together (Ctrl-C stops all)
	@trap 'kill 0' INT TERM; \
	go run ./cmd/server & \
	go run ./cmd/agent & \
	(cd web && npm run dev) & \
	wait

dev-server: ## Run only the UI backend
	go run ./cmd/server

dev-agent: ## Run only the AI agent
	go run ./cmd/agent

dev-web: ## Run only the Vite dev server
	cd web && npm run dev

build: build-web ## Build both binaries and the SPA into bin/ and web/dist/
	go build -o $(BIN_DIR)/server ./cmd/server
	go build -o $(BIN_DIR)/agent ./cmd/agent

build-web: ## Build the frontend
	cd web && npm run build

test: ## Run Go tests
	go test ./...

vet: ## Run go vet
	go vet ./...

docker-build: ## Build both container images
	docker build -f deploy/docker/Dockerfile.server -t $(IMAGE_PREFIX)-server:latest .
	docker build -f deploy/docker/Dockerfile.agent -t $(IMAGE_PREFIX)-agent:latest .

kind-deploy: docker-build ## Load images into a kind cluster and apply manifests
	kind load docker-image $(IMAGE_PREFIX)-server:latest
	kind load docker-image $(IMAGE_PREFIX)-agent:latest
	kubectl apply -f deploy/k8s/

MINIKUBE_PROFILE ?= minikube
minikube-deploy: ## Build images inside minikube's Docker daemon and apply manifests
	eval $$(minikube -p $(MINIKUBE_PROFILE) docker-env) && \
	docker build -f deploy/docker/Dockerfile.server -t $(IMAGE_PREFIX)-server:latest . && \
	docker build -f deploy/docker/Dockerfile.agent -t $(IMAGE_PREFIX)-agent:latest .
	kubectl apply -f deploy/k8s/

demo-up: ## Deploy the healthy demo shop app (namespace: shop)
	kubectl apply -f deploy/demo/01-shop.yaml

demo-incident: ## Break the demo app in four realistic ways
	kubectl apply -f deploy/demo/02-incidents.yaml

demo-reset: ## Remove the incidents (back to green)
	kubectl delete -f deploy/demo/02-incidents.yaml --ignore-not-found

demo-down: ## Delete the demo namespace entirely
	kubectl delete namespace shop --ignore-not-found

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) web/dist
