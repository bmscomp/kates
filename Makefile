.PHONY: all detect cluster monitoring deploy-all kafka kafka-deploy kafka-upgrade kafka-undeploy kafka-detect kafka-verify-policies kafka-deploy-auto kafka-deploy-generic ui ui-deploy ui-upgrade ui-undeploy ui-chart-lint ui-chart-template test test-load test-stress test-spike test-endurance test-volume test-capacity destroy clean download-charts litmus litmus-generic litmus-undeploy litmus-test litmus-gameday kates kates-generic kates-prod kates-build kates-native kates-deploy kates-logs kates-undeploy kates-helm kates-helm-deploy kates-helm-upgrade kates-helm-undeploy kates-helm-test kates-secret cli-build cli-install cli-clean logs chaos-ui chaos-status chaos-helm-test chart-lint chart-package chart-push connect-chart-lint connect-chart-template connect-chart-package connect-chart-push connect-chart-test connect-chart-all connect-deploy connect-undeploy kafka-chart-test helm-test-all gameday jaeger kyverno kyverno-undeploy book-html book-pdf book-clean

.DEFAULT_GOAL := help

TIMER := $(shell date +%s)

# ── Load version pins ─────────────────────────────────────────────────────────
include versions.env

# ── Resolve kates binary (installed CLI > local build) ────────────────────────
KATES_BIN := $(shell command -v kates 2>/dev/null || echo "./build/kates")
DETECTED_VALUES := .build/values-detected.yaml

# ── Cluster detection (single source of truth) ───────────────────────────────
detect: check-prerequisites
	@mkdir -p .build
	@echo "🔍 Detecting cluster configuration..."
	@if [ ! -x "$(KATES_BIN)" ]; then \
		echo "⚠️  kates binary not found, building it now..."; \
		$(MAKE) cli-build; \
		if [ ! -x "$(KATES_BIN)" ]; then cp cli/dist/kates $(KATES_BIN) 2>/dev/null || true; fi; \
	fi
	@$(KATES_BIN) detect --generate-values --values-output $(DETECTED_VALUES) --quiet
	@echo "✅ Detection complete → $(DETECTED_VALUES)"

# ── Main deployment pipeline ─────────────────────────────────────────────────
all: check-prerequisites
	@echo "🚀 Launching complete cluster setup via Kates Unified Orchestrator..."
	@echo ""
	@# ── Step 1: Cluster connectivity ──
	@if kubectl cluster-info >/dev/null 2>&1; then \
		CONTEXT=$$(kubectl config current-context); \
		echo "✅ Kubernetes cluster reachable (context: $$CONTEXT)"; \
	else \
		echo "⚠️  No Kubernetes cluster reachable — creating Kind cluster..."; \
		$(MAKE) cluster; \
	fi
	@echo ""
	@if [ ! -x "$(KATES_BIN)" ]; then \
		echo "⚠️  kates binary not found, building it now..."; \
		$(MAKE) cli-build >/dev/null; \
	fi
	@echo "Select Deployment Topology:"
	@echo "  1) Single Namespace (dev mode, everything in 'kates-stack')"
	@echo "  2) Isolated Namespaces (production mode, separate namespaces for kafka/kates/chaos)"
	@read -p "Enter choice [1/2]: " choice; \
	if [ "$$choice" = "1" ]; then \
		$(KATES_BIN) deploy --topology single --namespace kates-stack --with-schema-registry apicurio; \
	else \
		$(KATES_BIN) deploy --topology isolated --with-schema-registry apicurio; \
	fi
	@echo ""
	@echo "Step: Exposing service ports..."
	@./scripts/port-forward.sh || true
	@echo ""
	@echo "✅ Complete setup finished in $$(( $$(date +%s) - $(TIMER) ))s"
	@echo ""
	@echo "🔗 Access points:"
	@echo "  - Apicurio Registry: http://localhost:30082"
	@echo "  - Kates:             http://localhost:30083"
	@echo "  - Litmus UI:         http://localhost:9091  (admin/litmus)"
	@echo ""

# Check prerequisites — only kubectl and helm are strictly required for generic clusters
check-prerequisites:
	@echo "🔍 Checking prerequisites..."
	@command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl not found"; exit 1; }
	@command -v helm >/dev/null 2>&1 || { echo "❌ helm not found"; exit 1; }
	@echo "✅ All prerequisites met"

# Start Kind cluster only
cluster:
	@echo "🎯 Starting Kind cluster..."
	./scripts/start-cluster.sh

# Deploy monitoring stack (auto-detect provider)
monitoring:
	@echo "📊 Deploying monitoring stack..."
	@helm dependency build charts/monitoring 2>/dev/null || true
	@PROVIDER="generic"; \
	if [ -f "$(DETECTED_VALUES)" ]; then \
		PROVIDER=$$(grep '^# Provider:' $(DETECTED_VALUES) | awk '{print $$3}'); \
	fi; \
	MON_VALUES="charts/monitoring/values-generic.yaml"; \
	if [ "$$PROVIDER" = "kind" ] && [ -f "charts/monitoring/values-kind.yaml" ]; then \
		MON_VALUES="charts/monitoring/values-kind.yaml"; \
	fi; \
	echo "  Provider: $$PROVIDER → $$MON_VALUES"; \
	helm upgrade --install monitoring charts/monitoring \
		--namespace kafka --create-namespace \
		-f "$$MON_VALUES" \
		--timeout 10m --wait
	@echo "✅ Monitoring stack deployed"

monitoring-generic:
	@echo "📊 Deploying monitoring stack (Generic)..."
	helm dependency build charts/monitoring
	helm upgrade --install monitoring charts/monitoring \
		--namespace kafka --create-namespace \
		-f charts/monitoring/values-generic.yaml \
		--timeout 10m --wait

monitoring-undeploy:
	@echo "🗑️ Undeploying monitoring stack..."
	helm uninstall monitoring -n kafka || true
	kubectl delete pvc --all -n kafka || true
	kubectl # delete namespace monitoring || true

cert-manager:
	@echo "🔐 Deploying cert-manager..."
	./scripts/deploy-cert-manager.sh

kyverno:
	@echo "🛡️  Deploying Kyverno policy engine..."
	helm repo add kyverno https://kyverno.github.io/kyverno/ 2>/dev/null || true
	helm repo update kyverno 2>/dev/null || true
	helm upgrade --install kyverno kyverno/kyverno \
		-n kyverno --create-namespace \
		--set admissionController.replicas=1 \
		--timeout 5m --wait
	@echo "✅ Kyverno deployed"
	@kubectl get pods -n kyverno

kyverno-undeploy:
	@echo "🗑️ Removing Kyverno..."
	helm uninstall kyverno -n kyverno || true
	kubectl delete namespace kyverno --ignore-not-found
	@echo "✅ Kyverno removed"

