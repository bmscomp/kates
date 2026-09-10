package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	upgradeSourceDir  string
	upgradeInstallDir string
	upgradeDryRun     bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Build from source and install a new version of the Kates CLI",
	Long: `Build the Kates CLI from source and install it on this machine.

Replaces 'make cli-install'. Automatically detects the source directory,
builds an optimised binary, and copies it to the install path.

By default it installs over the kates the shell already runs (the first one
on PATH), so the upgrade takes effect where you type it. Pass --install-dir
to choose another directory.

Examples:
  kates upgrade                            # auto-detect source, install where kates resolves
  kates upgrade --source ~/codes/kates/cli # explicit source dir
  kates upgrade --install-dir ~/.local/bin # install to a custom directory (no sudo)
  kates upgrade --dry-run                  # show what would happen without doing it`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpgrade()
	},
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeSourceDir, "source", "", "Path to the CLI source directory (default: auto-detect from git)")
	upgradeCmd.Flags().StringVar(&upgradeInstallDir, "install-dir", "", "Directory to install the binary into (default: where `kates` already resolves on PATH, else /usr/local/bin)")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "Show what would happen without making changes")
	rootCmd.AddCommand(upgradeCmd)
}

// ──────────────────────────────────────────────────────────────────────────────
// Core logic
// ──────────────────────────────────────────────────────────────────────────────

