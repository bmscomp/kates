package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this guards: `make cli-install` / `kates upgrade` write
// /usr/local/bin/kates, print a version read from that same path, and say
// nothing about the /opt/homebrew/bin/kates that the shell actually runs.
// Every new command then looks missing.
func TestCheckPathShadow(t *testing.T) {
	ident := func(s string) (string, error) { return s, nil }

	tests := []struct {
		name        string
		installPath string
		lookPath    func(string) (string, error)
		resolve     func(string) (string, error)
		wantShadow  string
		wantNotOn   bool
		wantNil     bool
	}{
		{
			name:        "same file",
			installPath: "/usr/local/bin/kates",
			lookPath:    func(string) (string, error) { return "/usr/local/bin/kates", nil },
			resolve:     ident,
			wantNil:     true,
		},
		{
			name:        "symlink to the same file is not a rival",
			installPath: "/usr/local/bin/kates",
			lookPath:    func(string) (string, error) { return "/opt/homebrew/bin/kates", nil },
			resolve: func(p string) (string, error) {
				if p == "/opt/homebrew/bin/kates" {
					return "/usr/local/bin/kates", nil
				}
				return p, nil
			},
			wantNil: true,
		},
		{
			name:        "another kates wins on PATH",
			installPath: "/usr/local/bin/kates",
			lookPath:    func(string) (string, error) { return "/opt/homebrew/bin/kates", nil },
			resolve:     ident,
			wantShadow:  "/opt/homebrew/bin/kates",
		},
		{
			name:        "nothing on PATH",
			installPath: "/usr/local/bin/kates",
			lookPath:    func(string) (string, error) { return "", errors.New("not found") },
			resolve:     ident,
			wantNotOn:   true,
		},
		{
			name:        "unresolvable paths still compare by name",
			installPath: "/usr/local/bin/kates",
			lookPath:    func(string) (string, error) { return "/usr/local/bin/kates", nil },
			resolve:     func(string) (string, error) { return "", errors.New("no such file") },
			wantNil:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPathShadow(tc.installPath, tc.lookPath, tc.resolve)
			switch {
			case tc.wantNil:
				if got != nil {
					t.Fatalf("expected no warning, got %+v", got)
				}
			case tc.wantNotOn:
				if got == nil || !got.NotOnPath {
					t.Fatalf("expected a not-on-PATH warning, got %+v", got)
				}
			default:
				if got == nil || got.Resolved != tc.wantShadow {
					t.Fatalf("expected shadow %q, got %+v", tc.wantShadow, got)
				}
			}
		})
	}
}

// Installing over a Homebrew symlink writes into the Cellar: the formula's
// own binary is replaced, and its next upgrade silently undoes the install.
func TestCheckForeignSymlink(t *testing.T) {
	dir := t.TempDir()
	cellar := filepath.Join(dir, "Cellar", "kates", "1.21.0", "bin")
	brewBin := filepath.Join(dir, "homebrew", "bin")
	localBin := filepath.Join(dir, "local", "bin")
	for _, d := range []string{cellar, brewBin, localBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cellarBin := filepath.Join(cellar, "kates")
	if err := os.WriteFile(cellarBin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	brewLink := filepath.Join(brewBin, "kates")
	if err := os.Symlink(cellarBin, brewLink); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(localBin, "kates")
	if err := os.WriteFile(plain, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(localBin, "kates-link")
	if err := os.Symlink(plain, sibling); err != nil {
		t.Fatal(err)
	}

	t.Run("symlink into a Cellar is refused and named", func(t *testing.T) {
		fs := checkForeignSymlink(brewLink, os.Lstat, filepath.EvalSymlinks)
		if fs == nil {
			t.Fatal("expected a foreign symlink")
		}
		if fs.Manager != "Homebrew" {
			t.Errorf("manager: %q", fs.Manager)
		}
		if !strings.HasSuffix(fs.Target, filepath.Join("1.21.0", "bin", "kates")) {
			t.Errorf("target: %s", fs.Target)
		}
	})

	t.Run("a plain file is fine", func(t *testing.T) {
		if fs := checkForeignSymlink(plain, os.Lstat, filepath.EvalSymlinks); fs != nil {
			t.Errorf("expected no warning, got %+v", fs)
		}
	})

	t.Run("a symlink within the same directory is fine", func(t *testing.T) {
		if fs := checkForeignSymlink(sibling, os.Lstat, filepath.EvalSymlinks); fs != nil {
			t.Errorf("expected no warning, got %+v", fs)
		}
	})

	t.Run("a path that does not exist yet is fine", func(t *testing.T) {
		if fs := checkForeignSymlink(filepath.Join(localBin, "absent"), os.Lstat, filepath.EvalSymlinks); fs != nil {
			t.Errorf("expected no warning, got %+v", fs)
		}
	})

	t.Run("the refusal explains both ways out", func(t *testing.T) {
		err := func() error {
			// guardForeignSymlink reads the real filesystem; point it at the
			// Homebrew-shaped link built above.
			return guardForeignSymlinkAt(brewLink)
		}()
		if err == nil {
			t.Fatal("expected a refusal")
		}
		for _, want := range []string{"Homebrew owns", "--install-dir ~/.local/bin", "brew uninstall kates"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal misses %q:\n%s", want, err)
			}
		}
	})
}

// runningBinaryPath answers "which file am I?" — `kates version` prints it
// so a stale copy is visible without hunting through PATH.
func TestRunningBinaryPath(t *testing.T) {
	p := runningBinaryPath()
	if p == "" || p == "unknown" {
		t.Fatalf("expected the test binary's path, got %q", p)
	}
	if !strings.Contains(p, "/") {
		t.Errorf("expected an absolute path, got %q", p)
	}
}