# Deploy full stack (monitoring, Kafka, UI, Litmus) — without cluster/images
deploy-all:
	@echo "🚀 Deploying full stack..."
	$(MAKE) monitoring
	./scripts/deploy-kafka-generic.sh --yes
	./scripts/deploy-kafka-ui.sh
	./scripts/deploy-apicurio.sh
	./scripts/deploy-jaeger.sh
	$(MAKE) litmus
	./scripts/port-forward.sh
	@echo "✅ Full stack deployed!"

ENV ?= kind

# Deploy Kafka (shorthand for kafka-deploy)
kafka: kafka-deploy

# Deploy Kafka UI only (legacy script — applies raw manifests)
ui:
	@echo "🖥️ Deploying Kafka UI (raw manifests)..."
	./scripts/deploy-kafka-ui.sh

# ── Kafka UI Helm Chart ─────────────────────────────────────────────────────
UI_CHART_DIR := charts/kafka-ui

ui-chart-lint:
	@echo "🔍 Linting Kafka UI chart..."
	helm lint $(UI_CHART_DIR)
	@echo "✅ Kafka UI chart lint passed"

ui-chart-template:
	@echo "📄 Rendering Kafka UI templates (ENV=$(ENV))..."
	@OVERLAY=""; \
	if [ -f "$(UI_CHART_DIR)/values-$(ENV).yaml" ]; then \
		OVERLAY="-f $(UI_CHART_DIR)/values-$(ENV).yaml"; \
	fi; \
	helm template kafka-ui $(UI_CHART_DIR) \
		--namespace kafka $$OVERLAY

ui-deploy:
	@echo "🖥️  Deploying Kafka UI via Helm (ENV=$(ENV))..."
	@OVERLAY=""; \
	if [ -f "$(UI_CHART_DIR)/values-$(ENV).yaml" ]; then \
		OVERLAY="-f $(UI_CHART_DIR)/values-$(ENV).yaml"; \
		echo "  Using overlay: values-$(ENV).yaml"; \
	fi; \
	helm upgrade --install kafka-ui $(UI_CHART_DIR) \
		--namespace kafka --create-namespace \
		$$OVERLAY \
		--timeout 5m --wait
	@echo "✅ Kafka UI deployed"

ui-upgrade:
	@echo "🔄 Upgrading Kafka UI (ENV=$(ENV))..."
	@OVERLAY=""; \
	if [ -f "$(UI_CHART_DIR)/values-$(ENV).yaml" ]; then \
		OVERLAY="-f $(UI_CHART_DIR)/values-$(ENV).yaml"; \
		echo "  Using overlay: values-$(ENV).yaml"; \
	fi; \
	helm upgrade kafka-ui $(UI_CHART_DIR) \
		--namespace kafka --reuse-values \
		$$OVERLAY \
		--timeout 5m --wait
	@echo "✅ Kafka UI upgraded"

ui-undeploy:
	@echo "🗑️  Removing Kafka UI..."
	helm uninstall kafka-ui -n kafka 2>/dev/null || true
	@echo "✅ Kafka UI removed"

# Deploy Apicurio Registry
apicurio:
	@echo "📝 Deploying Apicurio Registry..."
	./scripts/deploy-apicurio.sh

jaeger:
	@echo "🔍 Deploying Jaeger (distributed tracing)..."
	./scripts/deploy-jaeger.sh

# Run Performance Test
test:
	@echo "🧪 Running Performance Test..."
	./scripts/test-kafka-performance.sh

test-load:
	@echo "🧪 Running Load Test..."
	./scripts/test-perf-load.sh

test-stress:
	@echo "🧪 Running Stress Test..."
	./scripts/test-perf-stress.sh

test-spike:
	@echo "🧪 Running Spike Test..."
	./scripts/test-perf-spike.sh

test-endurance:
	@echo "🧪 Running Endurance (Soak) Test..."
	./scripts/test-perf-endurance.sh

test-volume:
	@echo "🧪 Running Volume Test..."
	./scripts/test-perf-volume.sh

test-capacity:
	@echo "🧪 Running Capacity Test..."
	./scripts/test-perf-capacity.sh

test-net-kafka:
	@domain=$$(source scripts/common.sh && get_cluster_domain); \
	echo "🌐 Testing TCP connectivity to Kafka from 'default' namespace (Domain: $$domain)..."; \
	kubectl run -i --tty --rm debug-nc --image=busybox:1.36 --namespace=default --restart=Never -- nc -vz krafter-kafka-bootstrap.kafka.svc.$$domain 9092

test-net-api:
	@domain=$$(source scripts/common.sh && get_cluster_domain); \
	echo "🌐 Testing HTTP connectivity to Kates API from 'default' namespace (Domain: $$domain)..."; \
	kubectl run -i --tty --rm debug-curl --image=curlimages/curl:8.7.1 --namespace=default --restart=Never -- curl -sv http://kates.kafka.svc.$$domain:8080/api/health

test-net: test-net-kafka test-net-api
	@echo "✅ Cross-namespace network tests complete."

cluster-domain:
	@domain=$$(source scripts/common.sh && get_cluster_domain); \
	echo "🌐 Cluster domain: $$domain"

# Kates CLI (standalone install)
cli-build:
	@echo "🔨 Building Kates CLI locally..."
	cd cli && go build -ldflags="-s -w" -o dist/kates .
	@echo "🔨 Cross-compiling Kates CLI for all platforms..."
	cd cli && bash build.sh

cli-install:
	@echo "🔨 Building Kates CLI from source..."
	cd cli && go build -ldflags="-s -w" -o dist/kates .
	@echo "📦 Installing to /usr/local/bin/kates..."
	sudo cp cli/dist/kates /usr/local/bin/kates
	sudo xattr -dr com.apple.provenance /usr/local/bin/kates 2>/dev/null || true
	sudo xattr -dr com.apple.quarantine /usr/local/bin/kates 2>/dev/null || true
	sudo codesign -f -s - /usr/local/bin/kates
	@echo "✅ Installed: $$(kates version 2>/dev/null || echo '/usr/local/bin/kates')"

cli-clean:
	@echo "🧹 Removing CLI build artifacts..."
	rm -rf cli/dist

# Kates Application (Docker + Kind)
kates: kates-build kates-deploy
	@echo "✅ Kates deployed! Run 'make ports' to access at http://localhost:30083"

