package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type PerformanceProfile struct {
	Name       string  `json:"name"`
	SavedAt    string  `json:"savedAt"`
	TestType   string  `json:"testType"`
	RunID      string  `json:"runId"`
	Throughput float64 `json:"throughputRecPerSec"`
	P50Ms      float64 `json:"p50LatencyMs"`
	P95Ms      float64 `json:"p95LatencyMs"`
	P99Ms      float64 `json:"p99LatencyMs"`
	AvgMs      float64 `json:"avgLatencyMs"`
	ErrorRate  float64 `json:"errorRate"`
	Records    float64 `json:"records"`
	Brokers    int     `json:"brokers,omitempty"`
	Partitions int     `json:"partitions,omitempty"`
}

var (
	profileMaxRegression float64

	profTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	profUpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E")).
			Bold(true)

	profDownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	profDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Save, compare, and assert named performance profiles",
	Long: `Manage named performance snapshots for tracking changes across
deploys, upgrades, or configuration shifts.`,
}

var profileSaveCmd = &cobra.Command{
	Use:   "save <name> <run-id>",
	Short: "Save a test run as a named performance profile",
	Example: `  kates profile save production-v3.2 abc123
  kates profile save pre-upgrade abc123`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, runID := args[0], args[1]

		run, err := apiClient.GetTest(context.Background(), runID)
		if err != nil {
			return err
		}

		profile := PerformanceProfile{
			Name:     name,
			SavedAt:  time.Now().Format(time.RFC3339),
			TestType: run.TestType,
			RunID:    runID,
		}

		if len(run.Results) > 0 {
			for _, r := range run.Results {
				profile.Throughput += r.ThroughputRecordsPerSec
				profile.P50Ms += r.P50LatencyMs
				profile.P95Ms += r.P95LatencyMs
				profile.P99Ms += r.P99LatencyMs
				profile.AvgMs += r.AvgLatencyMs
				profile.Records += r.RecordsSent
			}
			n := float64(len(run.Results))
			profile.Throughput /= n
			profile.P50Ms /= n
			profile.P95Ms /= n
			profile.P99Ms /= n
			profile.AvgMs /= n
		}

		if err := saveProfile(profile); err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Profile %q saved (run: %s, type: %s)", name, runID, run.TestType))
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved performance profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		profiles, err := loadAllProfiles()
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			output.Hint("No profiles saved yet. Use 'kates profile save <name> <run-id>'")
			return nil
		}

		fmt.Println(profTitleStyle.Width(70).Render("  Saved Profiles"))
		fmt.Println()

		fmt.Printf("  %-22s %-10s %-12s %-10s %-10s %s\n",
			profDimStyle.Render("Name"),
			profDimStyle.Render("Type"),
			profDimStyle.Render("Throughput"),
			profDimStyle.Render("P99"),
			profDimStyle.Render("Saved"),
			profDimStyle.Render("Run"),
		)
		fmt.Println("  " + strings.Repeat("─", 68))

		for _, p := range profiles {
			savedDate := p.SavedAt
			if t, err := time.Parse(time.RFC3339, p.SavedAt); err == nil {
				savedDate = t.Format("Jan 02")
			}
			fmt.Printf("  %-22s %-10s %-12s %-10s %-10s %s\n",
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4")).Render(p.Name),
				p.TestType,
				profUpStyle.Render(fmtAdvisorNum(p.Throughput)+" rec/s"),
				lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(fmt.Sprintf("%.0fms", p.P99Ms)),
				profDimStyle.Render(savedDate),
				profDimStyle.Render(p.RunID[:minLen(len(p.RunID), 8)]),
			)
		}
		fmt.Println()
		return nil
	},
}

var profileCompareCmd = &cobra.Command{
	Use:   "compare <name1> <name2>",
	Short: "Compare two performance profiles side by side",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		p1, err := loadProfile(args[0])
		if err != nil {
			return fmt.Errorf("profile %q: %w", args[0], err)
		}
		p2, err := loadProfile(args[1])
		if err != nil {
			return fmt.Errorf("profile %q: %w", args[1], err)
		}

		fmt.Println(profTitleStyle.Width(60).Render(
			fmt.Sprintf("  %s  vs  %s", p1.Name, p2.Name),
		))
		fmt.Println()

		fmt.Printf("  %-18s %-16s %-16s %s\n",
			profDimStyle.Render("Metric"),
			profDimStyle.Render(p1.Name),
			profDimStyle.Render(p2.Name),
			profDimStyle.Render("Delta"),
		)
		fmt.Println("  " + strings.Repeat("─", 56))

		printProfileRow("Throughput", p1.Throughput, p2.Throughput, " rec/s", true)
		printProfileRow("Avg Latency", p1.AvgMs, p2.AvgMs, " ms", false)
		printProfileRow("P50 Latency", p1.P50Ms, p2.P50Ms, " ms", false)
		printProfileRow("P95 Latency", p1.P95Ms, p2.P95Ms, " ms", false)
		printProfileRow("P99 Latency", p1.P99Ms, p2.P99Ms, " ms", false)

		fmt.Println()
		return nil
	},
}

