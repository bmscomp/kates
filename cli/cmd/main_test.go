package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain runs the package's tests with HOME pointed at an empty directory.
//
// Commands under test read and write files under the home directory, and not
// every test isolates it. The deploy tests did not: their stubbed kubectl
// answers every Secret read, so each run stored that fake API key in the
// current context of the developer's own ~/.kates.yaml, and deleted
// ~/.kube/cache/discovery and ~/.cache/helm. A test that needs a home of its
// own still sets HOME itself.
//
// It also lowers the PBKDF2 work factor of key-source digests to the least
// keySourceMatches accepts: the tests store and check many keys, and at the
// production figure each digest takes a noticeable fraction of a second.
// TestSecretKeySource checks the production figure itself.
func TestMain(m *testing.M) {
	keySourceIterations = minKeySourceIterations
	home, err := os.MkdirTemp("", "kates-cmd-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	// Deploy writes under XDG_STATE_HOME outside a checkout, and KATES_CHECKOUT
	// moves the chart root: neither may come from the developer's shell.
	os.Unsetenv("XDG_STATE_HOME")
	os.Unsetenv("KATES_CHECKOUT")
	// A developer's own KATES_PROFILES would refuse the commands tests run. As
	// runKates' kates, the binary keeps the one the test passes.
	if os.Getenv("KATES_TEST_RUN_AS_CLI") != "1" {
		os.Unsetenv("KATES_PROFILES")
	}
	// Namespace labels come from ./.labels and KATES_NAMESPACE_LABELS: a
	// stray file in cli/cmd or the developer's variable would label what the
	// tests expect bare. An empty labels file stands in for ./.labels; the
	// tests of the default path set namespaceLabelsFile back to "".
	os.Unsetenv(namespaceLabelsEnv)
	namespaceLabelsFile = filepath.Join(home, "empty.labels")
	if err := os.WriteFile(namespaceLabelsFile, nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
