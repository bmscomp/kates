# Litmus Offline Installation Guide

This guide explains how to install LitmusChaos on a local Kubernetes cluster completely offline, without pulling any images or charts from the internet during installation.

## Overview

The offline installation process consists of two phases:

1. **Preparation Phase** (requires internet): Download all Helm charts and container images
2. **Installation Phase** (no internet required): Deploy Litmus using local resources

## Prerequisites

- Docker
- Kind (Kubernetes in Docker)
- Kubectl
- Helm 3.x
- Internet connection (for preparation phase only)

## Quick Start

### One-Command Setup

For a complete offline setup, run:

```bash
make litmus-offline-setup
```

This will:
- Setup local Docker registry at `localhost:5001`
- Download Litmus Helm charts to `./charts/litmus`
- Pull all required container images to local registry

### Deploy Litmus Offline

Once setup is complete, deploy Litmus without internet:

```bash
make litmus-offline
```

## Detailed Steps

### Phase 1: Preparation (One-Time Setup)

#### Step 1: Setup Local Docker Registry

```bash
./setup-registry.sh
```

This creates a local Docker registry at `localhost:5001` that will cache all images.

#### Step 2: Download Helm Charts

```bash
./download-litmus-charts.sh
```

This downloads the Litmus Helm chart (version 3.23.0) to `./charts/litmus`.

#### Step 3: Pull All Container Images

```bash
./pull-litmus-images.sh
```

This pulls all required images to the local registry:

**Core Components:**
- `litmuschaos/chaos-operator:3.23.0`
- `litmuschaos/chaos-runner:3.23.0`
- `litmuschaos/chaos-exporter:3.23.0`

**Portal Components:**
- `litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0`
- `litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0`
- `litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0`
- `litmuschaos/litmusportal-subscriber:3.23.0`
- `litmuschaos/litmusportal-event-tracker:3.23.0`

**Database:**
- `docker.io/bitnami/mongodb:latest`
- `docker.io/bitnamilegacy/os-shell:12-debian-12-r51`

**Experiment Images:**
- `litmuschaos/go-runner:3.23.0`
- `litmuschaos/litmus-checker:3.23.0`
- `litmuschaos/litmus-app-pod-delete:3.23.0`
- `litmuschaos/litmus-app-pod-network-latency:3.23.0`
- `litmuschaos/litmus-app-pod-network-loss:3.23.0`
- `litmuschaos/litmus-app-pod-cpu-hog:3.23.0`
- `litmuschaos/litmus-app-pod-memory-hog:3.23.0`
- `litmuschaos/litmus-app-container-kill:3.23.0`

### Phase 2: Offline Installation

Once preparation is complete, you can install Litmus completely offline:

```bash
./deploy-litmuschaos-offline.sh
```

This script:
- Uses the local Helm chart from `./charts/litmus`
- Configures all components to use images from `localhost:5001`
- Sets `imagePullPolicy: Never` to prevent any external pulls
- Creates ServiceMonitor for Prometheus integration

## Configuration Files

### `config/litmus-values-offline.yaml`

This file configures Litmus to use the local registry for all images:

```yaml
# All images use localhost:5001 registry
operator:
  image:
    repository: localhost:5001/litmuschaos/chaos-operator
    tag: "3.23.0"
    pullPolicy: Never
```

Key features:
- All image repositories point to `localhost:5001`
- `pullPolicy: Never` prevents external pulls
- Portal UI exposed via NodePort on port 30091
- MongoDB configured with local images
- Prometheus ServiceMonitor enabled

## Accessing Litmus UI

After installation, access the Litmus UI:

```bash
make chaos-ui
```

Or manually:

```bash
kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus
```

Then open: http://localhost:9091

**Default Credentials:**
- Username: `admin`
- Password: `litmus`

## Makefile Targets

### Setup and Installation

- `make litmus-offline-setup` - Complete offline setup (download charts + images)
- `make litmus-offline` - Deploy Litmus in offline mode
- `make litmus-download-charts` - Download Helm charts only
- `make litmus-pull-images` - Pull images to local registry only

### Management

