.PHONY: all cluster monitoring deploy-all kafka ui test destroy clean download-charts litmus

# Default target: Launch cluster and monitoring
all:
	@echo "🚀 Launching complete cluster setup..."
	./start-cluster.sh
	./setup-registry.sh
	./pull-images.sh
	./deploy-all-from-kind.sh
	@echo "✅ Setup complete!"

# Start Kind cluster only
cluster:
	@echo "🎯 Starting Kind cluster..."
	./start-cluster.sh

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
litmus:
	@echo "⚡ Installing LitmusChaos from local chart..."
	./deploy-litmuschaos.sh

litmus-pull-images:
	@echo "🐳 Pulling Litmus images to local registry..."
	./pull-litmus-images.sh

chaos-ui:
	@echo "🌐 Port-forwarding Litmus UI..."
	@echo "Access at: http://localhost:9091 (admin/litmus)"
	kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus

chaos-experiments:
	@echo "🧪 Deploying chaos experiments..."
	kubectl apply -f config/litmus-experiments/

chaos-clean:
	@echo "🧹 Removing LitmusChaos..."
	helm uninstall chaos -n litmus || true
	kubectl delete namespace litmus || true

# Destroy Cluster
destroy:
	@echo "💥 Destroying Cluster..."
	./destroy.sh

# Alias for destroy
clean: destroy