var profileAssertCmd = &cobra.Command{
	Use:   "assert <name> <run-id>",
	Short: "Assert a run doesn't regress against a profile",
	Long: `Compare a test run against a saved profile and exit non-zero
if throughput regression exceeds the threshold.`,
	Example: `  kates profile assert production-v3.2 abc123 --max-regression 10`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile, err := loadProfile(args[0])
		if err != nil {
			return err
		}

		run, err := apiClient.GetTest(context.Background(), args[1])
		if err != nil {
			return err
		}

		var throughput, p99 float64
		if len(run.Results) > 0 {
			for _, r := range run.Results {
				throughput += r.ThroughputRecordsPerSec
				p99 += r.P99LatencyMs
			}
			throughput /= float64(len(run.Results))
			p99 /= float64(len(run.Results))
		}

		throughputDelta := ((throughput - profile.Throughput) / profile.Throughput) * 100
		p99Delta := ((p99 - profile.P99Ms) / profile.P99Ms) * 100

		fmt.Println(profTitleStyle.Width(60).Render("  Profile Assert — " + profile.Name))
		fmt.Println()
		printProfileRow("Throughput", profile.Throughput, throughput, " rec/s", true)
		printProfileRow("P99 Latency", profile.P99Ms, p99, " ms", false)
		fmt.Println()

		if throughputDelta < -profileMaxRegression {
			output.Error(fmt.Sprintf("REGRESSION: throughput dropped %.1f%% (threshold: %.0f%%)", math.Abs(throughputDelta), profileMaxRegression))
			return cmdErr("regression detected")
		}
		if p99Delta > profileMaxRegression*2 {
			output.Warn(fmt.Sprintf("P99 latency increased %.1f%% — monitor closely", p99Delta))
		}

		output.Success(fmt.Sprintf("PASS: within %.0f%% regression threshold", profileMaxRegression))
		return nil
	},
}

func printProfileRow(label string, a, b float64, unit string, higherIsBetter bool) {
	delta := ((b - a) / a) * 100
	if a == 0 {
		delta = 0
	}
	var changeStr string
	if delta > 1 {
		if higherIsBetter {
			changeStr = profUpStyle.Render(fmt.Sprintf("▲%.1f%%", delta))
		} else {
			changeStr = profDownStyle.Render(fmt.Sprintf("▲%.1f%%", delta))
		}
	} else if delta < -1 {
		if higherIsBetter {
			changeStr = profDownStyle.Render(fmt.Sprintf("▼%.1f%%", math.Abs(delta)))
		} else {
			changeStr = profUpStyle.Render(fmt.Sprintf("▼%.1f%%", math.Abs(delta)))
		}
	} else {
		changeStr = profDimStyle.Render("≈")
	}

	fmt.Printf("  %-18s %-16s %-16s %s\n", label,
		fmtAdvisorNum(a)+unit, fmtAdvisorNum(b)+unit, changeStr)
}

func profileDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kates", "profiles")
}

func saveProfile(p PerformanceProfile) error {
	dir := profileDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, p.Name+".json"), data, 0644)
}

func loadProfile(name string) (*PerformanceProfile, error) {
	data, err := os.ReadFile(filepath.Join(profileDir(), name+".json"))
	if err != nil {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	var p PerformanceProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func loadAllProfiles() ([]PerformanceProfile, error) {
	dir := profileDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var profiles []PerformanceProfile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p PerformanceProfile
		if json.Unmarshal(data, &p) == nil {
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	profileAssertCmd.Flags().Float64Var(&profileMaxRegression, "max-regression", 10, "Max throughput regression percentage")
	profileCmd.AddCommand(profileSaveCmd, profileListCmd, profileCompareCmd, profileAssertCmd)
	rootCmd.AddCommand(profileCmd)
}
