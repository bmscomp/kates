package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Loading kates must not reach a cluster. The Connect commands used to find
// their namespace in init(), so every invocation, --help and shell completion
// included, ran `kubectl get kafkaconnect -A` with no time limit; with an
// unreachable cluster in the kubeconfig, `kates version` waited minutes for
// it. init() has run by the time a test in this process starts, so these run
// the test binary again as kates, with a kubectl stub first on PATH that logs
// every call it gets.

// TestHelperRunKates is kates itself when runKates starts this binary.
func TestHelperRunKates(t *testing.T) {
	if os.Getenv("KATES_TEST_RUN_AS_CLI") != "1" {
		t.Skip("runs only as runKates' subprocess")
	}
	rootCmd.SetArgs(strings.Fields(os.Getenv("KATES_TEST_ARGS")))
	Execute()
	os.Exit(0)
}

// runKates runs kates with args and returns the kubectl calls it made, one
// line of arguments each. The stub answers the all-namespaces lookup with
// "connect-ns" and everything else with nothing.
func runKates(t *testing.T, args ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the kubectl stub is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "kubectl-calls")
	stub := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + calls + "'\n" +
		"case \" $* \" in *' -A '*) echo connect-ns ;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunKates$")
	cmd.Env = append(os.Environ(),
		"KATES_TEST_RUN_AS_CLI=1",
		"KATES_TEST_ARGS="+strings.Join(args, " "),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KATES_CONNECT_NS=",
		"KATES_KAFKA_NS=",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kates %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	data, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestLoadingKatesRunsNoKubectl(t *testing.T) {
	if calls := runKates(t, "--help"); len(calls) != 0 {
		t.Errorf("kates --help ran kubectl:\n%s", strings.Join(calls, "\n"))
	}
}

func TestConnectFindsItsNamespaceWhenItRuns(t *testing.T) {
	calls := runKates(t, "kafka", "connect", "status")
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "get kafkaconnect -A") {
		t.Errorf("connect status did not look for the KafkaConnect's namespace:\n%s", joined)
	}
	if !strings.Contains(joined, "get kafkaconnect -n connect-ns") {
		t.Errorf("connect status did not use the namespace it found:\n%s", joined)
	}
}

func TestConnectNamespaceFlagSkipsTheLookup(t *testing.T) {
	calls := runKates(t, "kafka", "connect", "status", "-n", "chosen")
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, " -A") {
		t.Errorf("connect status looked the namespace up although -n named it:\n%s", joined)
	}
	if !strings.Contains(joined, "get kafkaconnect -n chosen") {
		t.Errorf("connect status did not use -n:\n%s", joined)
	}
}
