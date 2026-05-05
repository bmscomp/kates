.PHONY: all cluster monitoring deploy-all kafka kafka-deploy kafka-upgrade kafka-undeploy kafka-detect kafka-deploy-auto kafka-deploy-generic ui test test-load test-stress test-spike test-endurance test-volume test-capacity destroy clean download-charts litmus litmus-generic litmus-undeploy litmus-test litmus-gameday kates kates-build kates-native kates-deploy kates-logs kates-undeploy kates-helm kates-helm-deploy kates-helm-upgrade kates-helm-undeploy cli-build cli-install cli-clean logs chaos-ui chaos-status chart-lint chart-package chart-push gameday jaeger

.DEFAULT_GOAL := help

TIMER := $(shell date +%s)

all: check-prerequisites
	@echo "🚀 Launching complete cluster setup..."
	@echo ""
	@if kind get clusters 2>/dev/null | grep -q '^panda$$' && kubectl cluster-info --context kind-panda >/dev/null 2>&1; then \
		echo "✅ Kind cluster 'panda' already running — skipping creation"; \
	else \
		echo "Step 1: Starting Kind cluster + registry..."; \
		./scripts/start-cluster.sh; \
	fi
	@echo ""
	@if kubectl get pods -n monitoring -l "app.kubernetes.io/name=grafana" --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Monitoring already deployed — skipping"; \
	else \
		echo "Step 2: Deploying Monitoring (Prometheus & Grafana)..."; \
		./scripts/deploy-monitoring.sh; \
		echo "Step 3: Waiting for monitoring to be ready..."; \
		kubectl wait --for=condition=Ready pods -l "app.kubernetes.io/name=grafana" -n monitoring --timeout=120s || true; \
	fi
	@echo ""
	@if kubectl get deployment cert-manager -n cert-manager --no-headers 2>/dev/null | grep -q '1/1'; then \
		echo "✅ cert-manager already deployed — skipping"; \
	else \
		echo "Step 3: Deploying cert-manager..."; \
		./scripts/deploy-cert-manager.sh; \
	fi
	@echo ""
	@if kubectl get pods -n kafka -l strimzi.io/cluster=krafter --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kafka already deployed — skipping"; \
	else \
		echo "Step 4: Deploying Kafka (Strimzi)..."; \
		./scripts/deploy-kafka.sh; \
		echo "Step 5: Waiting for Kafka to be ready..."; \
		kubectl wait --for=condition=Ready pods -l strimzi.io/cluster=krafter -n kafka --timeout=300s || true; \
	fi
	@echo "Ensuring Kafka users and topics are applied..."
	@kubectl apply -f config/kafka/kafka-users.yaml
	@kubectl apply -f config/kafka/kafka-topics.yaml
	@kubectl wait kafkauser --all --for=condition=Ready --timeout=60s -n kafka 2>/dev/null || true
	@echo ""
	@if kubectl get pods -n kafka -l app=kafka-ui --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kafka UI already deployed — skipping"; \
	else \
		echo "Step 6: Deploying Kafka UI..."; \
		./scripts/deploy-kafka-ui.sh; \
	fi
	@echo ""
	@if kubectl get deployment apicurio-registry -n apicurio --no-headers 2>/dev/null | grep -q '1/1'; then \
		echo "✅ Apicurio Registry already deployed — skipping"; \
	else \
		echo "Step 7: Deploying Apicurio Registry..."; \
		./scripts/deploy-apicurio.sh; \
	fi
	@echo ""
	@if kubectl get pods -n litmus -l app.kubernetes.io/instance=chaos --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ LitmusChaos already deployed — skipping"; \
	else \
		echo "Step 8: Deploying LitmusChaos..."; \
		kubectl apply -f config/litmus/chaos-litmus-chaos-enable.yml 2>/dev/null || true; \
		kubectl apply -f config/litmus/kafka-litmus-chaos-enable.yml 2>/dev/null || true; \
		helm dependency build charts/kates-chaos 2>/dev/null || true; \
		helm upgrade --install chaos charts/kates-chaos \
			-n litmus --create-namespace \
			-f charts/kates-chaos/values-kind.yaml \
			--timeout 10m; \
		echo "Waiting for Litmus pods to be ready..."; \
		kubectl wait --for=condition=Ready pods -l app.kubernetes.io/instance=chaos -n litmus --timeout=300s 2>/dev/null || true; \
	fi
	@echo ""
	@echo "Step 9: Verifying chaos infrastructure..."
	@if kubectl get crd chaosengines.litmuschaos.io &>/dev/null; then \
		echo "✅ Litmus CRDs installed"; \
	else \
		echo "⚠️  Litmus CRDs not found — chaos provider will fall back to noop"; \
	fi
	@echo ""
	@if kubectl get pods -n monitoring -l app=jaeger --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Jaeger already deployed — skipping"; \
	else \
		echo "Step 9.5: Deploying Jaeger (distributed tracing)..."; \
		./scripts/deploy-jaeger.sh || true; \
	fi
	@echo ""
	@if kubectl get pods -n kates -l app=kates --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kates already deployed — skipping build"; \
	else \
		echo "Step 10: Building and deploying Kates..."; \
		docker build -f kates/Dockerfile -t kates:latest .; \
		kind load docker-image kates:latest --name panda; \
		kubectl apply -f kates/k8s/namespace.yaml; \
		kubectl apply -f kates/k8s/rbac.yaml; \
		kubectl apply -f kates/k8s/configmap.yaml; \
		echo "Copying Kafka SASL credentials to kates namespace..."; \
		kubectl get secret kates-backend -n kafka -o json \
			| jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
			| kubectl apply -n kates -f -; \
		kubectl apply -f kates/k8s/postgres.yaml; \
		echo "Waiting for PostgreSQL to be ready..."; \
		kubectl wait --for=condition=Ready pod -l app=postgres -n kates --timeout=120s; \
		kubectl apply -f kates/k8s/deployment.yaml; \
		kubectl apply -f kates/k8s/service.yaml; \
		kubectl rollout status deployment/kates -n kates --timeout=300s; \
	fi
	@echo ""
	@echo "Step 11: Exposing service ports..."
	./scripts/port-forward.sh
	@echo ""
	@echo "✅ Complete setup finished in $$(( $$(date +%s) - $(TIMER) ))s"
	@echo ""
	@echo "📊 Services deployed:"
	@echo "  ✓ Prometheus & Grafana (Monitoring)"
	@echo "  ✓ Kafka Cluster (Strimzi KRaft mode)"
	@echo "  ✓ Kafka UI"
	@echo "  ✓ Apicurio Registry"
	@echo "  ✓ LitmusChaos + Chaos Experiments"
	@echo "  ✓ Kates"
	@echo ""
	@echo "🔗 Access points:"
	@echo "  - Grafana:          http://localhost:30080 (admin/admin)"
	@echo "  - Kafka UI:         http://localhost:30081"
	@echo "  - Apicurio Registry:http://localhost:30082"
	@echo "  - Kates:            http://localhost:30083"
	@echo "  - Prometheus:       http://localhost:30090"
	@echo "  - Jaeger UI:        http://localhost:30086"
	@echo "  - Litmus UI:        http://localhost:9091  (admin/litmus)"
	@echo ""

