.PHONY: all cluster monitoring deploy-all kafka ui test test-load test-stress test-spike test-endurance test-volume test-capacity destroy clean download-charts litmus kates kates-build kates-native kates-deploy kates-logs kates-undeploy cli-build cli-install cli-clean

# Default target: Launch complete cluster setup with all services
all: check-prerequisites
	@echo "🚀 Launching complete cluster setup..."
	@echo ""
	@echo "Step 1: Starting Kind cluster + registry..."
	./scripts/start-cluster.sh
	@echo ""
	@echo "Step 2: Pulling images to local registry..."
	./scripts/pull-images.sh
	@echo ""
	@echo "Step 3: Loading images into Kind cluster..."
	./scripts/load-images-to-kind.sh
	@echo ""
	@echo "Step 4: Deploying Monitoring (Prometheus & Grafana)..."
	./scripts/deploy-monitoring.sh
	@echo ""
	@echo "Step 5: Waiting for monitoring to be ready..."
	@kubectl wait --for=condition=Ready pods -l "app.kubernetes.io/name=grafana" -n monitoring --timeout=120s || true
	@echo ""
	@echo "Step 6: Deploying Kafka (Strimzi)..."
	./scripts/deploy-kafka.sh
	@echo ""
	@echo "Step 7: Waiting for Kafka to be ready..."
	@kubectl wait --for=condition=Ready pods -l strimzi.io/cluster=krafter -n kafka --timeout=300s || true
	@echo ""
	@echo "Step 8: Deploying Kafka UI..."
	./scripts/deploy-kafka-ui.sh
	@echo ""
	@echo "Step 9: Deploying Apicurio Registry..."
	./scripts/deploy-apicurio.sh
	@echo ""
	@echo "Step 10: Deploying LitmusChaos..."
	./scripts/deploy-litmuschaos.sh
	@echo ""
	@echo "Step 11: Enabling chaos environment..."
	kubectl apply -f config/litmus/
	@echo ""
	@echo "Step 12: Building and deploying Kates..."
	cd kates && ./mvnw package -DskipTests -B
	docker build -f kates/Dockerfile -t kates:latest .
	kind load docker-image kates:latest --name panda
	kubectl apply -f kates/k8s/namespace.yaml
	kubectl apply -f kates/k8s/configmap.yaml
	kubectl apply -f kates/k8s/postgres.yaml
	@echo "Waiting for PostgreSQL to be ready..."
	@kubectl wait --for=condition=Ready pod -l app=postgres -n kates --timeout=120s
	kubectl apply -f kates/k8s/deployment.yaml
	kubectl apply -f kates/k8s/service.yaml
	@kubectl rollout status deployment/kates -n kates --timeout=120s
	@echo ""
	@echo "Step 13: Exposing service ports..."
	./scripts/port-forward.sh
	@echo ""
	@echo "✅ Complete setup finished!"
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
	@echo "  - Litmus UI:        http://localhost:9091  (admin/litmus)"
	@echo ""

