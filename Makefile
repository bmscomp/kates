.PHONY: all detect cluster monitoring deploy-all kafka kafka-deploy kafka-upgrade kafka-undeploy kafka-detect kafka-verify-policies kafka-deploy-auto kafka-deploy-generic ui test test-load test-stress test-spike test-endurance test-volume test-capacity destroy clean download-charts litmus litmus-generic litmus-undeploy litmus-test litmus-gameday kates kates-generic kates-prod kates-build kates-native kates-deploy kates-logs kates-undeploy kates-helm kates-helm-deploy kates-helm-upgrade kates-helm-undeploy kates-secret cli-build cli-install cli-clean logs chaos-ui chaos-status chart-lint chart-package chart-push gameday jaeger

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
	@echo "🚀 Launching complete cluster setup..."
	@echo ""
	@# ── Step 1: Cluster connectivity (auto-create Kind if none found) ──
	@if kubectl cluster-info >/dev/null 2>&1; then \
		CONTEXT=$$(kubectl config current-context); \
		echo "✅ Kubernetes cluster reachable (context: $$CONTEXT)"; \
	else \
		echo "⚠️  No Kubernetes cluster reachable — creating Kind cluster..."; \
		$(MAKE) cluster; \
	fi
	@echo ""
	@# ── Step 2: Detect cluster configuration ──
	@echo "Step 1: Detecting cluster configuration..."
	@mkdir -p .build
	@if [ ! -x "$(KATES_BIN)" ]; then \
		echo "  ⚠️  kates binary not found, building it now..."; \
		$(MAKE) cli-build >/dev/null; \
	fi
	@$(KATES_BIN) detect --generate-values --values-output $(DETECTED_VALUES) --quiet
	@PROVIDER=$$(grep '^# Provider:' $(DETECTED_VALUES) | awk '{print $$3}'); \
	echo "  Provider: $${PROVIDER:-unknown}"; \
	echo "  Values:   $(DETECTED_VALUES)"
	@echo ""
	@# ── Step 2: Strimzi Operator (read from detect output) ──
	@if grep -A1 'strimziOperator:' $(DETECTED_VALUES) | grep -q 'enabled: true'; then \
		echo "Step 2: Installing Strimzi Operator (cluster-wide)..."; \
		kubectl create namespace strimzi-operator --dry-run=client -o yaml | kubectl apply -f - > /dev/null 2>&1; \
		helm upgrade --install strimzi-operator oci://quay.io/strimzi-helm/strimzi-kafka-operator \
			--version $(STRIMZI_VERSION) \
			--namespace strimzi-operator \
			--set watchAnyNamespace=true \
			--set replicas=1 \
			--timeout 5m --wait; \
		echo "  Waiting for Strimzi CRDs to be established..."; \
		kubectl wait --for=condition=Established crd kafkas.kafka.strimzi.io --timeout=60s; \
	else \
		echo "✅ Strimzi Operator already running — skipping"; \
	fi
	@echo ""
	@# ── Ensure kafka namespace exists ──
	@kubectl create namespace kafka --dry-run=client -o yaml | kubectl apply -f - > /dev/null 2>&1
	@# ── Step 3: cert-manager ──
	@echo "Step 3: Deploying cert-manager..."
	@./scripts/deploy-cert-manager.sh
	@echo ""
	@# ── Step 4: Kafka cluster ──
	@if kubectl get pods -n kafka -l strimzi.io/cluster=krafter --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kafka already deployed — skipping"; \
	else \
		echo "Step 4: Deploying Kafka (Strimzi)..."; \
		./scripts/deploy-kafka-generic.sh --yes; \
	fi
	@# ── Step 5: Wait for Kafka CR Ready (handles KRaft voter-format upgrade) ──
	@echo "Step 5: Waiting for Kafka cluster to be ready..."
	@echo "  (First deploy may take up to 10 min for KRaft voter-format upgrade)"
	@kubectl wait kafka/krafter --for=condition=Ready --timeout=600s -n kafka || \
		{ echo "❌ Kafka cluster did not reach Ready state:"; \
		  kubectl get pods -n kafka -l strimzi.io/cluster=krafter; \
		  kubectl get kafka -n kafka -o wide; exit 1; }
	@echo ""
	@# ── Step 6: Wait for Entity Operator (HARD GATE) ──
	@echo "Step 6: Waiting for Entity Operator..."
	@kubectl wait deployment -l app.kubernetes.io/name=entity-operator -n kafka \
		--for=condition=Available --timeout=180s || \
		{ echo "❌ Entity Operator did not become ready within 180s. Cannot create users/topics."; exit 1; }
	@echo "✅ Entity Operator is ready"
	@echo ""
	@# ── Step 7: Apply users + topics ──
	@echo "Step 7: Applying Kafka users and topics..."
	@kubectl apply -f config/kafka/kafka-users.yaml
	@kubectl apply -f config/kafka/kafka-topics.yaml
	@kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n kafka 2>/dev/null || \
		{ echo "⚠️  Some KafkaUsers did not reach Ready:"; \
		  kubectl get kafkauser -n kafka -o wide; }
	@echo ""
	@# ── Step 7.5: Network Policies (if CNI supports them) ──
	@if grep -A1 'networkPolicies:' $(DETECTED_VALUES) | grep -q 'enabled: true'; then \
		echo "Step 7.5: Applying Kafka network policies..."; \
		kubectl apply -f config/kafka/kafka-networkpolicies.yaml; \
		echo "✅ NetworkPolicies applied"; \
	else \
		echo "⏭️  NetworkPolicies skipped (CNI does not support them)"; \
	fi
	@echo ""
	@# ── Step 8: Apicurio Registry ──
	@if kubectl get deployment apicurio-registry -n kafka --no-headers 2>/dev/null | grep -q '1/1'; then \
		echo "✅ Apicurio Registry already deployed — skipping"; \
	else \
		echo "Step 8: Deploying Apicurio Registry..."; \
		./scripts/deploy-apicurio.sh; \
	fi
	@echo ""
	@# ── Step 9: LitmusChaos (provider-aware values) ──
	@if kubectl get pods -n kafka -l app.kubernetes.io/instance=chaos --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ LitmusChaos already deployed — skipping"; \
	else \
		echo "Step 9: Deploying LitmusChaos..."; \
		PROVIDER=$$(grep '^# Provider:' $(DETECTED_VALUES) | awk '{print $$3}'); \
		CHAOS_VALUES="charts/kates-chaos/values.yaml"; \
		if [ "$$PROVIDER" = "kind" ] && [ -f "charts/kates-chaos/values-kind.yaml" ]; then \
			CHAOS_VALUES="charts/kates-chaos/values-kind.yaml"; \
		fi; \
		helm dependency update charts/kates-chaos; \
		helm upgrade --install chaos charts/kates-chaos \
			-n kafka --create-namespace \
			-f "$$CHAOS_VALUES" \
			--timeout 10m; \
		echo "Waiting for Litmus pods to be ready..."; \
		kubectl wait --for=condition=Ready pods -l app.kubernetes.io/instance=chaos -n kafka --timeout=300s 2>/dev/null || true; \
	fi
	@echo ""
	@echo "Step 9.5: Verifying chaos infrastructure..."
	@if kubectl get crd chaosengines.litmuschaos.io &>/dev/null; then \
		echo "✅ Litmus CRDs installed"; \
	else \
		echo "⚠️  Litmus CRDs not found — chaos provider will fall back to noop"; \
	fi
	@echo ""
	@# ── Step 10: Jaeger ──
	@if kubectl get pods -n kafka -l app=jaeger --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Jaeger already deployed — skipping"; \
	else \
		echo "Step 10: Deploying Jaeger (distributed tracing)..."; \
		./scripts/deploy-jaeger.sh || true; \
	fi
	@echo ""
	@# ── Step 11: Kates ──
	@if kubectl get pods -n kafka -l app=kates --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kates already deployed — skipping"; \
	else \
		echo "Step 11: Deploying Kates..."; \
		./scripts/deploy-kates.sh; \
	fi
	@echo ""
	@echo "Step 12: Exposing service ports..."
	@./scripts/port-forward.sh
	@echo ""
	@# ── Step 13: Post-deploy health check ──
	@echo "Step 13: Verifying deployment health..."
	@UNHEALTHY=0; \
	for dep in kates apicurio-registry jaeger; do \
		STATUS=$$(kubectl get deployment "$$dep" -n kafka -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null | sed 's|^/|0/|'); \
		if [ -z "$$STATUS" ] || echo "$$STATUS" | grep -qv '^[0-9]*/[0-9]*$$'; then \
			echo "  ⏭️  $$dep — not deployed"; \
		elif [ "$$(echo $$STATUS | cut -d/ -f1)" != "$$(echo $$STATUS | cut -d/ -f2)" ]; then \
			echo "  ⚠️  $$dep — $$STATUS replicas ready"; \
			UNHEALTHY=$$((UNHEALTHY + 1)); \
		else \
			echo "  ✅ $$dep — $$STATUS"; \
		fi; \
	done; \
	if [ "$$UNHEALTHY" -gt 0 ]; then \
		echo ""; \
		echo "⚠️  $$UNHEALTHY deployment(s) not fully ready — check 'kubectl get pods -n kafka'"; \
	else \
		echo ""; \
		echo "✅ All deployments healthy"; \
	fi
	@echo ""
	@echo "✅ Complete setup finished in $$(( $$(date +%s) - $(TIMER) ))s"
	@echo ""
	@echo "📊 Services deployed:"
	@echo "  ✓ Kafka Cluster (Strimzi KRaft mode)"
	@echo "  ✓ Apicurio Registry"
	@echo "  ✓ LitmusChaos + Chaos Experiments"
	@echo "  ✓ Jaeger (distributed tracing)"
	@echo "  ✓ Kates"
	@echo ""
	@echo "🔗 Access points:"
	@echo "  - Apicurio Registry: http://localhost:30082"
	@echo "  - Kates:             http://localhost:30083"
	@echo "  - Jaeger UI:         http://localhost:30086"
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

