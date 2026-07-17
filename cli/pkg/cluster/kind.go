package cluster

import (
	"fmt"
	"os"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// DefaultKindClusterName matches the Makefile's CLUSTER_NAME.
const DefaultKindClusterName = "panda"

// DefaultKindConfig is the 3-availability-zone topology: alpha (control-plane),
// sigma and gamma (workers), each labelled topology.kubernetes.io/zone.
const DefaultKindConfig = "config/cluster.yaml"

// KindZones are the zones DefaultKindConfig creates. Exported so the UI can
// state what it is about to build without re-parsing the YAML.
var KindZones = []string{"alpha", "sigma", "gamma"}

// KindOptions configures CreateKind.
type KindOptions struct {
	Name       string
	ConfigPath string
	// Progress, if set, is called with human-readable step descriptions.
	Progress func(string)
}

func (o *KindOptions) withDefaults() {
	if o.Name == "" {
		o.Name = DefaultKindClusterName
	}
	if o.ConfigPath == "" {
		o.ConfigPath = DefaultKindConfig
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
}

// ConfigExists reports whether the topology config is readable from here.
//
// DefaultKindConfig is repo-relative, so an installed binary run from anywhere
// but the repo root cannot see it. Checking up front turns a confusing failure
// deep inside `kind create` into an explanation the caller can act on.
func ConfigExists(path string) bool {
	if path == "" {
		path = DefaultKindConfig
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// KindClusterExists reports whether a Kind cluster of this name is already
// present, so we offer to create rather than clobber.
func KindClusterExists(exec detect.CommandExecutor, name string) bool {
	out, err := exec.Exec("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// CreateKind creates the 3-AZ Kind cluster and makes it actually usable.
//
// Creating the cluster is not sufficient on its own: Kind taints the
// control-plane node NoSchedule, and in this topology that node IS one of the
// three zones (alpha). Left tainted, nothing schedules there and the cluster
// silently has two usable zones instead of three — which fails the 3-AZ
// requirement every Kafka pool in this repo is built on. So the untaint is part
// of creation, not an optional extra.
//
// Zone StorageClasses are deliberately NOT applied here: the deploy path
// already bootstraps them immediately after preflight (see setupKindStorageClasses).
func CreateKind(exec detect.CommandExecutor, opts KindOptions) error {
	opts.withDefaults()

	opts.Progress(fmt.Sprintf("Creating Kind cluster %q with zones %s", opts.Name, strings.Join(KindZones, ", ")))
	if out, err := exec.Exec("kind", "create", "cluster",
		"--config", opts.ConfigPath,
		"--name", opts.Name); err != nil {
		return fmt.Errorf("kind create cluster: %w: %s", err, strings.TrimSpace(out))
	}

	opts.Progress("Waiting for all nodes to become Ready")
	if out, err := exec.Exec("kubectl", "wait", "--for=condition=Ready",
		"node", "--all", "--timeout=180s"); err != nil {
		return fmt.Errorf("nodes did not become ready: %w: %s", err, strings.TrimSpace(out))
	}

	opts.Progress("Untainting control-plane so all three zones can schedule")
	if err := untaintAllNodes(exec); err != nil {
		return err
	}

	opts.Progress(fmt.Sprintf("Kind cluster %q is ready", opts.Name))
	return nil
}

// untaintAllNodes removes the control-plane NoSchedule taint from every node.
//
// Removing a taint that is not present exits non-zero ("taint not found"), which
// is a success for our purposes — we only care that no node is left tainted. So
// per-node errors are tolerated and only a failure to enumerate nodes is fatal.
func untaintAllNodes(exec detect.CommandExecutor) error {
	out, err := exec.Exec("kubectl", "get", "nodes", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return fmt.Errorf("listing nodes to untaint: %w", err)
	}
	for _, node := range strings.Fields(out) {
		_, _ = exec.Exec("kubectl", "taint", "nodes", node,
			"node-role.kubernetes.io/control-plane:NoSchedule-")
	}
	return nil
}