kates-build:
	@if docker image inspect kates:latest >/dev/null 2>&1; then \
		echo "✅ Kates image already exists locally (kates:latest)."; \
	elif docker pull ghcr.io/bmscomp/kates:1.16.0; then \
		echo "✅ Pulled Kates image from registry."; \
		docker tag ghcr.io/bmscomp/kates:1.16.0 kates:latest; \
	else \
		echo "🔨 Building Kates (JVM + CLI) from source..."; \
		cd kates && ./mvnw package -DskipTests -B && \
		cd .. && docker build -f kates/Dockerfile -t kates:latest .; \
	fi
	kind load docker-image kates:latest --name $(CLUSTER_NAME)
	@echo "✅ Kates image loaded into Kind"

kates-native:
	@if docker image inspect kates:native >/dev/null 2>&1; then \
		echo "✅ Kates native image already exists locally (kates:native)."; \
	elif docker pull ghcr.io/bmscomp/kates:1.16.0-native; then \
		echo "✅ Pulled Kates native image from registry."; \
		docker tag ghcr.io/bmscomp/kates:1.16.0-native kates:native; \
	else \
		echo "🔨 Building Kates (native) from source..."; \
		docker build -f kates/Dockerfile.native -t kates:native .; \
	fi
	kind load docker-image kates:native --name $(CLUSTER_NAME)
	@echo "✅ Kates native image loaded into Kind"

tester-build:
	@echo "🔨 Building Kates Tester image..."
	docker build -f tester/Dockerfile -t kates-tester:latest tester/
	kind load docker-image kates-tester:latest --name $(CLUSTER_NAME) 2>/dev/null || true
	@echo "✅ Kates Tester image built and available"

connect-build:
	@echo "🔌 Building Kafka Connect image with enterprise plugins..."
	@DBZ_VERSION=$$(grep '^ARG DEBEZIUM_VERSION=' Dockerfile.connect | head -n1 | cut -d= -f2); \
	TAG=$${DBZ_VERSION%.Final}; \
	echo "  Debezium: $${DBZ_VERSION}  →  connect:$${TAG}"; \
	docker build -t connect:$${TAG} -t connect:latest \
		-t ghcr.io/bmscomp/connect:$${TAG} \
		-t ghcr.io/bmscomp/connect:latest \
		-f Dockerfile.connect . && \
	if kind get clusters 2>/dev/null | grep -q "$(CLUSTER_NAME)"; then \
		echo "Loading into Kind cluster ($(CLUSTER_NAME))..."; \
		kind load docker-image connect:$${TAG} --name $(CLUSTER_NAME); \
	fi && \
	echo "✅ connect:$${TAG} built successfully" && \
	echo "   Plugins: debezium-postgres, debezium-mysql, debezium-mongodb, debezium-sqlserver, debezium-oracle, debezium-db2, apicurio-converter, debezium-jdbc, debezium-scripting"

connect-push:
	@echo "🚀 Pushing Kafka Connect image to $(REGISTRY)..."
	@DBZ_VERSION=$$(grep '^ARG DEBEZIUM_VERSION=' Dockerfile.connect | head -n1 | cut -d= -f2); \
	TAG=$${DBZ_VERSION%.Final}; \
	docker push $(REGISTRY)/connect:$${TAG} && \
	docker push $(REGISTRY)/connect:latest && \
	echo "✅ Pushed: $(REGISTRY)/connect:$${TAG}"

REGISTRY ?= ghcr.io/bmscomp
push-images:
	@echo "🚀 Pushing images to $(REGISTRY)..."
	docker tag kates:latest $(REGISTRY)/kates:latest
	docker push $(REGISTRY)/kates:latest
	docker tag kates-tester:latest $(REGISTRY)/kates-tester:latest
	docker push $(REGISTRY)/kates-tester:latest
	@echo "✅ Images pushed successfully to $(REGISTRY)!"

kates-deploy:
	@echo "🚀 Deploying Kates to Kubernetes..."
	kubectl apply -f kates/k8s/namespace.yaml
	kubectl apply -f kates/k8s/rbac.yaml
	kubectl apply -f kates/k8s/configmap.yaml
	@echo "Copying Kafka SASL credentials to kates namespace..."
	@kubectl get secret kates-backend -n kafka -o json \
		| jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
		| kubectl apply -n kafka -f -
	kubectl apply -f kates/k8s/postgres.yaml
	@echo "Waiting for PostgreSQL to be ready..."
	@kubectl wait --for=condition=Ready pod -l app=postgres -n kafka --timeout=120s
	kubectl apply -f kates/k8s/deployment.yaml
	kubectl apply -f kates/k8s/service.yaml
	kubectl rollout status deployment/kates -n kafka --timeout=300s
	@echo "✅ Kates is running"
	@echo "Configuring local Kates CLI context..."
	@if command -v kates >/dev/null 2>&1; then \
		kates ctx set local --url "http://localhost:30083" --api-key "changeme" >/dev/null 2>&1 || true; \
		kates ctx use local >/dev/null 2>&1 || true; \
		echo "✅ Kates CLI context 'local' configured automatically!"; \
	elif [ -x "cli/dist/kates" ]; then \
		cli/dist/kates ctx set local --url "http://localhost:30083" --api-key "changeme" >/dev/null 2>&1 || true; \
		cli/dist/kates ctx use local >/dev/null 2>&1 || true; \
		echo "✅ Kates CLI context 'local' configured automatically!"; \
	else \
		echo "⚠️ Kates CLI not found. To configure manually run:"; \
		echo "  kates ctx set local --url http://localhost:30083 --api-key changeme"; \
	fi

kates-redeploy:
	@echo "🔄 Redeploying Kates..."
	kubectl rollout restart deployment/kates -n kafka
	kubectl rollout status deployment/kates -n kafka --timeout=300s

kates-secret:
	@echo "🔐 Setting up Kafka SASL credentials in kates namespace..."
	@./scripts/ensure-kafka-user.sh || true
	@if kubectl get secret kates-backend -n kafka >/dev/null 2>&1; then \
		echo "Copying from kafka namespace..."; \
		kubectl get secret kates-backend -n kafka -o json \
			| jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
			| kubectl apply -n $(KATES_NS) -f -; \
		echo "✅ Secret copied successfully"; \
	else \
		PWD="$(PASSWORD)"; \
		if [ -z "$$PWD" ]; then \
			PWD="changeme"; \
			echo "⚠️  No password provided. Using default password 'changeme'."; \
			echo "   To set a custom password, use: make kates-secret PASSWORD=your_password"; \
		fi; \
		echo "Creating secret manually..."; \
		kubectl create secret generic kates-backend -n $(KATES_NS) --from-literal=password="$$PWD" --dry-run=client -o yaml | kubectl apply -f -; \
		echo "✅ Secret created successfully"; \
	fi

kates-logs:
	@echo "📋 Streaming Kates logs..."
	kubectl logs -f -l app=kates -n kafka