# Deploy Kafka UI only
ui:
	@echo "🖥️ Deploying Kafka UI..."
	./scripts/deploy-kafka-ui.sh

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
	elif docker pull ghcr.io/bmscomp/kates:1.11.0; then \
		echo "✅ Pulled Kates image from registry."; \
		docker tag ghcr.io/bmscomp/kates:1.11.0 kates:latest; \
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
	elif docker pull ghcr.io/bmscomp/kates:1.11.0-native; then \
		echo "✅ Pulled Kates native image from registry."; \
		docker tag ghcr.io/bmscomp/kates:1.11.0-native kates:native; \
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
	helm test kafka-cluster --namespace kafka --timeout 120s

kafka-chart-all: kafka-chart-deps kafka-chart-lint kafka-chart-template kafka-chart-package
	@echo "✅ All kafka chart checks passed: .build/kafka-cluster-$(KAFKA_CHART_VERSION).tgz"

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
	@echo "  ui                                 - Deploy Kafka UI"
	@echo "  apicurio                           - Deploy Apicurio Registry"
	@echo "  jaeger                             - Deploy Jaeger (distributed tracing)"
	@echo "  litmus                             - Deploy Kates Chaos (Kind overlay)"
	@echo "  litmus-generic                     - Deploy Kates Chaos (generic K8s overlay)"
	@echo "  litmus-undeploy                    - Remove Kates Chaos stack completely"
	@echo "  litmus-test                        - Run Helm tests for chaos stack"
	@echo "  litmus-gameday                     - Trigger GameDay validation run"
	@echo "  velero                             - Deploy Velero backup"
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
