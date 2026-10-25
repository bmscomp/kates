# Publishing Kates Helm Charts to Artifact Hub

Kates packages and signs 15 production-grade Helm charts under GitHub Container Registry (GHCR) as OCI artifacts:
`oci://ghcr.io/bmscomp/charts/<chart-name>:<version>`

Every chart is signed keyless with **Cosign** (Sigstore OIDC) and accompanied by **SLSA build provenance attestations**.

This guide walks through registering the charts on [Artifact Hub](https://artifacthub.io/) to earn the **Signed** and **Verified Publisher** trust badges.

---

## 1. Top Flagship Charts to Register

Artifact Hub indexes OCI Helm charts per repository path. Here are the primary charts to register:

| Chart | OCI Registry URL | Description |
|:---|:---|:---|
| **kates** | `oci://ghcr.io/bmscomp/charts/kates` | Kates testing suite & Quarkus virtual-thread benchmark engine |
| **kafka-cluster** | `oci://ghcr.io/bmscomp/charts/kafka-cluster` | 3-node KRaft Kafka cluster with rack awareness and PodDisruptionBudgets |
| **strimzi-operator** | `oci://ghcr.io/bmscomp/charts/strimzi-operator` | Strimzi Kafka operator with automated CRD upgrade hook jobs |
| **kates-chaos** | `oci://ghcr.io/bmscomp/charts/kates-chaos` | 13 Litmus & K8s native chaos experiments with safety guardrails |
| **connect-cluster** | `oci://ghcr.io/bmscomp/charts/connect-cluster` | Distributed Kafka Connect cluster with Kaniko build support |
| **mirror-maker2** | `oci://ghcr.io/bmscomp/charts/mirror-maker2` | Active-Active and Active-Passive cross-cluster replication |
| **kates-monitoring** | `oci://ghcr.io/bmscomp/charts/kates-monitoring` | Prometheus, Alertmanager, and 12 opinionated Grafana dashboards |
| **kates-platform** | `oci://ghcr.io/bmscomp/charts/kates-platform` | Umbrella platform chart orchestrating the full lab topology |

---

## 2. Registering on Artifact Hub

1. Log in to [artifacthub.io](https://artifacthub.io) (using your GitHub account).
2. Go to **Control Panel** → **Repositories** → **Add repository**:
   * **Kind**: `Helm charts`
   * **Name**: e.g. `kates` (or `kafka-cluster`, etc.)
   * **Display Name**: `Kates` (or `Kafka Cluster`, etc.)
   * **URL**: `oci://ghcr.io/bmscomp/charts/kates`
3. Click **Add**.
4. Artifact Hub will automatically scan the registry, detect the versions, read the rich `artifacthub.io` metadata already inside `Chart.yaml`, and detect the **Cosign signature** to grant the **Signed** badge.

---

## 3. Claiming Ownership (Verified Publisher Badge)

To earn the green **Verified Publisher** badge:

1. In the Artifact Hub Control Panel, locate your added repository and note the generated **Repository ID** (a UUID such as `14bd8402-9829-4f9b-b71e-e496fc1307f5`).
2. Run the GitHub Actions workflow to push the `artifacthub.io` OCI metadata tag:

   **Via GitHub CLI (`gh`):**
   ```bash
   gh workflow run artifacthub.yml \
     -f chart=kates \
     -f repository_id="<YOUR-REPOSITORY-UUID>"
   ```

   **Via GitHub UI:**
   * Go to **Actions** → **Artifact Hub · Metadata** → **Run workflow**
   * Select `chart` (e.g. `kates`)
   * Enter the `repository_id` UUID
   * Click **Run workflow**

3. Back on Artifact Hub, click **Claim ownership** on the repository. Artifact Hub verifies the OCI tag and immediately awards the **Verified Publisher** badge!

---

## 4. Bulk Publishing All Charts

To push baseline ownership metadata to all 15 charts at once before registering them:

```bash
gh workflow run artifacthub.yml -f chart=all
```