kates-undeploy:
	@echo "🗑️  Removing Kates..."
	kubectl # kubectl delete namespace kates --ignore-not-found
	@echo "✅ Kates removed"

CLUSTER_NAME   ?= panda
KATES_NS       ?= kafka
KATES_IMAGE    ?= kates:latest
CHART_REGISTRY ?= oci://ghcr.io/klster/charts
CHART_DIR      := charts/kates
CHART_VERSION  := $(shell grep '^version:' $(CHART_DIR)/Chart.yaml | awk '{print $$2}')
kates-helm: kates-helm-deploy

kates-helm-deploy:
	@echo "📦 Deploying Kates via Helm (ENV=$(ENV))..."
	ENV=$(ENV) ./scripts/deploy-kates.sh

kates-helm-upgrade:
	@echo "🔄 Upgrading Kates via Helm (ENV=$(ENV))..."
	ENV=$(ENV) ./scripts/deploy-kates.sh

kates-generic:
	@echo "📦 Deploying Kates via Helm (generic Kubernetes)..."
	ENV=generic ./scripts/deploy-kates.sh

kates-prod:
	@echo "📦 Deploying Kates via Helm (production)..."
	ENV=prod ./scripts/deploy-kates.sh

kates-helm-undeploy:
	@echo "🗑️  Removing Kates (Helm release)..."
	helm uninstall kates -n $(KATES_NS) 2>/dev/null || true
	kubectl delete namespace $(KATES_NS) --ignore-not-found
	@echo "✅ Kates Helm release removed"

chart-lint:
	@echo "🔍 Linting Kates chart..."
	helm lint $(CHART_DIR) --strict
	@if command -v ct >/dev/null 2>&1; then \
		ct lint --config ct.yaml --charts $(CHART_DIR); \
	else \
		echo "⚠️  chart-testing (ct) not found — skipping ct lint"; \
	fi
	@echo "✅ Chart lint passed"

chart-package:
	@echo "📦 Packaging Kates chart v$(CHART_VERSION)..."
	helm package $(CHART_DIR) --destination .build/
	@echo "✅ Chart packaged: .build/kates-$(CHART_VERSION).tgz"

chart-push: chart-package
	@echo "🚀 Pushing to $(CHART_REGISTRY)..."
	helm push .build/kates-$(CHART_VERSION).tgz $(CHART_REGISTRY)
	@echo "✅ Chart pushed: $(CHART_REGISTRY)/kates:$(CHART_VERSION)"

KAFKA_CHART_DIR     := charts/kafka-cluster
KAFKA_CHART_VERSION := $(shell grep '^version:' $(KAFKA_CHART_DIR)/Chart.yaml | awk '{print $$2}')

kafka-chart-deps:
	helm dependency build $(KAFKA_CHART_DIR)

kafka-chart-lint: kafka-chart-deps
	@echo "🔍 Linting kafka-cluster chart (all environments)..."
	helm lint $(KAFKA_CHART_DIR)
	helm lint $(KAFKA_CHART_DIR) -f $(KAFKA_CHART_DIR)/values-dev.yaml
	helm lint $(KAFKA_CHART_DIR) -f $(KAFKA_CHART_DIR)/values-staging.yaml
	helm lint $(KAFKA_CHART_DIR) -f $(KAFKA_CHART_DIR)/values-prod.yaml
	@echo "✅ Kafka chart lint passed"

kafka-chart-template: kafka-chart-deps
	@mkdir -p .build
	helm template kafka-cluster $(KAFKA_CHART_DIR) \
		--namespace kafka \
		--set strimziOperator.enabled=false \
		--set crdUpgrade.enabled=false \
		> .build/kafka-rendered.yaml
	@echo "Rendered $$(grep -c '^kind:' .build/kafka-rendered.yaml) resources → .build/kafka-rendered.yaml"

kafka-chart-package: kafka-chart-deps
	@mkdir -p .build
	helm package $(KAFKA_CHART_DIR) --destination .build/
	@echo "✅ Kafka chart packaged: .build/kafka-cluster-$(KAFKA_CHART_VERSION).tgz"

kafka-chart-push: kafka-chart-package
	helm push .build/kafka-cluster-$(KAFKA_CHART_VERSION).tgz $(CHART_REGISTRY)
	@echo "✅ Kafka chart pushed: $(CHART_REGISTRY)/kafka-cluster:$(KAFKA_CHART_VERSION)"

kafka-chart-test:
	@echo ""
	@echo "╭────────────────────────────────────────────────────────────────╮"
	@echo "│  🧪 Helm Test · Kafka Cluster                                 │"
	@echo "╰────────────────────────────────────────────────────────────────╯"
	@echo ""
	@KAFKA_RELEASE=$${KAFKA_RELEASE:-kafka-cluster}; \
	KAFKA_NAMESPACE=$${KAFKA_NAMESPACE:-kafka}; \
	TIMEOUT=$${TIMEOUT:-180s}; \
	echo "  Release:    $$KAFKA_RELEASE"; \
	echo "  Namespace:  $$KAFKA_NAMESPACE"; \
	echo "  Timeout:    $$TIMEOUT"; \
	echo ""; \
	helm test $$KAFKA_RELEASE --namespace $$KAFKA_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; \
	EXIT=$$?; \
	echo ""; \
	if [ $$EXIT -eq 0 ]; then \
		echo "  ✅ Kafka cluster tests passed"; \
	else \
		echo "  ❌ Kafka cluster tests failed (exit $$EXIT)"; \
		echo ""; \
		echo "  Diagnostics:"; \
		echo "    kubectl get pods -n $$KAFKA_NAMESPACE -l helm.sh/hook=test --no-headers"; \
		kubectl get pods -n $$KAFKA_NAMESPACE -l helm.sh/hook=test --no-headers 2>/dev/null | sed 's/^/    /'; \
		echo ""; \
		echo "  View failed pod logs:"; \
		for POD in $$(kubectl get pods -n $$KAFKA_NAMESPACE -l helm.sh/hook=test --field-selector=status.phase=Failed -o name 2>/dev/null); do \
			echo "    kubectl logs $$POD -n $$KAFKA_NAMESPACE"; \
		done; \
	fi; \
	exit $$EXIT

