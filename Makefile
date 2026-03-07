.PHONY: all cluster monitoring deploy-all kafka ui test test-load test-stress test-spike test-endurance test-volume test-capacity destroy clean download-charts litmus kates kates-build kates-native kates-deploy kates-logs kates-undeploy cli-build cli-install cli-clean logs chaos-kafka-memory-stress chaos-kafka-io-stress chaos-kafka-dns-error chaos-kafka-node-drain chart-lint chart-package chart-push gameday jaeger

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
	@if kubectl get pods -n kafka -l strimzi.io/cluster=krafter --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kafka already deployed — skipping"; \
	else \
		echo "Step 4: Deploying Kafka (Strimzi)..."; \
		./scripts/deploy-kafka.sh; \
		echo "Step 5: Waiting for Kafka to be ready..."; \
		kubectl wait --for=condition=Ready pods -l strimzi.io/cluster=krafter -n kafka --timeout=300s || true; \
	fi
	@echo ""
	@if kubectl get pods -n kafka -l app=kafka-ui --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kafka UI already deployed — skipping"; \
	else \
		echo "Step 6: Deploying Kafka UI..."; \
		./scripts/deploy-kafka-ui.sh; \
	fi
	@echo ""
	@if kubectl get pods -n kafka -l app=apicurio-registry --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Apicurio Registry already deployed — skipping"; \
	else \
		echo "Step 7: Deploying Apicurio Registry..."; \
		./scripts/deploy-apicurio.sh; \
	fi
	@echo ""
	@if kubectl get pods -n litmus -l app.kubernetes.io/component=litmus --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ LitmusChaos already deployed — skipping"; \
	else \
		echo "Step 8: Deploying LitmusChaos..."; \
		./scripts/deploy-litmuschaos.sh; \
	fi
	@echo ""
	@echo "Step 9: Enabling chaos environment..."
	kubectl apply -f config/litmus/kates-chaos-rbac.yaml
	kubectl apply -f config/litmus/kafka-rbac.yaml
	@echo ""
	@if kubectl get pods -n kates -l app=kates --no-headers 2>/dev/null | grep -q Running; then \
		echo "✅ Kates already deployed — skipping build"; \
	else \
		echo "Step 10: Building and deploying Kates (native)..."; \
		docker build -f kates/Dockerfile.native -t kates:latest .; \
		kind load docker-image kates:latest --name panda; \
		kubectl apply -f kates/k8s/namespace.yaml; \
		kubectl apply -f kates/k8s/rbac.yaml; \
		kubectl apply -f kates/k8s/configmap.yaml; \
		kubectl apply -f kates/k8s/postgres.yaml; \
		echo "Waiting for PostgreSQL to be ready..."; \
		kubectl wait --for=condition=Ready pod -l app=postgres -n kates --timeout=120s; \
		kubectl apply -f kates/k8s/deployment.yaml; \
		kubectl apply -f kates/k8s/service.yaml; \
		kubectl rollout status deployment/kates -n kates --timeout=120s; \
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
	@echo "📊 Deploying monitoring stack..."
	./scripts/deploy-monitoring.sh

# Deploy full stack (monitoring, Kafka, UI, Litmus) — without cluster/images
deploy-all:
	@echo "🚀 Deploying full stack..."
	./scripts/deploy-monitoring.sh
	./scripts/deploy-kafka.sh
	./scripts/deploy-kafka-ui.sh
	./scripts/deploy-apicurio.sh
	./scripts/deploy-jaeger.sh
	./scripts/deploy-litmuschaos.sh
	./scripts/port-forward.sh
	@echo "✅ Full stack deployed!"

# Deploy Kafka only (from local chart)
kafka:
	@echo "📦 Deploying Kafka from local chart..."
	./scripts/deploy-kafka.sh

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
	kubectl apply -f kates/k8s/postgres.yaml
	@echo "Waiting for PostgreSQL to be ready..."
	@kubectl wait --for=condition=Ready pod -l app=postgres -n kates --timeout=120s
	kubectl apply -f kates/k8s/deployment.yaml
	kubectl apply -f kates/k8s/service.yaml
	kubectl rollout status deployment/kates -n kates --timeout=120s
	@echo "✅ Kates is running"

kates-redeploy:
	@echo "🔄 Redeploying Kates..."
	kubectl rollout restart deployment/kates -n kates
	kubectl rollout status deployment/kates -n kates --timeout=120s

kates-logs:
	@echo "📋 Streaming Kates logs..."
	kubectl logs -f -l app=kates -n kates

kates-undeploy:
	@echo "🗑️  Removing Kates..."
	kubectl delete namespace kates --ignore-not-found
	@echo "✅ Kates removed"

CHART_REGISTRY ?= oci://ghcr.io/klster/charts
CHART_DIR      := charts/kates
CHART_VERSION  := $(shell grep '^version:' $(CHART_DIR)/Chart.yaml | awk '{print $$2}')

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

# Port Forwarding
ports:
	@echo "🔌 Starting Port Forwarding..."
	./scripts/port-forward.sh

# Download all Helm charts
download-charts:
	@echo "📦 Downloading all Helm charts..."
	./scripts/download-charts.sh

# LitmusChaos Management
litmus:
	@echo "⚡ Installing LitmusChaos..."
	./scripts/deploy-litmuschaos.sh

chaos-ui:
	@echo "🌐 Port-forwarding Litmus UI..."
	@echo "Access at: http://localhost:9091 (admin/litmus)"
	kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus

chaos-experiments:
	@echo "🧪 Deploying chaos experiments..."
	kubectl apply -f config/litmus/experiments/

