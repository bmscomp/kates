package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/pkg/theme"
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

Examples:
  kates upgrade                            # auto-detect source, install to /usr/local/bin
  kates upgrade --source ~/codes/kates/cli # explicit source dir
  kates upgrade --install-dir ~/.local/bin # install to a custom directory (no sudo)
  kates upgrade --dry-run                  # show what would happen without doing it`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpgrade()
	},
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeSourceDir, "source", "", "Path to the CLI source directory (default: auto-detect from git)")
	upgradeCmd.Flags().StringVar(&upgradeInstallDir, "install-dir", "", "Directory to install the binary into (default: /usr/local/bin)")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "Show what would happen without making changes")
	rootCmd.AddCommand(upgradeCmd)
}

// ──────────────────────────────────────────────────────────────────────────────
// Styles
// ──────────────────────────────────────────────────────────────────────────────

var (
	upgBanner = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(theme.Accent).
			Padding(0, 1)

	upgSectionTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Info)

	upgLabel = lipgloss.NewStyle().
			Foreground(theme.Muted).
			Width(14)

	upgValue = lipgloss.NewStyle().
			Foreground(theme.Text).
			Bold(true)

	upgDim = lipgloss.NewStyle().
		Foreground(theme.Muted)

	upgSuccess = lipgloss.NewStyle().
			Foreground(theme.Success).
			Bold(true)

	upgWarn = lipgloss.NewStyle().
		Foreground(theme.Warning)

	upgError = lipgloss.NewStyle().
			Foreground(theme.Error).
			Bold(true)

	upgAccent = lipgloss.NewStyle().
			Foreground(theme.Accent)

	upgHighlight = lipgloss.NewStyle().
			Foreground(theme.Highlight).
			Bold(true)

	upgBorder = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(theme.Subtle).
			Padding(0, 1)

	upgCompleteBorder = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(theme.Success).
				Padding(0, 1)
)

// ──────────────────────────────────────────────────────────────────────────────
// Core logic
// ──────────────────────────────────────────────────────────────────────────────

func runUpgrade() error {
	start := time.Now()

	// ── Banner ───────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("  " + upgBanner.Render(" ⬆ Kates Upgrade "))
	fmt.Println()

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
		gitDirty = upgWarn.Render(" (dirty)")
	}

	// ── 5. Display plan ──────────────────────────────────────────────────────
	planRows := []string{
		upgStyledRow("Source", srcDir),
		upgStyledRow("Branch", upgAccent.Render(gitBranch)+gitDirty),
		upgStyledRow("Commit", upgAccent.Render(gitCommit)),
		upgStyledRow("Install", installPath),
	}
	if needsSudo {
		planRows = append(planRows, upgStyledRow("Sudo", upgWarn.Render("required")))
	}
	if oldVersion != "" {
		planRows = append(planRows, upgStyledRow("Current", upgDim.Render(oldVersion)))
	}

	planContent := strings.Join(planRows, "\n")
	planBox := upgBorder.Render(planContent)
	for _, line := range strings.Split(planBox, "\n") {
		fmt.Println("  " + line)
	}
	fmt.Println()

	if upgradeDryRun {
		fmt.Println("  " + upgDim.Render("⏸  Dry run — no changes made"))
		return nil
	}

	// ── 6. Build ─────────────────────────────────────────────────────────────
	fmt.Println("  " + upgSectionTitle.Render("Building"))
	fmt.Println("  " + upgDim.Render(strings.Repeat("─", 40)))

	buildDate := time.Now().UTC().Format(time.RFC3339)
	ldflags := fmt.Sprintf("-s -w -X github.com/klster/kates-cli/cmd.Version=%s -X github.com/klster/kates-cli/cmd.Commit=%s -X github.com/klster/kates-cli/cmd.BuildDate=%s",
		gitBranch+"-"+gitCommit, gitCommit, buildDate)

	tmpBin := filepath.Join(srcDir, "dist", "kates")
	os.MkdirAll(filepath.Join(srcDir, "dist"), 0755)

	fmt.Printf("  %s Compiling %s/%s for %s/%s...\n",
		upgDim.Render("⚙"),
		upgAccent.Render(gitBranch),
		upgAccent.Render(gitCommit),
		upgDim.Render(runtime.GOOS),
		upgDim.Render(runtime.GOARCH))

	buildStart := time.Now()
	buildCmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", tmpBin, ".")
	buildCmd.Dir = srcDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Println("  " + upgError.Render("✖ Build failed"))
		return fmt.Errorf("build failed: %w", err)
	}
	buildElapsed := time.Since(buildStart).Round(time.Millisecond)

	// Verify the binary exists and is executable
	info, err := os.Stat(tmpBin)
	if err != nil || info.IsDir() {
		return fmt.Errorf("build produced no binary at %s", tmpBin)
	}
	fmt.Printf("  %s Built %s  %s  %s\n",
		upgSuccess.Render("✔"),
		upgValue.Render("kates"),
		upgDim.Render(humanSize(info.Size())),
		upgDim.Render(buildElapsed.String()))

	// ── 7. Install ───────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("  " + upgSectionTitle.Render("Installing"))
	fmt.Println("  " + upgDim.Render(strings.Repeat("─", 40)))

	fmt.Printf("  %s Copying to %s\n",
		upgDim.Render("📦"),
		upgAccent.Render(installPath))

	if err := installBinary(tmpBin, installPath, needsSudo); err != nil {
		fmt.Println("  " + upgError.Render("✖ Installation failed"))
		return err
	}

	// ── 8. macOS: clear quarantine + codesign ────────────────────────────────
	if runtime.GOOS == "darwin" {
		fmt.Printf("  %s Signing binary (macOS)...\n", upgDim.Render("🔏"))
		macOSSign(installPath, needsSudo)
	}

	fmt.Printf("  %s Installed successfully\n", upgSuccess.Render("✔"))

	// ── 9. Summary ───────────────────────────────────────────────────────────
	newVersion := captureInstalledVersion(installPath)
	elapsed := time.Since(start).Round(time.Millisecond)

	fmt.Println()

	summaryRows := []string{}
	if oldVersion != "" {
		summaryRows = append(summaryRows, upgStyledRow("Previous", upgDim.Render(oldVersion)))
	}
	summaryRows = append(summaryRows,
		upgStyledRow("Installed", upgHighlight.Render(newVersion)),
		upgStyledRow("Binary", installPath),
		upgStyledRow("Elapsed", upgAccent.Render(elapsed.String())),
	)

	summaryContent := strings.Join(summaryRows, "\n")
	summaryBox := upgCompleteBorder.Render(summaryContent)

	fmt.Println("  " + upgSuccess.Render("✔ Upgrade Complete"))
	fmt.Println()
	for _, line := range strings.Split(summaryBox, "\n") {
		fmt.Println("  " + line)
	}

	fmt.Println()
	fmt.Println("  " + upgDim.Render("Verify:"))
	fmt.Println("  " + upgAccent.Render("  $ kates version"))
	fmt.Println()

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// upgStyledRow renders a styled label: value row for the upgrade panels.
func upgStyledRow(label, value string) string {
	return upgLabel.Render(label) + " " + value
}

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

func resolveInstallDir() string {
	if upgradeInstallDir != "" {
		abs, _ := filepath.Abs(upgradeInstallDir)
		return abs
	}
	return "/usr/local/bin"
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
