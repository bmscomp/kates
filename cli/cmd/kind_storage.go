package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// quickDetectKind checks whether the current cluster is a Kind cluster by
// looking for the kindnet DaemonSet in kube-system. This is a lightweight
// probe that runs BEFORE the full collector.Collect(), so that
// setupKindStorageClasses can create the zone-specific StorageClasses before
// detect introspects storage — ensuring matchStorageClass() returns the right
// class names in values-detected.yaml.
func quickDetectKind() bool {
	out, err := exec.Command(
		"kubectl", "get", "daemonset", "kindnet",
		"-n", "kube-system",
		"--ignore-not-found",
		"--no-headers",
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// setupKindStorageClasses ensures that every zone-specific StorageClass
// expected by charts/kafka-cluster/values-kind.yaml exists before the Kafka
// chart is installed.
//
// Kind clusters ship with a single default StorageClass ("standard") backed by
// the rancher.io/local-path provisioner. The Kafka chart uses per-zone classes
// named "local-storage-<zone>" (e.g. local-storage-alpha). This function reads
// the actual node topology labels and creates those classes on demand.
//
// Safe to call on every deploy — kubectl apply is idempotent.
func setupKindStorageClasses(ctx context.Context) error {
	fmt.Println("    - Ensuring zone StorageClasses exist for Kind cluster...")

	// ── 1. Collect topology zones from actual node labels ─────────────────────
	out, err := exec.CommandContext(ctx,
		"kubectl", "get", "nodes",
		"-o", `jsonpath={range .items[*]}{.metadata.labels.topology\.kubernetes\.io/zone}{"\n"}{end}`,
	).Output()
	if err != nil {
		return fmt.Errorf("failed to list node zones: %w", err)
	}

	seen := map[string]bool{}
	var zones []string
	for _, z := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		z = strings.TrimSpace(z)
		if z != "" && !seen[z] {
			seen[z] = true
			zones = append(zones, z)
		}
	}

	if len(zones) == 0 {
		fmt.Println("    ⚠️  No topology.kubernetes.io/zone labels found on nodes — skipping StorageClass creation")
		return nil
	}

	fmt.Printf("    - Detected zones: %s\n", strings.Join(zones, ", "))

	// ── 2. Create one StorageClass per zone ───────────────────────────────────
	// WaitForFirstConsumer ensures the PV is provisioned on the same node where
	// the pod gets scheduled, which is essential for local storage on Kind.
	for _, zone := range zones {
		scName := "local-storage-" + zone
		scYaml := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
  labels:
    app.kubernetes.io/managed-by: kates
    topology.kubernetes.io/zone: %s
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: false
`, scName, zone)

		if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, scYaml); err != nil {
			return fmt.Errorf("failed to create StorageClass %s: %w", scName, err)
		}
		fmt.Printf("    ✅ StorageClass %q ready\n", scName)
	}

	return nil
}