# Check prerequisites
check-prerequisites:
	@echo "🔍 Checking prerequisites..."
	@command -v docker >/dev/null 2>&1 || { echo "❌ Docker not found"; exit 1; }
	@command -v kind >/dev/null 2>&1 || { echo "❌ Kind not found"; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl not found"; exit 1; }
	@command -v helm >/dev/null 2>&1 || { echo "❌ Helm not found"; exit 1; }
	@echo "✅ All prerequisites met"

# Start Kind cluster only
cluster:
	@echo "🎯 Starting Kind cluster..."
	./scripts/start-cluster.sh

# Setup registry, pull all images, and load into Kind
images: registry-ensure
	@echo "🐳 Pulling and loading all images..."
	./scripts/pull-images.sh
	./scripts/load-images-to-kind.sh

# Ensure registry is running
registry-ensure:
	@echo "🐳 Ensuring local registry is running..."
	@if ! curl -s http://localhost:5001/v2/_catalog > /dev/null 2>&1; then \
		echo "Starting registry..."; \
		./scripts/setup-registry.sh; \
	else \
		echo "✅ Registry already running"; \
	fi

# Deploy monitoring stack only
monitoring:
	@echo "📊 Deploying monitoring stack..."
	./scripts/deploy-monitoring.sh

# Deploy full stack (monitoring, Kafka, UI, Litmus)
deploy-all:
	@echo "🚀 Deploying full stack..."
	./scripts/deploy-all-from-kind.sh

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

cli-install: cli-build
	@echo "📦 Installing Kates CLI locally..."
	bash scripts/install-kates.sh

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
	cd kates && ./mvnw package -Dnative -DskipTests -B
	cd kates && docker build -f Dockerfile.native -t kates:latest .
	kind load docker-image kates:latest --name panda
	@echo "✅ Kates native image loaded into Kind"

kates-deploy:
	@echo "🚀 Deploying Kates to Kubernetes..."
	kubectl apply -f kates/k8s/namespace.yaml
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

# Port Forwarding
ports:
	@echo "🔌 Starting Port Forwarding..."
	./scripts/port-forward.sh

# Registry Management
registry-setup:
	@echo "🐳 Setting up local Docker registry..."
	./scripts/setup-registry.sh
	./scripts/pull-images.sh

registry-status:
	@echo "📊 Checking registry status..."
	./scripts/registry-status.sh

registry-clean:
	@echo "🧹 Cleaning up registry..."
	./scripts/cleanup-registry.sh

# Download all Helm charts for offline use
download-charts:
	@echo "📦 Downloading all Helm charts..."
	./scripts/download-charts.sh

# LitmusChaos Management
litmus: registry-ensure
	@echo "⚡ Installing LitmusChaos..."
	./scripts/deploy-litmuschaos.sh

chaos-ui:
	@echo "🌐 Port-forwarding Litmus UI..."
	@echo "Access at: http://localhost:9091 (admin/litmus)"
	kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus

chaos-experiments:
	@echo "🧪 Deploying chaos experiments..."
	kubectl apply -f config/litmus-experiments/

# Kafka Chaos Testing
chaos-kafka:
	@echo "⚡ Setting up Kafka chaos testing environment..."
	./scripts/setup-kafka-chaos.sh

chaos-kafka-pod-delete:
	@echo "💥 Running Kafka broker pod-delete chaos..."
	kubectl apply -f config/litmus-experiments/kafka-pod-delete.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-network-partition:
	@echo "🔌 Running Kafka network partition chaos..."
	kubectl apply -f config/litmus-experiments/kafka-network-partition.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-cpu-stress:
	@echo "🔥 Running Kafka CPU stress chaos..."
	kubectl apply -f config/litmus-experiments/kafka-cpu-stress.yaml
	@echo "Monitor: kubectl get chaosresults -n kafka -w"

chaos-kafka-all:
	@echo "🌪️ Running ALL Kafka chaos experiments..."
	kubectl apply -f config/litmus-experiments/kafka-pod-delete.yaml
	kubectl apply -f config/litmus-experiments/kafka-network-partition.yaml
	kubectl apply -f config/litmus-experiments/kafka-cpu-stress.yaml
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

# Status check
status:
	@echo "📊 Cluster Status:"
	@echo ""
	@echo "=== Pods by Namespace ==="
	@kubectl get pods -A --no-headers | awk '{print $$1}' | sort | uniq -c | sort -rn
	@echo ""
	@echo "=== Not Ready Pods ==="
	@kubectl get pods -A | grep -v Running | grep -v Completed || echo "All pods are running!"

# Destroy Cluster
destroy:
	@echo "💥 Destroying Cluster..."
	./scripts/destroy.sh

# Alias for destroy
clean: destroy

# Help
help:
	@echo "Available targets:"
	@echo ""
	@echo "  Cluster & Infrastructure"
	@echo "  all              - Complete setup (cluster, registry, images, all services)"
	@echo "  cluster          - Start Kind cluster only"
	@echo "  images           - Pull and load all images"
	@echo "  monitoring       - Deploy Prometheus & Grafana"
	@echo "  kafka            - Deploy Kafka (Strimzi)"
	@echo "  ui               - Deploy Kafka UI"
	@echo "  apicurio         - Deploy Apicurio Registry"
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
	@echo "  status           - Check cluster status"
	@echo "  chaos-ui         - Port-forward Litmus UI"
	@echo "  chaos-experiments- Apply chaos experiments"
	@echo "  destroy          - Destroy cluster"
	@echo "  help             - Show this help"
