# Litmus Offline Installation - Quick Start

## TL;DR

```bash
# One-time setup (requires internet)
make litmus-offline-setup

# Deploy Litmus (no internet needed)
make litmus-offline
```

## What Gets Downloaded?

### Helm Charts
- Litmus chart v3.23.0 → `./charts/litmus/`

### Container Images (20+ images)
All images cached in local registry at `localhost:5001`:

**Core:**
- chaos-operator, chaos-runner, chaos-exporter

**Portal:**
- litmusportal-server, frontend, auth-server
- subscriber, event-tracker

**Database:**
- MongoDB + utilities

**Experiments:**
- go-runner, litmus-checker
- pod-delete, network-latency, network-loss
- cpu-hog, memory-hog, container-kill

## Commands

```bash
# Setup (one-time, needs internet)
make litmus-offline-setup          # Complete setup
make litmus-download-charts        # Charts only
make litmus-pull-images            # Images only

# Deploy (offline)
make litmus-offline                # Install Litmus

# Access UI
make chaos-ui                      # Port-forward to UI
# Then open: http://localhost:9091 (admin/litmus)

# Experiments
make chaos-experiments             # Deploy samples

# Cleanup
make chaos-clean                   # Remove Litmus
```

## Verify Setup

```bash
# Check registry
curl http://localhost:5001/v2/_catalog | jq

# Check charts
ls -la ./charts/litmus

# Check deployment
kubectl get pods -n litmus
```

## Files Created

```
charts/litmus/                     # Helm chart
config/litmus-values-offline.yaml  # Offline config
download-litmus-charts.sh          # Chart downloader
pull-litmus-images.sh              # Image puller
setup-litmus-offline.sh            # Complete setup
deploy-litmuschaos-offline.sh      # Offline installer
```

## Troubleshooting

**Registry not running?**
```bash
./setup-registry.sh
```

**Images missing?**
```bash
./pull-litmus-images.sh
```

**Charts missing?**
```bash
./download-litmus-charts.sh
```

For detailed documentation, see `LITMUS_OFFLINE_INSTALLATION.md`
