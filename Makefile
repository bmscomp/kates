.PHONY: all deploy ui test destroy clean ps argo-install argo-cli-install argo-ui argo-clean

# Default target: Launch cluster, deploy Kafka, and deploy UI
all:
	@echo "🚀 Launching full stack..."
	./launch.sh
	./deploy-kafka.sh
	@echo "Installing LitmusChaos..."
	./deploy-litmuschaos.sh
	@echo "✅ Stack deployed!"

# Deploy Kafka and Dashboards only
deploy:
	@echo "📦 Deploying Kafka and Dashboards..."
	./deploy-kafka.sh kafka

# Deploy everything from Kind images (no registry pulls)
deploy-offline:
	@echo "📦 Deploying from Kind images (offline mode)..."
	./deploy-from-kind.sh

# Deploy Kafka UI only
ui:
	@echo "🖥️ Deploying Kafka UI..."
	./deploy-kafka.sh ui

# Run Performance Test
test:
	@echo "🧪 Running Performance Test..."
	./test-kafka-performance.sh

# Port Forwarding
ports:
	@echo "🔌 Starting Port Forwarding..."
	./port-forward.sh

# Cluster Status (nodes, pods, memory)
ps:
	@./ps.sh

# LitmusChaos Management
chaos-install:
	@echo "⚡ Installing LitmusChaos..."
	./deploy-litmuschaos.sh

chaos-experiments:
	@echo "🧪 Deploying chaos experiments..."
	kubectl apply -f config/litmus-experiments/

chaos-ui:
	@echo "🖥️  Starting LitmusChaos UI..."
	@echo "Access at http://localhost:9091 (admin/litmus)"
	@kubectl --context kind-panda port-forward svc/chaos-litmus-frontend-service -n litmus 9091:9091

chaos-clean:
	@echo "🧹 Removing LitmusChaos..."
	helm uninstall chaos -n litmus || true
	kubectl delete namespace litmus || true

# Argo Workflows Management
argo-install:
	@echo "⚡ Installing Argo Workflows..."
	./deploy-argo.sh

argo-cli-install:
	@echo "📦 Installing Argo CLI..."
	./install-argo-cli.sh

argo-ui:
	@echo "🖥️  Starting Argo Workflows UI..."
	@echo "Access at https://localhost:2746 (accept self-signed certificate)"
	@kubectl --context kind-panda port-forward svc/argo-server -n argo 2746:2746

argo-clean:
	@echo "🧹 Removing Argo Workflows..."
	kubectl delete -n argo -f https://github.com/argoproj/argo-workflows/releases/download/v3.5.5/install.yaml || true
	kubectl delete namespace argo || true

# Chaos Workflow Management
chaos-workflows-deploy:
	@echo "🚀 Deploying chaos workflows..."
	./manage-chaos-workflows.sh deploy

chaos-workflows-run:
	@echo "⚡ Running chaos test suite..."
	./manage-chaos-workflows.sh run-suite

chaos-workflows-load:
	@echo "📊 Running load test with chaos..."
	./manage-chaos-workflows.sh run-load-chaos

chaos-workflows-schedule:
	@echo "⏰ Enabling scheduled chaos tests..."
	./manage-chaos-workflows.sh enable-schedule

chaos-workflows-status:
	@echo "📋 Checking workflow status..."
	./manage-chaos-workflows.sh status

chaos-workflows-clean:
	@echo "🧹 Cleaning up workflows..."
	./manage-chaos-workflows.sh clean

# Destroy Cluster
destroy:
	@echo "💥 Destroying Cluster..."
	./destroy.sh

# Alias for destroy
clean: destroy