func runUpgrade() error {
	start := time.Now()

	// ── 1. Resolve source directory ──────────────────────────────────────────
	srcDir, err := resolveSourceDir()
	if err != nil {
		return err
	}

	// ── 2. Resolve install path ──────────────────────────────────────────────
	installDir := resolveInstallDir()
	installPath := filepath.Join(installDir, "kates")
	needsSudo := needsSudoForPath(installDir)

	// ── 3. Capture current version for comparison ────────────────────────────
	oldVersion := captureInstalledVersion(installPath)

	// ── 4. Read git info ─────────────────────────────────────────────────────
	gitCommit := gitShort(srcDir, "rev-parse", "--short", "HEAD")
	gitBranch := gitShort(srcDir, "rev-parse", "--abbrev-ref", "HEAD")
	gitDirty := ""
	if out, _ := execOutput(srcDir, "git", "status", "--porcelain"); strings.TrimSpace(out) != "" {
		gitDirty = " (dirty)"
	}

	// ── 5. Display plan ──────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("    ╭─ Kates Upgrade ────────────────────────────────────────────────────────────╮")
	upgradeRow("Source", srcDir)
	upgradeRow("Branch", gitBranch+gitDirty)
	upgradeRow("Commit", gitCommit)
	installNote := installPath
	if upgradeInstallDir == "" {
		if found, lerr := exec.LookPath("kates"); lerr == nil && filepath.Dir(found) == installDir {
			installNote += "  (where `kates` resolves)"
		}
	}
	upgradeRow("Install", installNote)
	if needsSudo {
		upgradeRow("Sudo", "required")
	}
	if oldVersion != "" {
		upgradeRow("Current", oldVersion)
	}
	fmt.Println("    ╰────────────────────────────────────────────────────────────────────────────╯")
	fmt.Println()

	if upgradeDryRun {
		fmt.Println("    ⏸  Dry run — no changes made")
		// Worth knowing before the sudo password, not after: whether this
		// path is a package manager's, and whether installing here would
		// change what `kates` runs at all.
		if err := guardForeignSymlink(installPath); err != nil {
			output.Warn(err.Error())
		}
		printPathShadowWarning(installPath)
		return nil
	}

	// ── 6. Build ─────────────────────────────────────────────────────────────
	buildDate := time.Now().UTC().Format(time.RFC3339)
	ldflags := fmt.Sprintf("-s -w -X github.com/bmscomp/kates/cli/cmd.Version=%s -X github.com/bmscomp/kates/cli/cmd.Commit=%s -X github.com/bmscomp/kates/cli/cmd.BuildDate=%s",
		gitBranch+"-"+gitCommit, gitCommit, buildDate)

	tmpBin := filepath.Join(srcDir, "dist", "kates")
	os.MkdirAll(filepath.Join(srcDir, "dist"), 0755)

	buildCmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", tmpBin, ".")
	buildCmd.Dir = srcDir
	// Capture stderr for error reporting but don't print live
	// (go build is noisy only on failure)
	var buildStderr strings.Builder
	buildCmd.Stderr = &buildStderr

	// Spinner with elapsed time
	spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	buildStart := time.Now()
	done := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				elapsed := time.Since(buildStart).Round(time.Second)
				fmt.Printf("\r    %s  Building from source... %s", spinFrames[i%len(spinFrames)], elapsed)
				i++
				time.Sleep(150 * time.Millisecond)
			}
		}
	}()

	buildErr := buildCmd.Run()
	close(done)
	// Clear the spinner line
	fmt.Printf("\r%s\r", strings.Repeat(" ", 60))

	if buildErr != nil {
		fmt.Println("    ❌ Build failed")
		if errOut := buildStderr.String(); errOut != "" {
			fmt.Fprint(os.Stderr, errOut)
		}
		return fmt.Errorf("build failed: %w", buildErr)
	}

	// Verify the binary exists and is executable
	info, err := os.Stat(tmpBin)
	if err != nil || info.IsDir() {
		return fmt.Errorf("build produced no binary at %s", tmpBin)
	}
	fmt.Printf("    ✅ Built  %s  (%s)\n", filepath.Base(tmpBin), humanSize(info.Size()))

	// ── 7. Install ───────────────────────────────────────────────────────────
	if err := guardForeignSymlink(installPath); err != nil {
		return err
	}
	fmt.Printf("    📦 Installing to %s...\n", installPath)
	if err := installBinary(tmpBin, installPath, needsSudo); err != nil {
		return err
	}

	// ── 8. macOS: clear quarantine + codesign ────────────────────────────────
	if runtime.GOOS == "darwin" {
		macOSSign(installPath, needsSudo)
	}

	// ── 9. Verify ────────────────────────────────────────────────────────────
	newVersion := captureInstalledVersion(installPath)

	fmt.Println()
	fmt.Println("    ╭─ Upgrade Complete ─────────────────────────────────────────────────────────╮")
	if oldVersion != "" {
		upgradeRow("Previous", oldVersion)
	}
	upgradeRow("Installed", newVersion)
	upgradeRow("Elapsed", time.Since(start).Round(time.Millisecond).String())
	fmt.Println("    ╰────────────────────────────────────────────────────────────────────────────╯")
	fmt.Println()

	// ── 10. Is the file we just wrote the one `kates` runs? ──────────────────
	// Installing and verifying the same absolute path proves nothing about
	// what the shell will execute: another kates earlier on PATH keeps
	// winning, silently, and every new feature looks missing. Say so here,
	// where the answer is still in front of the user.
	printPathShadowWarning(installPath)

	return nil
}

// pathShadow describes a kates on PATH that is not the one just installed.
type pathShadow struct {
	// Resolved is the binary the shell would run, with symlinks resolved.
	Resolved string
	// NotOnPath is true when no kates at all resolves on PATH.
	NotOnPath bool
}

// checkPathShadow answers "does `kates` run the file at installPath?".
// lookPath and resolve are injected so the check is testable without
// touching the real PATH or filesystem.
func checkPathShadow(installPath string, lookPath func(string) (string, error), resolve func(string) (string, error)) *pathShadow {
	found, err := lookPath("kates")
	if err != nil || strings.TrimSpace(found) == "" {
		return &pathShadow{NotOnPath: true}
	}
	// Compare the real files: /usr/local/bin/kates and a symlink to it are
	// the same binary, and reporting them as rivals would be noise.
	a, err1 := resolve(found)
	if err1 != nil {
		a = found
	}
	b, err2 := resolve(installPath)
	if err2 != nil {
		b = installPath
	}
	if a == b {
		return nil
	}
	return &pathShadow{Resolved: a}
}