# Check prerequisites
check-prerequisites:
	@echo "🔍 Checking prerequisites..."
	@bash -c 'source scripts/common.sh && require_cmd docker && require_cmd kind && require_cmd kubectl && require_cmd helm'
	@echo "✅ All prerequisites met"

# Start Kind cluster only
cluster:
	@echo "🎯 Starting Kind cluster..."
	./scripts/start-cluster.sh

# Deploy monitoring stack only
monitoring:
	@echo "📊 Deploying monitoring stack (Kind)..."
	helm dependency build charts/monitoring
	helm upgrade --install monitoring charts/monitoring \
		--namespace monitoring --create-namespace \
		-f charts/monitoring/values-kind.yaml \
		--timeout 10m --wait

monitoring-generic:
	@echo "📊 Deploying monitoring stack (Generic)..."
	helm dependency build charts/monitoring
	helm upgrade --install monitoring charts/monitoring \
		--namespace monitoring --create-namespace \
		-f charts/monitoring/values-generic.yaml \
		--timeout 10m --wait

monitoring-undeploy:
	@echo "🗑️ Undeploying monitoring stack..."
	helm uninstall monitoring -n monitoring || true
	kubectl delete pvc --all -n monitoring || true
	kubectl delete namespace monitoring || true

cert-manager:
	@echo "🔐 Deploying cert-manager..."
	./scripts/deploy-cert-manager.sh

# Deploy full stack (monitoring, Kafka, UI, Litmus) — without cluster/images
deploy-all:
	@echo "🚀 Deploying full stack..."
	$(MAKE) monitoring
	./scripts/deploy-kafka.sh
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
	@echo "🔨 Building Kates (JVM + CLI)..."
	cd kates && ./mvnw package -DskipTests -B
	docker build -f kates/Dockerfile -t kates:latest .
	kind load docker-image kates:latest --name panda
	@echo "✅ Kates image loaded into Kind"

kates-native:
	@echo "🔨 Building Kates (native)..."
	docker build -f kates/Dockerfile.native -t kates:latest .
	kind load docker-image kates:latest --name panda
	@echo "✅ Kates native image loaded into Kind"

kates-deploy:
	@echo "🚀 Deploying Kates to Kubernetes..."
	kubectl apply -f kates/k8s/namespace.yaml
	kubectl apply -f kates/k8s/rbac.yaml
	kubectl apply -f kates/k8s/configmap.yaml
	@echo "Copying Kafka SASL credentials to kates namespace..."
	@kubectl get secret kates-backend -n kafka -o json \
		| jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp,.metadata.annotations,.metadata.labels,.metadata.managedFields,.metadata.ownerReferences)' \
		| kubectl apply -n kates -f -
	kubectl apply -f kates/k8s/postgres.yaml
	@echo "Waiting for PostgreSQL to be ready..."
	@kubectl wait --for=condition=Ready pod -l app=postgres -n kates --timeout=120s
	kubectl apply -f kates/k8s/deployment.yaml
	kubectl apply -f kates/k8s/service.yaml
	kubectl rollout status deployment/kates -n kates --timeout=300s
	@echo "✅ Kates is running"

