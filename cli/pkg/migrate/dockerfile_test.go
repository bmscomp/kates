package migrate

import (
	"bytes"
	"os"
	"testing"
)

// The built-in Dockerfile must be the repository's: a kates outside a
// checkout would otherwise build a different legacy image.
func TestLegacyDockerfileMatchesTheRepository(t *testing.T) {
	repo, err := os.ReadFile("../../../" + LegacyDockerfileName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, LegacyDockerfile) {
		t.Fatalf("cli/pkg/migrate/legacy-kafka.Dockerfile differs from %s: cp %s cli/pkg/migrate/legacy-kafka.Dockerfile", LegacyDockerfileName, LegacyDockerfileName)
	}
}