- `make chaos-ui` - Port-forward to Litmus UI
- `make chaos-experiments` - Deploy sample chaos experiments
- `make chaos-clean` - Uninstall Litmus completely

### Registry

- `make registry-status` - Check local registry status
- `make registry-clean` - Clean up local registry

## Verification

### Check Registry Contents

```bash
curl http://localhost:5001/v2/_catalog | jq
```

### Check Litmus Pods

```bash
kubectl get pods -n litmus
```

Expected pods:
- `chaos-operator-ce-*` - Main operator
- `chaos-exporter-*` - Metrics exporter
- `chaos-litmus-frontend-*` - Web UI
- `chaos-litmus-server-*` - Backend server
- `chaos-litmus-auth-server-*` - Authentication
- `mongo-*` - MongoDB database

### Check ServiceMonitor

```bash
kubectl get servicemonitor -n litmus
```

## Troubleshooting

### Images Not Found

If pods fail with `ImagePullBackOff`:

1. Check registry is running:
   ```bash
   curl http://localhost:5001/v2/_catalog
   ```

2. Verify images are in registry:
   ```bash
   curl http://localhost:5001/v2/_catalog | jq '.repositories | map(select(contains("litmus")))'
   ```

3. Re-pull images:
   ```bash
   ./pull-litmus-images.sh
   ```

### Apple Silicon (M1/M2) Issues

For Apple Silicon Macs, the setup automatically handles platform-specific images:

```bash
# Automatically runs on Apple Silicon
./force-platform-load.sh
```

### Chart Not Found

If Helm can't find the chart:

1. Verify chart exists:
   ```bash
   ls -la ./charts/litmus
   ```

2. Re-download chart:
   ```bash
   rm -rf ./charts/litmus
   ./download-litmus-charts.sh
   ```

### MongoDB Issues

If MongoDB fails to start:

1. Check PVC:
   ```bash
   kubectl get pvc -n litmus
   ```

2. Check MongoDB logs:
   ```bash
   kubectl logs -n litmus -l app.kubernetes.io/name=mongodb
   ```

## Directory Structure

```
klster/
├── charts/                          # Downloaded Helm charts
│   └── litmus/                      # Litmus chart (downloaded)
├── config/
│   ├── litmus-values.yaml           # Online configuration
│   ├── litmus-values-offline.yaml   # Offline configuration
│   └── litmus-experiments/          # Sample experiments
├── download-litmus-charts.sh        # Download Helm charts
├── pull-litmus-images.sh            # Pull images to registry
├── setup-litmus-offline.sh          # Complete offline setup
└── deploy-litmuschaos-offline.sh    # Offline deployment
```

## Advanced Usage

### Custom Experiments

Deploy custom chaos experiments:

```bash
kubectl apply -f config/litmus-experiments/pod-delete.yaml
```

### View Metrics in Grafana

Litmus exports metrics to Prometheus. Access Grafana:

```bash
kubectl port-forward svc/monitoring-grafana 30080:80 -n monitoring
```

Open: http://localhost:30080 (admin/admin)

### Cleanup and Reinstall

```bash
# Remove Litmus
make chaos-clean

# Reinstall offline
make litmus-offline
```

## Network Isolation

To verify true offline operation:

1. Disconnect from internet
2. Run deployment:
   ```bash
   ./deploy-litmuschaos-offline.sh
   ```

All resources should deploy successfully without internet access.

## Version Information

- **Litmus Version:** 3.23.0
- **Helm Chart Version:** 3.23.0
- **MongoDB Version:** latest (Bitnami)
- **Local Registry:** localhost:5001

## Additional Resources

- [Litmus Documentation](https://docs.litmuschaos.io/)
- [Chaos Experiments Hub](https://hub.litmuschaos.io/)
- [Litmus GitHub](https://github.com/litmuschaos/litmus)

## Support

For issues specific to this offline setup, check:
1. Registry status: `make registry-status`
2. Cluster status: `kubectl get nodes`
3. Pod status: `kubectl get pods -n litmus`
4. Logs: `kubectl logs -n litmus -l app.kubernetes.io/component=operator`