kates-redeploy:
	@echo "🔄 Redeploying Kates..."
	kubectl rollout restart deployment/kates -n kates
	kubectl rollout status deployment/kates -n kates --timeout=300s

kates-logs:
	@echo "📋 Streaming Kates logs..."
	kubectl logs -f -l app=kates -n kates

kates-undeploy:
	@echo "🗑️  Removing Kates..."
	kubectl delete namespace kates --ignore-not-found
	@echo "✅ Kates removed"

CLUSTER_NAME   ?= panda
KATES_NS       ?= kates
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
	ENV=$(ENV) ./scripts/deploy-kafka.sh

kafka-upgrade: kafka-chart-deps
	@echo "🔄 Upgrading Kafka cluster (ENV=$(ENV))..."
	ENV=$(ENV) ./scripts/deploy-kafka.sh

kafka-detect:
	@./scripts/kafka-cluster-report.sh

kafka-deploy-auto: kafka-chart-deps
	@echo "🔍 Auto-detecting cluster configuration from kubeconfig..."
	@mkdir -p .build
	@./scripts/detect-cluster-config.sh -o .build/values-detected.yaml
	@echo ""
	@echo "📦 Deploying Kafka cluster with detected configuration..."
	helm upgrade --install kafka-cluster $(KAFKA_CHART_DIR) \
		--namespace kafka --create-namespace \
		-f .build/values-detected.yaml \
		--timeout 10m --wait
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
	@echo "Applying Litmus CRDs..."
	@kubectl apply -f config/litmus/chaos-litmus-chaos-enable.yml 2>/dev/null || true
	@kubectl apply -f config/litmus/kafka-litmus-chaos-enable.yml 2>/dev/null || true
	@kubectl wait --for=condition=Established crd/chaosengines.litmuschaos.io --timeout=60s 2>/dev/null || true
	helm dependency build charts/kates-chaos 2>/dev/null || true
	helm upgrade --install chaos charts/kates-chaos \
		-n litmus --create-namespace \
		-f charts/kates-chaos/values-kind.yaml \
		--timeout 10m --wait
	@echo "✅ Kates Chaos deployed"

litmus-undeploy:
	@echo "🧹 Removing Kates Chaos (LitmusChaos)..."
	@helm uninstall chaos -n litmus 2>/dev/null || true
	@kubectl delete pvc --all -n litmus 2>/dev/null || true
	@kubectl delete all --all -n litmus 2>/dev/null || true
	@kubectl delete namespace litmus 2>/dev/null || true
	@echo "✅ Kates Chaos removed"

chaos-ui:
	@echo "🌐 Port-forwarding Litmus UI..."
	@echo "Access at: http://localhost:9091 (admin/litmus)"
	kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus

chaos-status:
	@echo "📊 Chaos Status:"
	@echo ""
	@echo "Helm Release:"
	@helm list -n litmus 2>/dev/null || echo "No release found"
	@echo ""
	@echo "Pods:"
	@kubectl get pods -n litmus 2>/dev/null || echo "No pods found"
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
	@echo "Applying Litmus CRDs..."
	@kubectl apply -f config/litmus/chaos-litmus-chaos-enable.yml 2>/dev/null || true
	@kubectl apply -f config/litmus/kafka-litmus-chaos-enable.yml 2>/dev/null || true
	@kubectl wait --for=condition=Established crd/chaosengines.litmuschaos.io --timeout=60s 2>/dev/null || true
	helm dependency build charts/kates-chaos 2>/dev/null || true
	helm upgrade --install chaos charts/kates-chaos \
		-n litmus --create-namespace \
		-f charts/kates-chaos/values-generic.yaml \
		--timeout 10m --wait
	@echo "✅ Kates Chaos deployed (generic)"

litmus-test:
	@echo "🧪 Running Helm tests..."
	helm test chaos -n litmus

litmus-gameday:
	@echo "🎮 Triggering GameDay validation..."
	helm upgrade chaos charts/kates-chaos \
		-n litmus \
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
	@echo "  destroy                            - Destroy cluster (FORCE=1 to skip prompt)"
	@echo "  help                               - Show this help"

logs:
	@echo "📋 Streaming logs from all services (Ctrl+C to stop)..."
	@echo ""
	@kubectl logs -f -l app=kates -n kates --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l strimzi.io/cluster=krafter -n kafka --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app.kubernetes.io/name=grafana -n monitoring --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app=kafka-ui -n kafka --prefix --tail=20 2>/dev/null &
	@wait