kates-helm-test:
	@echo ""
	@echo "╭────────────────────────────────────────────────────────────────╮"
	@echo "│  🧪 Helm Test · Kates API                                     │"
	@echo "╰────────────────────────────────────────────────────────────────╯"
	@echo ""
	@KATES_RELEASE=$${KATES_RELEASE:-kates}; \
	KATES_NAMESPACE=$${KATES_NAMESPACE:-$(KATES_NS)}; \
	TIMEOUT=$${TIMEOUT:-120s}; \
	echo "  Release:    $$KATES_RELEASE"; \
	echo "  Namespace:  $$KATES_NAMESPACE"; \
	echo "  Timeout:    $$TIMEOUT"; \
	echo ""; \
	helm test $$KATES_RELEASE --namespace $$KATES_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; \
	EXIT=$$?; \
	echo ""; \
	if [ $$EXIT -eq 0 ]; then \
		echo "  ✅ Kates API tests passed"; \
	else \
		echo "  ❌ Kates API tests failed (exit $$EXIT)"; \
		echo ""; \
		echo "  Diagnostics:"; \
		kubectl get pods -n $$KATES_NAMESPACE -l helm.sh/hook=test --no-headers 2>/dev/null | sed 's/^/    /'; \
	fi; \
	exit $$EXIT

chaos-helm-test:
	@echo ""
	@echo "╭────────────────────────────────────────────────────────────────╮"
	@echo "│  🧪 Helm Test · Chaos (LitmusChaos)                           │"
	@echo "╰────────────────────────────────────────────────────────────────╯"
	@echo ""
	@CHAOS_RELEASE=$${CHAOS_RELEASE:-chaos}; \
	CHAOS_NAMESPACE=$${CHAOS_NAMESPACE:-kafka}; \
	TIMEOUT=$${TIMEOUT:-120s}; \
	echo "  Release:    $$CHAOS_RELEASE"; \
	echo "  Namespace:  $$CHAOS_NAMESPACE"; \
	echo "  Timeout:    $$TIMEOUT"; \
	echo ""; \
	helm test $$CHAOS_RELEASE --namespace $$CHAOS_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; \
	EXIT=$$?; \
	echo ""; \
	if [ $$EXIT -eq 0 ]; then \
		echo "  ✅ Chaos tests passed"; \
	else \
		echo "  ❌ Chaos tests failed (exit $$EXIT)"; \
	fi; \
	exit $$EXIT

# ── Unified Helm Test Suite ──────────────────────────────────────────────────
# Run all Helm tests across every component, with a final summary.
# Configurable via environment variables:
#   KAFKA_RELEASE    Kafka Helm release name      (default: kafka-cluster)
#   KAFKA_NAMESPACE  Kafka namespace               (default: kafka)
#   KATES_RELEASE    Kates Helm release name       (default: kates)
#   KATES_NAMESPACE  Kates namespace               (default: kafka)
#   CHAOS_RELEASE    Chaos Helm release name       (default: chaos)
#   CHAOS_NAMESPACE  Chaos namespace               (default: kafka)
#   TIMEOUT          Helm test timeout per suite   (default: 180s)
#   SKIP_CHAOS       Set to 1 to skip chaos tests  (default: 0)
#   SKIP_KATES       Set to 1 to skip kates tests  (default: 0)
helm-test-all:
	@echo ""
	@echo "╔════════════════════════════════════════════════════════════════╗"
	@echo "║  🧪 Helm Test Suite · All Components                          ║"
	@echo "╚════════════════════════════════════════════════════════════════╝"
	@echo ""
	@TOTAL=0; PASSED=0; FAILED=0; SKIPPED=0; \
	KAFKA_RELEASE=$${KAFKA_RELEASE:-kafka-cluster}; \
	KAFKA_NAMESPACE=$${KAFKA_NAMESPACE:-kafka}; \
	KATES_RELEASE=$${KATES_RELEASE:-kates}; \
	KATES_NAMESPACE=$${KATES_NAMESPACE:-$(KATES_NS)}; \
	CHAOS_RELEASE=$${CHAOS_RELEASE:-chaos}; \
	CHAOS_NAMESPACE=$${CHAOS_NAMESPACE:-kafka}; \
	TIMEOUT=$${TIMEOUT:-180s}; \
	SKIP_CHAOS=$${SKIP_CHAOS:-0}; \
	SKIP_KATES=$${SKIP_KATES:-0}; \
	echo "  Configuration:"; \
	echo "    Kafka:   $$KAFKA_RELEASE  (ns: $$KAFKA_NAMESPACE)"; \
	echo "    Kates:   $$KATES_RELEASE  (ns: $$KATES_NAMESPACE)"; \
	echo "    Chaos:   $$CHAOS_RELEASE  (ns: $$CHAOS_NAMESPACE)"; \
	echo "    Timeout: $$TIMEOUT"; \
	echo ""; \
	echo "──────────────────────────────────────────────────────────────────"; \
	echo "  ▸ Kafka Cluster"; \
	TOTAL=$$((TOTAL+1)); \
	if helm status $$KAFKA_RELEASE -n $$KAFKA_NAMESPACE >/dev/null 2>&1; then \
		if helm test $$KAFKA_RELEASE -n $$KAFKA_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; then \
			PASSED=$$((PASSED+1)); \
			echo "  ✅ Kafka: PASSED"; \
		else \
			FAILED=$$((FAILED+1)); \
			echo "  ❌ Kafka: FAILED"; \
		fi; \
	else \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Kafka: SKIPPED (release $$KAFKA_RELEASE not found in $$KAFKA_NAMESPACE)"; \
	fi; \
	echo ""; \
	echo "──────────────────────────────────────────────────────────────────"; \
	echo "  ▸ Kates API"; \
	TOTAL=$$((TOTAL+1)); \
	if [ "$$SKIP_KATES" = "1" ]; then \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Kates: SKIPPED (SKIP_KATES=1)"; \
	elif helm status $$KATES_RELEASE -n $$KATES_NAMESPACE >/dev/null 2>&1; then \
		if helm test $$KATES_RELEASE -n $$KATES_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; then \
			PASSED=$$((PASSED+1)); \
			echo "  ✅ Kates: PASSED"; \
		else \
			FAILED=$$((FAILED+1)); \
			echo "  ❌ Kates: FAILED"; \
		fi; \
	else \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Kates: SKIPPED (release $$KATES_RELEASE not found in $$KATES_NAMESPACE)"; \
	fi; \
	echo ""; \
	echo "──────────────────────────────────────────────────────────────────"; \
	echo "  ▸ Chaos (LitmusChaos)"; \
	TOTAL=$$((TOTAL+1)); \
	if [ "$$SKIP_CHAOS" = "1" ]; then \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Chaos: SKIPPED (SKIP_CHAOS=1)"; \
	elif helm status $$CHAOS_RELEASE -n $$CHAOS_NAMESPACE >/dev/null 2>&1; then \
		if helm test $$CHAOS_RELEASE -n $$CHAOS_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; then \
			PASSED=$$((PASSED+1)); \
			echo "  ✅ Chaos: PASSED"; \
		else \
			FAILED=$$((FAILED+1)); \
			echo "  ❌ Chaos: FAILED"; \
		fi; \
	else \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Chaos: SKIPPED (release $$CHAOS_RELEASE not found in $$CHAOS_NAMESPACE)"; \
	fi; \
	echo ""; \
	echo "──────────────────────────────────────────────────────────────────"; \
	echo "  ▸ Kafka Connect"; \
	CONNECT_RELEASE=$${CONNECT_RELEASE:-connect-cluster}; \
	CONNECT_NAMESPACE=$${CONNECT_NAMESPACE:-kafka}; \
	TOTAL=$$((TOTAL+1)); \
	if helm status $$CONNECT_RELEASE -n $$CONNECT_NAMESPACE >/dev/null 2>&1; then \
		if helm test $$CONNECT_RELEASE -n $$CONNECT_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; then \
			PASSED=$$((PASSED+1)); \
			echo "  ✅ Connect: PASSED"; \
		else \
			FAILED=$$((FAILED+1)); \
			echo "  ❌ Connect: FAILED"; \
		fi; \
	else \
		SKIPPED=$$((SKIPPED+1)); \
		echo "  ⏭  Connect: SKIPPED (release $$CONNECT_RELEASE not found in $$CONNECT_NAMESPACE)"; \
	fi; \
	echo ""; \
	echo "╔════════════════════════════════════════════════════════════════╗"; \
	echo "║  Results: $$PASSED passed · $$FAILED failed · $$SKIPPED skipped     ║"; \
	echo "╚════════════════════════════════════════════════════════════════╝"; \
	echo ""; \
	[ $$FAILED -eq 0 ]

