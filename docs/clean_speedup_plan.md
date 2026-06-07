# Kates Clean Speedup Plan

This document details the proposed optimization of the `kates clean` command. Currently, the cleaning process is extremely slow because it performs dozens of sequential `kubectl` and `helm` executions. We propose refactoring it to use parallel executions, bulk status checks, and batched deletions.

## Current Bottlenecks

1. **Sequential Discovery Scan**:
   Currently, discovery runs 54 sequential status checks (10 helm releases, 8 namespaces, 36 CRDs). If each command takes 100ms, it takes 5.4 seconds. On slow or remote clusters, this can easily escalate to 30+ seconds.
2. **Sequential Custom Resource Deletion**:
   Step 1 executes 80 sequential delete commands ($10 \text{ CRDs} \times 8 \text{ namespaces}$) regardless of whether the namespace or CRD even exists.
3. **Sequential Finalizer Patching**:
   Step 2 executes 80 sequential get/patch commands to clear finalizers.
4. **Sequential Helm Uninstall**:
   Step 3 uninstalls each Helm release sequentially, waiting for Helm to finish before moving to the next.
5. **Sequential Namespace Deletion**:
   Step 4 deletes namespaces one-by-one. Since namespace deletion is slow (waiting for resource cleanup), this creates a major blocking queue.
6. **Sequential CRD Deletion**:
   Step 5 deletes 36 CRDs sequentially using 36 separate `kubectl` commands.

---

## Proposed Optimizations

### 1. Bulk Discovery Scan
Instead of checking namespaces, CRDs, and Helm releases one-by-one, run 3 bulk queries and filter them in memory:
- `helm list -A -o json` to identify all installed releases.
- `kubectl get namespaces -o jsonpath={.items[*].metadata.name}` to get all existing namespaces.
- `kubectl get crd -o jsonpath={.items[*].metadata.name}` to get all existing CRDs.

This reduces 54 sequential process invocations to just 3 bulk queries, saving significant overhead.

### 2. Pre-Check Filtering for Deletions & Patching
- Skip deletion and finalizer patching for namespaces or CRDs that were detected as missing during the bulk scan.
- Run the remaining checks and deletions concurrently using goroutines.

### 3. Concurrent Helm Uninstall
- Uninstall installed Helm releases concurrently via goroutines using a wait group.

### 4. Parallel Namespace Deletion
- Trigger the deletion of all existing target namespaces concurrently via goroutines or a single multi-argument command:
  ```bash
  kubectl delete ns namespace1 namespace2 ...
  ```
  This allows Kubernetes to handle terminations in parallel.

### 5. Batched CRD Deletion
- Delete all detected CRDs in a single batched command:
  ```bash
  kubectl delete crd crd1 crd2 crd3 ...
  ```
  This reduces 36 sequential deletions to a single fast bulk operation.
