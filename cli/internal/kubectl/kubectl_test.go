package kubectl

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A command whose context ends returns then, even when a process it started
// still holds its output open, as the process behind a kubectl wrapper
// script can. Killing kubectl alone left Wait waiting for that pipe to close,
// so a lookup given ten seconds took as long as the other process ran.
func TestOutputReturnsWhenItsContextEnds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the kubectl stub is a shell script")
	}
	dir := t.TempDir()
	stub := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func(d time.Duration) { waitDelay = d }(waitDelay)
	waitDelay = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := New("").Output(ctx, "get", "kafkaconnect", "-A")
	if err == nil {
		t.Fatal("a command cut off by its context returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Output returned %s after its context ended, not when it ended", elapsed)
	}
}

func TestBuildArgs(t *testing.T) {
	c := &Client{Context: "test-ctx"}
	args := c.buildArgs([]string{"get", "pods"})
	if len(args) != 4 || args[0] != "--context" || args[1] != "test-ctx" {
		t.Errorf("expected --context prepended, got %v", args)
	}

	c2 := &Client{}
	args2 := c2.buildArgs([]string{"get", "pods"})
	if len(args2) != 2 {
		t.Errorf("expected no extra args, got %v", args2)
	}
}