kafka-chart-all: kafka-chart-deps kafka-chart-lint kafka-chart-template kafka-chart-package
	@echo "✅ All kafka chart checks passed: .build/kafka-cluster-$(KAFKA_CHART_VERSION).tgz"

CONNECT_CHART_DIR     := charts/connect-cluster
CONNECT_CHART_VERSION := $(shell grep '^version:' $(CONNECT_CHART_DIR)/Chart.yaml | awk '{print $$2}')

connect-chart-lint:
	@echo "🔍 Linting connect-cluster chart..."
	helm lint $(CONNECT_CHART_DIR)
	@echo "✅ Connect chart lint passed"

connect-chart-template:
	@mkdir -p .build
	helm template connect-cluster $(CONNECT_CHART_DIR) \
		--namespace kafka \
		> .build/connect-rendered.yaml
	@echo "Rendered $$(grep -c '^kind:' .build/connect-rendered.yaml) resources → .build/connect-rendered.yaml"

connect-chart-package:
	@mkdir -p .build
	helm package $(CONNECT_CHART_DIR) --destination .build/
	@echo "✅ Connect chart packaged: .build/connect-cluster-$(CONNECT_CHART_VERSION).tgz"

connect-chart-push: connect-chart-package
	helm push .build/connect-cluster-$(CONNECT_CHART_VERSION).tgz $(CHART_REGISTRY)
	@echo "✅ Connect chart pushed: $(CHART_REGISTRY)/connect-cluster:$(CONNECT_CHART_VERSION)"

connect-chart-test:
	@echo ""
	@echo "╭────────────────────────────────────────────────────────────────╮"
	@echo "│  🧪 Helm Test · Kafka Connect                                 │"
	@echo "╰────────────────────────────────────────────────────────────────╯"
	@echo ""
	@CONNECT_RELEASE=$${CONNECT_RELEASE:-connect-cluster}; \
	CONNECT_NAMESPACE=$${CONNECT_NAMESPACE:-kafka}; \
	TIMEOUT=$${TIMEOUT:-180s}; \
	echo "  Release:    $$CONNECT_RELEASE"; \
	echo "  Namespace:  $$CONNECT_NAMESPACE"; \
	echo "  Timeout:    $$TIMEOUT"; \
	echo ""; \
	helm test $$CONNECT_RELEASE --namespace $$CONNECT_NAMESPACE --timeout $$TIMEOUT --logs 2>&1; \
	EXIT=$$?; \
	echo ""; \
	if [ $$EXIT -eq 0 ]; then \
		echo "  ✅ Connect tests passed"; \
	else \
		echo "  ❌ Connect tests failed (exit $$EXIT)"; \
	fi; \
	exit $$EXIT

connect-chart-all: connect-chart-lint connect-chart-template connect-chart-package
	@echo "✅ All connect chart checks passed: .build/connect-cluster-$(CONNECT_CHART_VERSION).tgz"

connect-deploy:
	@echo "🔌 Deploying Kafka Connect cluster (ENV=$(ENV))..."
	@OVERLAY=""; \
	if [ -f "$(CONNECT_CHART_DIR)/values-$(ENV).yaml" ]; then \
		OVERLAY="-f $(CONNECT_CHART_DIR)/values-$(ENV).yaml"; \
	fi; \
	helm upgrade --install connect-cluster $(CONNECT_CHART_DIR) \
		--namespace kafka --create-namespace \
		$$OVERLAY \
		--timeout 10m --wait
	@echo "✅ Kafka Connect deployed"

connect-undeploy:
	@echo "🗑️  Removing Kafka Connect cluster..."
	helm uninstall connect-cluster -n kafka 2>/dev/null || true
	@echo "✅ Kafka Connect removed"

kafka-deploy: kafka-chart-deps
	@echo "📦 Deploying Kafka cluster (ENV=$(ENV))..."
	ENV=$(ENV) ./scripts/deploy-kafka-generic.sh --yes

kafka-upgrade: kafka-chart-deps
	@echo "🔄 Upgrading Kafka cluster (ENV=$(ENV))..."
	ENV=$(ENV) ./scripts/deploy-kafka-generic.sh --yes

kafka-detect:
	@./scripts/kafka-cluster-report.sh

kafka-verify-policies:
	@./scripts/verify-kafka-policies.sh

kafka-deploy-auto:
	@echo "🤖 Starting Kates Auto-Deploy..."
	cd cli && go run . auto --chart-dir ../$(KAFKA_CHART_DIR)
	@echo ""
	@echo "✅ Kafka cluster deployed with auto-detected zones and storage!"
	@echo "  Run tests:     helm test kafka-cluster -n kafka"
	@echo "  Check status:  kubectl get kafka,kafkanodepools -n kafka"

VALUES_FILE ?=
kafka-deploy-generic: kafka-chart-deps
	@./scripts/deploy-kafka-generic.sh --yes

kafka-deploy-generic-interactive: kafka-chart-deps
	@./scripts/deploy-kafka-generic.sh

kafka-deploy-generic-custom: kafka-chart-deps
	@if [ -z "$(VALUES_FILE)" ]; then \
		echo "❌ VALUES_FILE is required. Usage: make kafka-deploy-generic-custom VALUES_FILE=my-values.yaml"; \
		exit 1; \
	fi
	@./scripts/deploy-kafka-generic.sh --yes -f $(VALUES_FILE)

