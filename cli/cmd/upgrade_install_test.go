package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// installBinary used to write the destination in place, which Linux refuses
// for the running kates (ETXTBSY). It now writes dst.tmp and renames it over
// dst, leaving no temporary file behind.
func TestInstallBinary_ReplacesByRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "built")
	if err := os.WriteFile(src, []byte("new binary"), 0755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "bin", "kates")
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	// Hold the old file open, as a running executable is: the rename must
	// still go through, and the open file must keep its old contents.
	old, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	if err := installBinary(src, dst, false); err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "new binary" {
		t.Errorf("dst = %q, %v; want the new binary", got, err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Errorf("dst mode = %v, %v; want 0755", info.Mode(), err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("%s.tmp left behind (stat: %v)", dst, err)
	}
	stale := make([]byte, 10)
	if n, _ := old.Read(stale); string(stale[:n]) != "old binary" {
		t.Errorf("the open old file reads %q; the rename must not write through it", stale[:n])
	}
}
