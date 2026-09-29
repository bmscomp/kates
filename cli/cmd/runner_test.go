package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bmscomp/kates/cli/internal/podrun"
)

// RunInput hands back stdout as written; Run trims it, which callers that
// parse a version or a name rely on.
func TestProcRunner_RunInputKeepsStdoutRunTrimsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses cat and printf")
	}
	const props = "security.protocol=SASL_PLAINTEXT\nsasl.mechanism=SCRAM-SHA-512\n"
	out, err := defaultRunner.RunInput(context.Background(), props, "cat")
	if err != nil || out != props {
		t.Errorf("RunInput = (%q, %v), want the input back as written", out, err)
	}
	out, err = defaultRunner.Run(context.Background(), "printf", "  v1.2.3\\n")
	if err != nil || out != "v1.2.3" {
		t.Errorf("Run = (%q, %v), want trimmed stdout", out, err)
	}
}

// podrun.WriteFile through the runner kates migrate uses. It compares tee's
// echo with what it wrote, and the runner used to trim that echo, so every
// client.properties came back a byte short ("wrote 209 bytes, expected 210")
// and `kates migrate verify` failed before producing a record.
func TestWriteFileThroughTheMigrateRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the kubectl stub is a shell script")
	}
	dir := t.TempDir()
	// kubectl exec … -- argv: run argv here, with the pod standing in for
	// this machine.
	stub := "#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	path := filepath.Join(dir, "client.properties")
	content := podrun.SASLProperties("SCRAM-SHA-512", "kates-mm2", "secret")
	pod := podrun.Pod{Namespace: "kafka", Name: "kates-migrate-m391-431-tgt"}
	if err := podrun.WriteFile(context.Background(), defaultRunner, pod, path, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != content {
		t.Errorf("file holds %q (%v), want %q", got, err, content)
	}
}