// printPathShadowWarning reports, in the terminal's own voice, that the
// upgraded binary is not the one PATH resolves to — and what to do.
func printPathShadowWarning(installPath string) {
	shadow := checkPathShadow(installPath, exec.LookPath, filepath.EvalSymlinks)
	if shadow == nil {
		return
	}
	if shadow.NotOnPath {
		output.Warn(fmt.Sprintf("%s is not on your PATH — typing `kates` will not find it.", installPath))
		output.Hint(fmt.Sprintf("Add it:  export PATH=\"%s:$PATH\"", filepath.Dir(installPath)))
		return
	}

	output.Warn(fmt.Sprintf("`kates` still runs %s, not the binary just installed.", shadow.Resolved))
	if v := captureInstalledVersion(shadow.Resolved); v != "" {
		output.Hint("That one reports: " + v)
	}
	output.Hint("It comes earlier in PATH, so every new command and flag will look missing.")
	fmt.Println()
	fmt.Println("      Install where the shell already looks:")
	fmt.Printf("        kates upgrade --install-dir %s\n", filepath.Dir(shadow.Resolved))
	fmt.Println("      or remove the stale copy:")
	fmt.Printf("        rm %s\n", shadow.Resolved)
	fmt.Println("      or put the new one first:")
	fmt.Printf("        export PATH=\"%s:$PATH\"\n", filepath.Dir(installPath))
	fmt.Println()
	fmt.Println("      Then run `hash -r` so the shell forgets the old location.")
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// resolveSourceDir finds the CLI source directory. Priority:
//  1. --source flag
//  2. Git repo root of the running binary + "/cli"
//  3. Git repo root of the cwd + "/cli"
func resolveSourceDir() (string, error) {
	if upgradeSourceDir != "" {
		abs, err := filepath.Abs(upgradeSourceDir)
		if err != nil {
			return "", fmt.Errorf("invalid source path: %w", err)
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return "", fmt.Errorf("no go.mod found in %s — is this the CLI source dir?", abs)
		}
		return abs, nil
	}

	// Try to find the repo root from the running binary's location
	exe, _ := os.Executable()
	if exe != "" {
		exeDir := filepath.Dir(exe)
		if root := gitRepoRoot(exeDir); root != "" {
			candidate := filepath.Join(root, "cli")
			if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
				return candidate, nil
			}
		}
	}

	// Try cwd
	cwd, _ := os.Getwd()
	if root := gitRepoRoot(cwd); root != "" {
		candidate := filepath.Join(root, "cli")
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf(
		"cannot auto-detect source directory\n" +
			"  Use: kates upgrade --source /path/to/kates/cli")
}

// resolveInstallDir decides where the new binary goes. With no flag it
// follows the kates the shell already runs: upgrading somewhere the shell
// does not look is the one outcome nobody wants, and /usr/local/bin loses
// to ~/.local/bin, ~/go/bin or Homebrew's bin on most PATHs.
func resolveInstallDir() string {
	if upgradeInstallDir != "" {
		abs, _ := filepath.Abs(upgradeInstallDir)
		return abs
	}
	if found, err := exec.LookPath("kates"); err == nil {
		if dir := filepath.Dir(found); dir != "" && dir != "." {
			// Not the symlink target: /opt/homebrew/bin is the directory on
			// PATH, ../Cellar/... is where the file happens to live.
			return dir
		}
	}
	return "/usr/local/bin"
}

// foreignSymlink describes an install destination that is a symlink into
// another directory — Homebrew links /opt/homebrew/bin/<x> to its Cellar,
// and `cp` onto that link overwrites the package manager's own file.
type foreignSymlink struct {
	Target  string
	Manager string // "Homebrew" when the target is in a Cellar, else ""
}

// checkForeignSymlink reports whether writing to dst would follow a symlink
// out of its own directory. lstat and resolve are injected for testing.
func checkForeignSymlink(dst string, lstat func(string) (os.FileInfo, error), resolve func(string) (string, error)) *foreignSymlink {
	info, err := lstat(dst)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, err := resolve(dst)
	if err != nil || filepath.Dir(target) == filepath.Dir(dst) {
		return nil
	}
	fs := &foreignSymlink{Target: target}
	if strings.Contains(target, "/Cellar/") {
		fs.Manager = "Homebrew"
	}
	return fs
}

// guardForeignSymlink refuses an install that would clobber a
// package-manager-owned binary, and says how to proceed instead.
func guardForeignSymlink(installPath string) error {
	return guardForeignSymlinkAt(installPath)
}

// guardForeignSymlinkAt is guardForeignSymlink against the real filesystem,
// separated so a test can point it at a Homebrew-shaped layout in a tempdir.
func guardForeignSymlinkAt(installPath string) error {
	fs := checkForeignSymlink(installPath, os.Lstat, filepath.EvalSymlinks)
	if fs == nil {
		return nil
	}
	owner := "another package"
	if fs.Manager != "" {
		owner = fs.Manager
	}
	msg := fmt.Sprintf("%s is a symlink to %s, which %s owns.\n", installPath, fs.Target, owner) +
		"  Installing here would overwrite that package's binary, and its next upgrade would silently undo yours.\n" +
		"  Install somewhere you own instead, e.g.:\n" +
		"    kates upgrade --install-dir ~/.local/bin"
	if fs.Manager == "Homebrew" {
		msg += "\n  Or remove the formula first:  brew uninstall kates"
	}
	return errors.New(msg)
}

func needsSudoForPath(dir string) bool {
	testFile := filepath.Join(dir, ".kates-write-test")
	f, err := os.Create(testFile)
	if err != nil {
		return true
	}
	f.Close()
	os.Remove(testFile)
	return false
}

func installBinary(src, dst string, useSudo bool) error {
	os.MkdirAll(filepath.Dir(dst), 0755)

	if useSudo {
		cmd := exec.Command("sudo", "cp", src, dst)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("install failed (sudo cp): %w\n  Try: kates upgrade --install-dir ~/.local/bin", err)
		}
	} else {
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read built binary: %w", err)
		}
		if err := os.WriteFile(dst, data, 0755); err != nil {
			return fmt.Errorf("write to %s: %w", dst, err)
		}
	}
	return nil
}