kafka-undeploy:
	@echo "🗑️  Removing Kafka cluster..."
	helm uninstall kafka-cluster -n kafka 2>/dev/null || true
	@echo "Cleaning up PVCs..."
	kubectl delete pvc -l strimzi.io/cluster=krafter -n kafka --ignore-not-found
	@echo "✅ Kafka cluster removed"

# Port Forwarding
ports:
	@echo "🔌 Starting Port Forwarding..."
	./scripts/port-forward.sh

# Download all Helm charts
download-charts:
	@echo "📦 Downloading all Helm charts..."
	./scripts/download-charts.sh

# Kates Chaos Management (LitmusChaos via kates-chaos chart)
litmus:
	@echo "⚡ Deploying Kates Chaos (LitmusChaos)..."
	helm dependency update charts/kates-chaos
	helm upgrade --install chaos charts/kates-chaos \
		-n kafka --create-namespace \
		-f charts/kates-chaos/values-kind.yaml \
		--timeout 10m --wait
	@echo "✅ Kates Chaos deployed"

litmus-undeploy:
	@echo "🧹 Removing Kates Chaos (LitmusChaos)..."
	@helm uninstall chaos -n kafka 2>/dev/null || true
	@kubectl delete pvc --all -n kafka 2>/dev/null || true
	@kubectl delete all --all -n kafka 2>/dev/null || true
	@kubectl # delete namespace litmus 2>/dev/null || true
	@echo "✅ Kates Chaos removed"

chaos-ui:
	@echo "🌐 Port-forwarding Litmus UI..."
	@echo "Access at: http://localhost:9091 (admin/litmus)"
	kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n kafka

chaos-status:
	@echo "📊 Chaos Status:"
	@echo ""
	@echo "Helm Release:"
	@helm list -n kafka 2>/dev/null || echo "No release found"
	@echo ""
	@echo "Pods:"
	@kubectl get pods -n kafka 2>/dev/null || echo "No pods found"
	@echo ""
	@echo "ChaosExperiments (kafka):"
	@kubectl get chaosexperiments -n kafka 2>/dev/null || echo "No experiments found"
	@echo ""
	@echo "ChaosEngines (kafka):"
	@kubectl get chaosengines -n kafka 2>/dev/null || echo "No engines found"
	@echo ""
	@echo "ChaosResults (kafka):"
	@kubectl get chaosresults -n kafka 2>/dev/null || echo "No results found"

litmus-generic:
	@echo "⚡ Deploying Kates Chaos (generic Kubernetes)..."
	helm dependency update charts/kates-chaos
	helm upgrade --install chaos charts/kates-chaos \
		-n kafka --create-namespace \
		-f charts/kates-chaos/values-generic.yaml \
		--timeout 10m --wait
	@echo "✅ Kates Chaos deployed (generic)"

litmus-test:
	@echo "🧪 Running Helm tests..."
	helm test chaos -n kafka

litmus-gameday:
	@echo "🎮 Triggering GameDay validation..."
	helm upgrade chaos charts/kates-chaos \
		-n kafka \
		-f charts/kates-chaos/values-kind.yaml \
		--set gameday.enabled=true \
		--timeout 5m --wait

# Velero backup
velero:
	@echo "💾 Deploying Velero backup..."
	./scripts/deploy-velero.sh

gameday:
	@echo "🎮 Running Automated GameDay Validation..."
	./scripts/gameday.sh

# Status check
status:
	@echo "📊 Cluster Status:"
	@echo ""
	@echo "=== Pods by Namespace ==="
	@kubectl get pods -A --no-headers | awk '{print $$1}' | sort | uniq -c | sort -rn
	@echo ""
	@echo "=== Not Ready Pods ==="
	@kubectl get pods -A | grep -v Running | grep -v Completed || echo "All pods are running!"

# Destroy Cluster (FORCE=1 skips confirmation prompt)
destroy:
	FORCE=$(FORCE) ./scripts/destroy.sh

# Alias for destroy
clean: destroy

kyverno-permissive:
	@echo "🔓 Making Kyverno completely permissive (ignoring all resources)..."
	@kubectl patch configmap kyverno -n kyverno --type merge -p '{"data":{"resourceFilters":"[*,*,*]"}}' 2>/dev/null || echo "⚠️  Could not patch Kyverno ConfigMap (is it installed?)"
	@echo "🔄 Restarting Kyverno pods to apply changes..."
	@kubectl rollout restart deployment -n kyverno -l app.kubernetes.io/name=kyverno 2>/dev/null || true
	@echo "✅ Kyverno is now in permissive mode."

kyverno-audit:
	@echo "👁️  Setting all Kyverno policies to Audit mode..."
	@kubectl get clusterpolicy -o name 2>/dev/null | xargs -I {} kubectl patch {} --type='json' -p='[{"op": "replace", "path": "/spec/validationFailureAction", "value": "Audit"}]' 2>/dev/null || true
	@kubectl get policy -A -o name 2>/dev/null | xargs -I {} kubectl patch {} --type='json' -p='[{"op": "replace", "path": "/spec/validationFailureAction", "value": "Audit"}]' 2>/dev/null || true
	@echo "✅ All policies set to Audit mode."

