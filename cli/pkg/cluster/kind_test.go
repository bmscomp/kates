package cluster

import (
	"bytes"
	"os"
	"testing"
)

// The built-in topology must be the repository's: a kates outside a checkout
// would otherwise build a different cluster than `make cluster` does.
func TestKindConfigMatchesTheRepository(t *testing.T) {
	repo, err := os.ReadFile("../../../" + DefaultKindConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, KindConfig) {
		t.Fatalf("cli/pkg/cluster/kind-cluster.yaml differs from %s: cp %s cli/pkg/cluster/kind-cluster.yaml", DefaultKindConfig, DefaultKindConfig)
	}
}
