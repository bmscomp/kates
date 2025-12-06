# Local Kubernetes Cluster with Monitoring

This project provides scripts to launch a local Kubernetes cluster using [Kind](https://kind.sigs.k8s.io/) with 3 nodes simulating different availability zones, and sets up monitoring with Prometheus and Grafana.

## Prerequisites

Ensure you have the following installed:
- [Docker](https://www.docker.com/) with at least **32GB RAM** and **8 CPUs** allocated
  - Docker Desktop: Go to Settings → Resources and set Memory to 32GB, CPUs to 8
  - Each Kind node requires ~10GB RAM (3 nodes total)
- [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [Kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)

## Quick Start

### Option 1: Full Stack (Recommended)
```bash
make all
```
This will:
- Create Kind cluster with 3 nodes (`alpha`, `sigma`, `gamma`)
- Install Prometheus & Grafana monitoring
- Deploy Kafka cluster
- Install LitmusChaos

### Option 2: Step by Step
```bash
# 1. Create Kind cluster
./launch.sh

# 2. Install monitoring (Prometheus + Grafana)
make monitoring

# 3. Deploy Kafka
make deploy

# 4. Install LitmusChaos (optional)
make chaos-install
```

### Access Services
- **Grafana**: http://localhost:30080 (admin/admin)
- **Kafka UI**: http://localhost:30081
- **Prometheus**: http://localhost:30090

*Note: Use port-forwarding if NodePort is not accessible:*
```bash
make ports
```

### Cleanup
```bash
make destroy
```

## Kafka Deployment

To deploy a Kafka Strimzi cluster with KRaft mode and monitoring:

1. **Run the deployment script:**
   ```bash
   ./deploy-kafka.sh
   ```
   This will:
   - Install the Strimzi Cluster Operator via Helm.
   - Deploy a Kafka cluster with 3 brokers (one per zone).
   - Configure Prometheus metrics and a custom Grafana dashboard.

2. **Access the Dashboards:**
   - Go to Grafana (http://localhost:30080).
   - Look for the## 🛠️ Makefile Shortcuts

You can use the `Makefile` to manage the lifecycle of the cluster:

**Cluster & Deployment:**
- **`make all`**: 🚀 Launch cluster + monitoring + Kafka + LitmusChaos (full stack).
- **`make monitoring`**: 📊 Deploy Prometheus & Grafana monitoring stack.
- **`make deploy`**: 📦 Deploy Kafka and Dashboards.
- **`make deploy-offline`**: 📦 Deploy everything from Kind images only (no registry pulls).
- **`make ui`**: 🖥️ Deploy Kafka UI.

**Testing & Monitoring:**
- **`make test`**: 🧪 Run the performance test script.
- **`make ports`**: 🔌 Start port forwarding for Grafana, Kafka UI, and Prometheus.
- **`make ps`**: 📊 Show cluster status (nodes, pods, CPU, memory).

**Chaos Engineering:**
- **`make chaos-install`**: ⚡ Install LitmusChaos operator.
- **`make chaos-ui`**: 🖥️ Open LitmusChaos UI.
- **`make chaos-experiments`**: 🧪 Deploy sample chaos experiments.
- **`make chaos-clean`**: 🧹 Remove LitmusChaos.

**Argo Workflows:**
- **`make argo-install`**: ⚡ Install Argo Workflows.
- **`make argo-cli-install`**: 📦 Install Argo CLI (required for workflows).
- **`make argo-ui`**: 🖥️ Open Argo Workflows UI.
- **`make argo-clean`**: 🧹 Remove Argo Workflows.

**Cleanup:**
- **`make destroy`**: 💥 Destroy the cluster.

## Features

-✨ **Direct Image Loading**: All container images are pulled from Docker registries and loaded directly into Kind nodes for faster deployments and offline operation. Images are cached in Kind with their original names, supporting `imagePullPolicy: Never`. The `pull-images.sh` script handles all image pulling, verification, and loading into Kind.

### 📦 Image Management Scripts

- **`./pull-images.sh`**: Pull images from registries and load into Kind
- **`./load-images-to-kind.sh`**: Load images from any registry to Kind (uses `images.txt`)
- **`./deploy-from-kind.sh`**: Deploy all services using only images in Kind (offline mode)
- **`./portability/export-kind-images.sh`**: Export all images from Kind cluster to tar archives
- **`./portability/import-kind-images.sh`**: Import previously exported images into Kind cluster

**Image List**: `images.txt` contains all required images for the full stack (Kafka, Monitoring, LitmusChaos, Argo)

**Use Cases**: 
- **Registry to Kind**: Use `load-images-to-kind.sh` to pull from any registry and load into Kind
- **Custom Images**: Edit `images.txt` to add/remove images for selective loading
- **Offline Deployment**: Use `deploy-from-kind.sh` to deploy without internet access
- **Air-Gapped Environments**: Export/import for isolated networks
- **Backup/Restore**: Save and restore exact image states

## 🧪 Chaos Engineering with LitmusChaos

This project integrates [LitmusChaos](https://litmuschaos.io/) for comprehensive Kafka cluster resilience testing.

### 🚀 Quick Setup

1. **Install LitmusChaos**: `make chaos-install`
2. **Access Portal**: `make chaos-ui` → http://localhost:9091 (admin/litmus)
3. **Enable Infrastructure**: Follow the [LitmusChaos Setup Guide](LITMUS-SETUP-GUIDE.md)
4. **Run Experiments**: `make chaos-workflows-run`

📖 **Detailed Guide**: See [LITMUS-SETUP-GUIDE.md](LITMUS-SETUP-GUIDE.md) for complete setup instructions.

### Quick Setup

```bash
# Install LitmusChaos operator, UI, and experiments (included in make all)
make chaos-install

# Or deploy as part of full stack
make all

# Access the LitmusChaos UI
make chaos-ui
# Open http://localhost:9091
# Default credentials: admin / litmus

# Deploy chaos experiments
make chaos-experiments
```

### Available Chaos Experiments

#### 1. **Pod Delete** (`01-pod-delete-experiment.yaml`)
Randomly deletes Kafka pods to test recovery and replication.
- **Duration**: 30s
- **Interval**: 10s  
- **Affected**: 50% of pods
- **Tests**: StatefulSet recovery, leader election, data replication

#### 2. **Container Kill** (`02-container-kill-experiment.yaml`)
Kills Kafka containers to test restart policies and data consistency.
- **Duration**: 60s
- **Interval**: 10s
- **Target**: kafka container
- **Tests**: Container restart, data persistence, client reconnection

#### 3. **Node Drain** (`03-node-drain-experiment.yaml`)
Drains a Kubernetes node to test pod rescheduling and cluster rebalancing.
- **Duration**: 60s
- **Scope**: Cluster-wide
- **Tests**: Pod rescheduling, zone awareness, partition rebalancing

#### 4. **Network Loss** (`04-network-loss-experiment.yaml`)
Introduces packet loss to test network resilience.
- **Duration**: 60s
- **Packet Loss**: 20%
- **Tests**: Network resilience, replication lag, client timeouts

#### 5. **Disk Fill** (`05-disk-fill-experiment.yaml`)
Fills disk space to test storage monitoring and alerts.
- **Duration**: 60s
- **Fill**: 80%
- **Tests**: Storage pressure, log compaction, disk monitoring

### Running Chaos Experiments

#### Prerequisites

1. **RBAC Setup** (automatically applied by `make chaos-install`):
   ```bash
   kubectl apply -f config/litmus-experiments/00-chaosengine-rbac.yaml
   ```

2. **Verify Kafka is Running**:
   ```bash
   kubectl get kafka krafter -n kafka
   ```

3. **Verify LitmusChaos Operator**:
   ```bash
   kubectl get pods -n litmus
   ```

#### Apply All Experiments

```bash
kubectl apply -f config/litmus-experiments/
```

#### Run Individual Experiment

Create a ChaosEngine to trigger an experiment:

```yaml
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosEngine
metadata:
  name: kafka-pod-delete-test
  namespace: kafka
spec:
  appinfo:
    appns: kafka
    applabel: 'strimzi.io/cluster=krafter'
    appkind: statefulset
  engineState: active
  chaosServiceAccount: kafka-chaos-sa
  experiments:
    - name: pod-delete
      spec:
        components:
          env:
            - name: TOTAL_CHAOS_DURATION
              value: '30'
            - name: CHAOS_INTERVAL
              value: '10'
```

Apply it:
```bash
kubectl apply -f my-chaos-engine.yaml
```

### Monitoring Chaos Results

#### View Experiment Status
```bash
kubectl get chaosexperiments -n kafka
```

#### View Chaos Results
```bash
kubectl get chaosresults -n kafka
```

#### View Detailed Results
```bash
kubectl describe chaosresult <result-name> -n kafka
```

#### View Chaos Logs
```bash
kubectl logs -n kafka -l job-name=<chaos-job-name>
```

#### Grafana Dashboards

Monitor chaos impact in real-time:
1. Access Grafana: http://localhost:30080
2. Navigate to Dashboards → LitmusChaos
3. Monitor:
   - Experiment success rate
   - Kafka cluster health during chaos
   - Recovery time
   - Message throughput impact
   - Partition replication status

### Best Practices

1. **Start Small**: Begin with short durations and low impact percentages
2. **Monitor Continuously**: Always watch Grafana dashboards during experiments
3. **Document Results**: Record observations and recovery times
4. **Gradual Increase**: Increase chaos intensity gradually
5. **Production-like**: Test scenarios that match real production failures
6. **Baseline First**: Establish normal performance metrics before chaos testing

### Chaos Cleanup

#### Stop Running Experiments
```bash
kubectl delete chaosengine --all -n kafka
```

#### Remove Experiments
```bash
kubectl delete chaosexperiments --all -n kafka
```

#### Clean Results
```bash
kubectl delete chaosresults --all -n kafka
```

#### Uninstall LitmusChaos
```bash
make chaos-clean
```

### Troubleshooting Chaos Experiments

#### Experiment Not Starting
- Check RBAC: `kubectl get sa kafka-chaos-sa -n kafka`
- Check operator logs: `kubectl logs -n litmus -l app.kubernetes.io/component=operator`
- Verify experiment exists: `kubectl get chaosexperiments -n kafka`

#### Experiment Failed
- View result details: `kubectl describe chaosresult <name> -n kafka`
- Check pod logs: `kubectl logs -n kafka -l job-name=<chaos-job>`
- Verify target pods exist: `kubectl get pods -n kafka -l strimzi.io/cluster=krafter`

#### Images Not Found
- Ensure images are loaded: `make ps`
- Check image pull policy is `Never` in experiment definitions
- Verify images in Kind: `docker exec panda-control-plane crictl images | grep litmus`

### Advanced Chaos Workflow Management

The project includes sophisticated Argo-based chaos workflows for comprehensive Kafka resilience testing:

#### Available Workflows

1. **Kafka Chaos Suite** (`kafka-chaos-suite`)
   - Progressive chaos testing through 8 phases
   - Automated metrics collection at each phase
   - Comprehensive report generation
   - Tests: Pod delete, container kill, network loss/latency, CPU/memory stress, disk fill, node drain

2. **Load Testing with Chaos** (`kafka-load-chaos`)
   - Combines performance testing with chaos injection
   - Measures throughput under various chaos conditions
   - Baseline vs chaos performance comparison
   - Tests producer resilience under failure

3. **Scheduled Chaos** (`kafka-chaos-schedule`)
   - Daily automated chaos testing (2 AM UTC)
   - Random experiment selection
   - Automated recovery verification
   - Historical test tracking

#### Workflow Management Commands

```bash
# Deploy all chaos workflows
make chaos-workflows-deploy
# or
./manage-chaos-workflows.sh deploy

# Run comprehensive chaos test suite
make chaos-workflows-run
# or
./manage-chaos-workflows.sh run-suite

# Run load testing with chaos
make chaos-workflows-load
# or
./manage-chaos-workflows.sh run-load-chaos

# Enable scheduled daily tests
make chaos-workflows-schedule
# or
./manage-chaos-workflows.sh enable-schedule

# Check workflow status
make chaos-workflows-status
# or
./manage-chaos-workflows.sh status

# View workflow logs
./manage-chaos-workflows.sh logs <workflow-name>

# Clean up workflows
make chaos-workflows-clean
# or
./manage-chaos-workflows.sh clean
```

#### Viewing Workflow Results

1. **Argo UI**: https://localhost:2746
   - Visual workflow progress
   - Step-by-step execution logs
   - Workflow DAG visualization

2. **Grafana Dashboards**: http://localhost:30080
   - Real-time Kafka metrics during chaos
   - Performance impact analysis
   - Recovery time tracking

3. **Command Line**:
   ```bash
   # List all workflows
   kubectl get workflows -n argo
   
   # Describe specific workflow
   kubectl describe workflow kafka-chaos-suite -n argo
   
   # View logs
   argo logs kafka-chaos-suite -n argo
   ```

### Project Structure

The LitmusChaos setup includes:
- **Project Configuration**: `config/litmus-project.yaml` - Namespace, RBAC, project metadata
- **Helm Values**: `config/litmus-values.yaml` - Resource limits, image configs
- **Experiments**: `config/litmus-experiments/` - All chaos experiment definitions
- **Workflows**: `config/litmus-workflows/` - Advanced Argo-based chaos workflows
  - `kafka-chaos-suite.yaml` - Comprehensive progressive testing
  - `kafka-load-chaos.yaml` - Performance testing with chaos
  - `kafka-chaos-schedule.yaml` - Scheduled automated testing
- **Management Script**: `manage-chaos-workflows.sh` - Workflow lifecycle management
- **Documentation**: `config/litmus-experiments/README.md` - Detailed experiment guide
🚀 **Quick Setup**: One-command deployment of full Kafka + monitoring stack  
📊 **Comprehensive Monitoring**: Prometheus, Grafana, and custom Kafka dashboards  
⚡ **Performance Testing**: Built-in Kafka performance test scripts  
🧪 **Chaos Engineering**: LitmusChaos integration for resilience testing  
🖥️ **Kafka UI**: Web-based interface for Kafka cluster management

## 📊 Monitoring & Dashboards

**Unified Kafka Dashboard** (all working ✅):
- **Kafka**: ⭐ Comprehensive unified dashboard with all metrics
  - Cluster Overview: Broker status, partitions, zones
  - Performance Metrics: Throughput, latency, topic growth
  - JVM Metrics: Memory, GC, threads, heap usage
  - Network Metrics: Bytes in/out, request rates
  - Consumer/Producer Metrics: Lag, offsets
  - Node Affinity: Kubernetes zone distribution
  - Performance Test Results: Real-time test metrics

## Performance Testing

To test Kafka cluster performance with 1 million messages:

1. **Run the performance test:**
   ```bash
   ./test-kafka-performance.sh
   ```
   This will:
   - Create a `performance` namespace
   - Create a test topic with 3 partitions
   - Deploy a producer that sends 1 million messages
   - Deploy a consumer that reads those messages
   - Display throughput and latency metrics

2. **Cleanup after testing:**
   ```bash
   kubectl delete namespace performance
   ```

## Kafka UI

A web-based UI to manage and browse your Kafka cluster:

1. **Deploy Kafka UI:**
   ```bash
   ./deploy-kafka-ui.sh
   ```

2. **Access the UI:**
   - URL: http://localhost:30081
   - Features:
     - Browse topics and partitions
     - View message content and headers
     - Monitor consumer groups and lag
     - View broker configurations
     - Full KRaft mode support

## Configuration

- **`config/cluster.yaml`**: Defines the Kind cluster topology.
    - Node 1 (Control Plane): Name `alpha`, Zone `alpha`
    - Node 2 (Worker): Name `sigma`, Zone `sigma`
    - Node 3 (Worker): Name `gamma`, Zone `gamma`
    - *Note: Resource limits (3CPU/6GB/10GB Storage) are defined as instance types labels for simulation, as Kind relies on host Docker resources.*

- **`config/monitoring.yaml`**: Helm values for `kube-prometheus-stack`.
    - Configures Grafana admin password and NodePort.

- **`config/custom-dashboard.yaml`**: ConfigMap containing a custom "Global Resource Vision" dashboard.