# Help
help:
	@echo "Available targets:"
	@echo ""
	@echo "  Cluster & Infrastructure"
	@echo "  all                                - Complete setup (cluster, all services)"
	@echo "  cluster                            - Start Kind cluster only"
	@echo "  monitoring                         - Deploy Prometheus & Grafana"
	@echo "  cert-manager                       - Deploy cert-manager"
	@echo "  kafka                              - Deploy Kafka (shorthand for kafka-deploy)"
	@echo "  kafka-deploy                       - Deploy Kafka via Helm (ENV=kind|dev|staging|prod)"
	@echo "  kafka-detect                       - Deep cluster compatibility report for Kafka"
	@echo "  kafka-verify-policies              - Verify Kyverno/network policy compliance for generic cluster"
	@echo "  kafka-deploy-auto                  - Auto-detect cluster config and deploy Kafka"
	@echo "  kafka-deploy-generic               - Full pipeline: detect → deploy → wait → verify"
	@echo "  kafka-deploy-generic-interactive   - Same but prompts before deploy"
	@echo "  kafka-deploy-generic-custom        - Generic + extra overlay (VALUES_FILE=...)"
	@echo "  kafka-upgrade                      - Upgrade existing Kafka release (ENV=...)"
	@echo "  kafka-undeploy                     - Remove Kafka Helm release + PVCs"
	@echo "  ui                                 - Deploy Kafka UI (raw manifests)"
	@echo "  ui-deploy                          - Deploy Kafka UI via Helm (ENV=kind|dev|staging|prod)"
	@echo "  ui-upgrade                         - Upgrade Kafka UI Helm release (ENV=...)"
	@echo "  ui-undeploy                        - Remove Kafka UI Helm release"
	@echo "  ui-chart-lint                      - Lint the kafka-ui chart"
	@echo "  ui-chart-template                  - Render kafka-ui templates (ENV=...)"
	@echo "  apicurio                           - Deploy Apicurio Registry"
	@echo "  jaeger                             - Deploy Jaeger (distributed tracing)"
	@echo "  kyverno                            - Deploy Kyverno policy engine"
	@echo "  kyverno-undeploy                   - Remove Kyverno"
	@echo "  litmus                             - Deploy Kates Chaos (Kind overlay)"
	@echo "  litmus-generic                     - Deploy Kates Chaos (generic K8s overlay)"
	@echo "  litmus-undeploy                    - Remove Kates Chaos stack completely"
	@echo "  litmus-test                        - Run Helm tests for chaos stack"
	@echo "  litmus-gameday                     - Trigger GameDay validation run"
	@echo "  velero                             - Deploy Velero backup"
	@echo ""
	@echo "  Kafka Connect Chart"
	@echo "  connect-chart-lint                 - Lint the connect-cluster chart"
	@echo "  connect-chart-template             - Render connect-cluster templates"
	@echo "  connect-chart-package              - Package the connect-cluster chart"
	@echo "  connect-chart-push                 - Push connect-cluster to OCI registry"
	@echo "  connect-chart-test                 - Run Helm tests for connect-cluster"
	@echo "  connect-chart-all                  - lint + template + package"
	@echo "  connect-deploy                     - Deploy Kafka Connect via Helm (ENV=kind|dev|staging|prod)"
	@echo "  connect-undeploy                   - Remove Kafka Connect Helm release"
	@echo ""
	@echo "  Helm Test Suite"
	@echo "  kafka-chart-test                   - Run Helm tests for Kafka cluster (KAFKA_RELEASE=... KAFKA_NAMESPACE=...)"
	@echo "  kates-helm-test                    - Run Helm tests for Kates API (KATES_RELEASE=... KATES_NAMESPACE=...)"
	@echo "  chaos-helm-test                    - Run Helm tests for Chaos stack (CHAOS_RELEASE=... CHAOS_NAMESPACE=...)"
	@echo "  helm-test-all                      - Run all Helm tests across all components with summary"
	@echo "                                       TIMEOUT=180s SKIP_CHAOS=0 SKIP_KATES=0"
	@echo ""
	@echo "  Kates CLI"
	@echo "  cli-build                          - Cross-compile CLI (macOS + Linux)"
	@echo "  cli-install                        - Build and install CLI on this machine"
	@echo "  cli-clean                          - Remove CLI build artifacts"
	@echo ""
	@echo "  Kates Application (Docker + Kind)"
	@echo "  kates                              - Build + deploy Kates (full pipeline)"
	@echo "  kates-build                        - Build Kates JVM image and load into Kind"
	@echo "  kates-native                       - Build Kates native image and load into Kind"
	@echo "  tester-build                       - Build Kates Tester image and load into Kind"
	@echo "  push-images                        - Push kates and tester images to remote registry"
	@echo "  kates-deploy                       - Apply Kates K8s manifests"
	@echo "  kates-redeploy                     - Restart Kates deployment"
	@echo "  kates-logs                         - Stream Kates logs"
	@echo "  kates-undeploy                     - Remove Kates namespace"
	@echo ""
	@echo "  Kates Application (Helm chart)"
	@echo "  kates-helm                         - Deploy via Helm (shorthand)"
	@echo "  kates-helm-deploy                  - Deploy via Helm (ENV=kind|dev|staging|prod)"
	@echo "  kates-helm-upgrade                 - Upgrade existing release (ENV=...)"
	@echo "  kates-helm-undeploy                - Remove Kates Helm release"
	@echo "  chart-lint                         - Lint the Helm chart"
	@echo "  chart-package                      - Package the Helm chart"
	@echo "  chart-push                         - Push the chart to OCI registry"
	@echo ""
	@echo "  Performance Tests"
	@echo "  test                               - Run baseline 1M-message perf test"
	@echo "  test-load                          - Run load test (concurrent producers)"
	@echo "  test-stress                        - Run stress test (ramp to breaking point)"
	@echo "  test-spike                         - Run spike test (flash sale simulation)"
	@echo "  test-endurance                     - Run endurance/soak test (sustained load)"
	@echo "  test-volume                        - Run volume test (large data)"
	@echo "  test-capacity                      - Run capacity test (find max throughput)"
	@echo "  test-net                           - Run cross-namespace network connectivity tests"
	@echo "  test-net-kafka                     - Test TCP connectivity to Kafka from default namespace"
	@echo "  test-net-api                       - Test HTTP connectivity to Kates API from default namespace"
	@echo ""
	@echo "  Operations"
	@echo "  ports                              - Start port forwarding"
	@echo "  logs                               - Stream logs from all services"
	@echo "  status                             - Check cluster status"
	@echo "  chaos-ui                           - Port-forward Litmus UI"
	@echo "  chaos-status                       - Show chaos infrastructure status"
	@echo "  gameday                            - Run automated GameDay validation"
	@echo "  kyverno-permissive                 - Make Kyverno completely permissive (ignore all)"
	@echo "  kyverno-audit                      - Set all Kyverno policies to Audit mode"
	@echo "  destroy                            - Destroy cluster (FORCE=1 to skip prompt)"
	@echo "  help                               - Show this help"

logs:
	@echo "📋 Streaming logs from all services (Ctrl+C to stop)..."
	@echo ""
	@kubectl logs -f -l app=kates -n kafka --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l strimzi.io/cluster=krafter -n kafka --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app.kubernetes.io/name=grafana -n kafka --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app=kafka-ui -n kafka --prefix --tail=20 2>/dev/null &
	@wait

# ─── Book Generation ─────────────────────────────────────────────────────────

book-html: ## Generate HTML book site
	@echo "📖 Building HTML book..."
	@cd docs/book && quarto render --to html
	@echo "✅ HTML book generated at docs/book/_book/"
	@echo "   Open: open docs/book/_book/index.html"

book-pdf: ## Generate PDF book
	@echo "📖 Building PDF book..."
	@cd docs/book && quarto render --to pdf
	@echo "✅ PDF book generated at docs/book/_book/"

book-clean: ## Clean book build artifacts
	@echo "🧹 Cleaning book build artifacts..."
	@rm -rf docs/book/_book docs/book/_freeze docs/book/.quarto
	@echo "✅ Clean"