# Kafka Chaos Testing
chaos-kafka:
	@echo "⚡ Setting up Kafka chaos testing environment..."
	./scripts/setup-kafka-chaos.sh

chaos-kafka-pod-delete:
	@echo "💥 Running Kafka broker pod-delete chaos..."
	kubectl apply -f config/litmus/experiments/kafka-pod-delete.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-network-partition:
	@echo "🔌 Running Kafka network partition chaos..."
	kubectl apply -f config/litmus/experiments/kafka-network-partition.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-cpu-stress:
	@echo "🔥 Running Kafka CPU stress chaos..."
	kubectl apply -f config/litmus/experiments/kafka-cpu-stress.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-memory-stress:
	@echo "🧠 Running Kafka memory stress chaos..."
	kubectl apply -f config/litmus/experiments/kafka-memory-stress.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-io-stress:
	@echo "💾 Running Kafka disk I/O stress chaos..."
	kubectl apply -f config/litmus/experiments/kafka-io-stress.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-dns-error:
	@echo "🌐 Running Kafka DNS error chaos..."
	kubectl apply -f config/litmus/experiments/kafka-dns-error.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-node-drain:
	@echo "🚧 Running Kafka node drain chaos..."
	kubectl apply -f config/litmus/experiments/kafka-node-drain.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-all:
	@echo "🌪️ Running ALL Kafka chaos experiments..."
	kubectl apply -f config/litmus/experiments/kafka-pod-delete.yaml
	kubectl apply -f config/litmus/experiments/kafka-network-partition.yaml
	kubectl apply -f config/litmus/experiments/kafka-cpu-stress.yaml
	kubectl apply -f config/litmus/experiments/kafka-memory-stress.yaml
	kubectl apply -f config/litmus/experiments/kafka-io-stress.yaml
	kubectl apply -f config/litmus/experiments/kafka-dns-error.yaml
	kubectl apply -f config/litmus/experiments/kafka-node-drain.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-status:
	@echo "📊 Kafka Chaos Status:"
	@echo ""
	@echo "=== Chaos Engines ==="
	@kubectl get chaosengines -n kafka 2>/dev/null || echo "No engines found"
	@echo ""
	@echo "=== Chaos Results ==="
	@kubectl get chaosresults -n kafka 2>/dev/null || echo "No results found"
	@echo ""
	@echo "=== Infrastructure Pods ==="
	@kubectl get pods -n litmus -l 'app in (chaos-operator,chaos-exporter,subscriber,workflow-controller,event-tracker)' 2>/dev/null || echo "No infra pods found"

chaos-clean:
	@echo "🧹 Removing LitmusChaos..."
	helm uninstall chaos -n litmus || true
	kubectl delete namespace litmus || true

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
	@echo "  all              - Complete setup (cluster, all services)"
	@echo "  cluster          - Start Kind cluster only"
	@echo "  monitoring       - Deploy Prometheus & Grafana"
	@echo "  kafka            - Deploy Kafka (Strimzi)"
	@echo "  ui               - Deploy Kafka UI"
	@echo "  apicurio         - Deploy Apicurio Registry"
	@echo "  jaeger           - Deploy Jaeger (distributed tracing)"
	@echo "  litmus           - Deploy LitmusChaos (with images)"
	@echo "  velero           - Deploy Velero backup"
	@echo ""
	@echo "  Kates CLI"
	@echo "  cli-build        - Cross-compile CLI for macOS + Linux (amd64/arm64)"
	@echo "  cli-install      - Build and install CLI on this machine"
	@echo "  cli-clean        - Remove CLI build artifacts"
	@echo ""
	@echo "  Kates Application (Docker + Kind)"
	@echo "  kates            - Build + deploy Kates (full pipeline)"
	@echo "  kates-build      - Build Kates JVM image and load into Kind"
	@echo "  kates-native     - Build Kates native image and load into Kind"
	@echo "  kates-deploy     - Apply Kates K8s manifests"
	@echo "  kates-redeploy   - Restart Kates deployment"
	@echo "  kates-logs       - Stream Kates logs"
	@echo "  kates-undeploy   - Remove Kates namespace"
	@echo ""
	@echo "  Performance Tests"
	@echo "  test             - Run baseline 1M-message perf test"
	@echo "  test-load        - Run load test (concurrent producers/consumers)"
	@echo "  test-stress      - Run stress test (ramp to breaking point)"
	@echo "  test-spike       - Run spike test (flash sale simulation)"
	@echo "  test-endurance   - Run endurance/soak test (sustained load)"
	@echo "  test-volume      - Run volume test (large data)"
	@echo "  test-capacity    - Run capacity test (find max throughput)"
	@echo ""
	@echo "  Operations"
	@echo "  ports            - Start port forwarding"
	@echo "  logs             - Stream logs from all services"
	@echo "  status           - Check cluster status"
	@echo "  chaos-ui         - Port-forward Litmus UI"
	@echo "  chaos-experiments- Apply chaos experiments"
	@echo "  gameday          - Run automated GameDay validation"
	@echo "  destroy          - Destroy cluster (FORCE=1 to skip prompt)"
	@echo "  help             - Show this help"

logs:
	@echo "📋 Streaming logs from all services (Ctrl+C to stop)..."
	@echo ""
	@kubectl logs -f -l app=kates -n kates --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l strimzi.io/cluster=krafter -n kafka --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app.kubernetes.io/name=grafana -n monitoring --prefix --tail=20 2>/dev/null &
	@kubectl logs -f -l app=kafka-ui -n kafka --prefix --tail=20 2>/dev/null &
	@wait
