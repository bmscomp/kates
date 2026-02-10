.PHONY: all cluster monitoring deploy-all kafka ui test destroy clean download-charts litmus

# Default target: Launch complete cluster setup with all services
all: check-prerequisites
	@echo "🚀 Launching complete cluster setup..."
	@echo ""
	@echo "Step 1: Starting Kind cluster + registry..."
	./start-cluster.sh
	@echo ""
	@echo "Step 2: Pulling images to local registry..."
	./pull-images.sh
	@echo ""
	@echo "Step 3: Loading images into Kind cluster..."
	./load-images-to-kind.sh
	@echo ""
	@echo "Step 4: Deploying Monitoring (Prometheus & Grafana)..."
	./deploy-monitoring.sh
	@echo ""
	@echo "Step 5: Waiting for monitoring to be ready..."
	@kubectl wait --for=condition=Ready pods -l "app.kubernetes.io/name=grafana" -n monitoring --timeout=120s || true
	@echo ""
	@echo "Step 6: Deploying Kafka (Strimzi)..."
	./deploy-kafka.sh
	@echo ""
	@echo "Step 7: Waiting for Kafka to be ready..."
	@kubectl wait --for=condition=Ready pods -l strimzi.io/cluster=krafter -n kafka --timeout=300s || true
	@echo ""
	@echo "Step 8: Deploying Kafka UI..."
	./deploy-kafka-ui.sh
	@echo ""
	@echo "Step 9: Deploying Apicurio Registry..."
	./deploy-apicurio.sh
	@echo ""
	@echo "Step 10: Deploying LitmusChaos..."
	./deploy-litmuschaos.sh
	@echo ""
	@echo "✅ Complete setup finished!"
	@echo ""
	@echo "📊 Services deployed:"
	@echo "  ✓ Prometheus & Grafana (Monitoring)"
	@echo "  ✓ Kafka Cluster (Strimzi KRaft mode)"
	@echo "  ✓ Kafka UI"
	@echo "  ✓ Apicurio Registry"
	@echo "  ✓ LitmusChaos"
	@echo ""
	@echo "🔗 Access points:"
	@echo "  - Grafana: http://localhost:30080 (admin/admin)"
	@echo "  - Kafka UI: http://localhost:30081"
	@echo "  - Litmus UI: make chaos-ui then http://localhost:9091 (admin/litmus)"
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
	./start-cluster.sh

# Setup registry, pull all images, and load into Kind
images: registry-ensure
	@echo "🐳 Pulling and loading all images..."
	./pull-images.sh
	./load-images-to-kind.sh

# Ensure registry is running
registry-ensure:
	@echo "🐳 Ensuring local registry is running..."
	@if ! curl -s http://localhost:5001/v2/_catalog > /dev/null 2>&1; then \
		echo "Starting registry..."; \
		./setup-registry.sh; \
	else \
		echo "✅ Registry already running"; \
	fi

# Deploy monitoring stack only
monitoring:
	@echo "📊 Deploying monitoring stack..."
	./deploy-monitoring.sh

# Deploy full stack (monitoring, Kafka, UI, Litmus)
deploy-all:
	@echo "🚀 Deploying full stack..."
	./deploy-all-from-kind.sh

# Deploy Kafka only (from local chart)
kafka:
	@echo "📦 Deploying Kafka from local chart..."
	./deploy-kafka.sh

# Deploy Kafka UI only
ui:
	@echo "🖥️ Deploying Kafka UI..."
	./deploy-kafka-ui.sh

# Deploy Apicurio Registry
apicurio:
	@echo "📝 Deploying Apicurio Registry..."
	./deploy-apicurio.sh

# Run Performance Test
test:
	@echo "🧪 Running Performance Test..."
	./test-kafka-performance.sh

# Port Forwarding
ports:
	@echo "🔌 Starting Port Forwarding..."
	./port-forward.sh

# Registry Management
registry-setup:
	@echo "🐳 Setting up local Docker registry..."
	./setup-registry.sh
	./pull-images.sh

registry-status:
	@echo "📊 Checking registry status..."
	./registry-status.sh

registry-clean:
	@echo "🧹 Cleaning up registry..."
	./cleanup-registry.sh

# Download all Helm charts for offline use
download-charts:
	@echo "📦 Downloading all Helm charts..."
	./download-charts.sh

# LitmusChaos Management
litmus: registry-ensure
	@echo "⚡ Installing LitmusChaos..."
	./deploy-litmuschaos.sh

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
	./setup-kafka-chaos.sh

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
	./deploy-velero.sh

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
	./destroy.sh

# Alias for destroy
clean: destroy

# Help
help:
	@echo "Available targets:"
	@echo "  all              - Complete setup (cluster, registry, images, all services)"
	@echo "  cluster          - Start Kind cluster only"
	@echo "  images           - Pull and load all images"
	@echo "  monitoring       - Deploy Prometheus & Grafana"
	@echo "  kafka            - Deploy Kafka (Strimzi)"
	@echo "  ui               - Deploy Kafka UI"
	@echo "  apicurio         - Deploy Apicurio Registry"
	@echo "  litmus           - Deploy LitmusChaos (with images)"
	@echo "  chaos-ui         - Port-forward Litmus UI"
	@echo "  chaos-experiments- Apply chaos experiments"
	@echo "  velero           - Deploy Velero backup"
	@echo "  ports            - Start port forwarding"
	@echo "  status           - Check cluster status"
	@echo "  destroy          - Destroy cluster"
	@echo "  help             - Show this help"
