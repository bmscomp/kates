package cmd

import (
	"fmt"
	"os"
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
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