func macOSSign(path string, useSudo bool) {
	run := func(name string, args ...string) {
		if useSudo {
			args = append([]string{name}, args...)
			name = "sudo"
		}
		cmd := exec.Command(name, args...)
		cmd.Stdin = os.Stdin
		cmd.Run() // best-effort, ignore errors
	}

	run("xattr", "-dr", "com.apple.provenance", path)
	run("xattr", "-dr", "com.apple.quarantine", path)
	run("codesign", "-f", "-s", "-", path)
}

func captureInstalledVersion(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	out, err := exec.Command(path, "version").CombinedOutput()
	if err != nil {
		return ""
	}
	// Parse the version output — look for lines like "Kates CLI  dev" or "Kates CLI  main-abc1234"
	var cli, commit string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "kates cli"):
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				cli = parts[len(parts)-1]
			}
		case strings.Contains(lower, "commit"):
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				commit = parts[len(parts)-1]
			}
		}
	}
	if cli == "" {
		return ""
	}
	if commit != "" && commit != "unknown" {
		return cli + " (" + commit + ")"
	}
	return cli
}

func gitRepoRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitShort(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func execOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func upgradeRow(label, value string) {
	content := fmt.Sprintf("  %-12s %s", label, value)
	pad := 76 - len(content)
	if pad < 0 {
		content = content[:73] + "..."
		pad = 0
	}
	fmt.Printf("    │%s%s│\n", content, strings.Repeat(" ", pad))
}

func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
