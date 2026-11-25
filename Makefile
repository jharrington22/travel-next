# Travel Next — podman and local-Kubernetes workflows.
# `make help` lists the targets.

IMAGE       ?= localhost/travel-next:dev
NAMESPACE   ?= travel-next
KIND_CLUSTER?= travel-next
PODMAN      ?= podman
# kind talks to podman instead of docker.
export KIND_EXPERIMENTAL_PROVIDER = podman

.DEFAULT_GOAL := help
.PHONY: help tidy fmt vet test build run db-up db-down image compose-up compose-down compose-logs \
        kind-up kind-down kind-ingress kind-load k8s-deploy k8s-delete k8s-status k8s-logs \
        k8s-prompt-reload port-forward

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?## "};{printf "  \033[1m%-18s\033[0m %s\n", $$1, $$2}'

## ---------- Go ----------

tidy: ## go mod tidy
	go mod tidy

fmt: ## Format
	gofmt -w ./cmd ./internal

vet: ## go vet
	go vet ./...

test: ## Run tests
	go test ./...

build: ## Build the binary into ./bin
	go build -o bin/server ./cmd/server

## ---------- Local, no containers for the app ----------

db-up: ## Start Postgres in podman for local `make run`
	$(PODMAN) run -d --name travel-next-db \
	  -e POSTGRES_USER=travel -e POSTGRES_PASSWORD=travel -e POSTGRES_DB=travel \
	  -p 5432:5432 docker.io/library/postgres:16-alpine

db-down: ## Stop and remove that Postgres
	-$(PODMAN) rm -f travel-next-db

run: ## Run the app on :8080 against the local Postgres
	DATABASE_URL="postgres://travel:travel@localhost:5432/travel?sslmode=disable" \
	BASE_URL="http://localhost:8080" APP_ENV=development \
	AI_PROVIDER="$${AI_PROVIDER:-stub}" \
	go run ./cmd/server

## ---------- Local, full stack in podman ----------

image: ## Build the container image with podman
	$(PODMAN) build -t $(IMAGE) .

compose-up: ## nginx + app + postgres on http://localhost:8080
	podman-compose up --build -d

compose-down: ## Stop the compose stack
	podman-compose down

compose-logs: ## Follow compose logs
	podman-compose logs -f

## ---------- Local Kubernetes (kind on podman) ----------

kind-up: ## Create the kind cluster with ports 80/443 mapped
	kind create cluster --config deploy/k8s/kind-cluster.yaml

kind-down: ## Delete the kind cluster
	kind delete cluster --name $(KIND_CLUSTER)

kind-ingress: ## Install the nginx ingress controller and wait for it
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
	kubectl -n ingress-nginx wait --for=condition=ready pod \
	  --selector=app.kubernetes.io/component=controller --timeout=180s

kind-load: image ## Build and load the image into kind
	$(PODMAN) save $(IMAGE) -o /tmp/travel-next.tar
	kind load image-archive /tmp/travel-next.tar --name $(KIND_CLUSTER)
	rm -f /tmp/travel-next.tar

k8s-deploy: ## Apply the manifests
	kubectl apply -k deploy/k8s
	kubectl -n $(NAMESPACE) rollout status deploy/travel-next --timeout=180s
	@echo "Open http://travel-next.localtest.me"

k8s-delete: ## Remove everything in the namespace
	-kubectl delete -k deploy/k8s

k8s-status: ## Pods, services and ingress
	kubectl -n $(NAMESPACE) get pods,svc,ingress

k8s-logs: ## Follow the app logs
	kubectl -n $(NAMESPACE) logs -l app=travel-next -f --tail=100

k8s-prompt-reload: ## Push deploy/k8s/prompt-context.md to the cluster (no restart)
	kubectl -n $(NAMESPACE) create configmap travel-next-prompt-context \
	  --from-file=prompt-context.md=deploy/k8s/prompt-context.md \
	  --dry-run=client -o yaml | kubectl apply -f -
	@echo "Pods reload it within ~70s. Check: kubectl -n $(NAMESPACE) exec deploy/travel-next -- wget -qO- localhost:8080/readyz"

port-forward: ## Reach the app without the ingress
	kubectl -n $(NAMESPACE) port-forward svc/travel-next 8080:80
