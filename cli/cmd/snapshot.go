package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type ClusterSnapshot struct {
	Name       string            `json:"name"`
	CapturedAt string            `json:"capturedAt"`
	Context    string            `json:"context"`
	Brokers    int               `json:"brokers"`
	Topics     int               `json:"topics"`
	Groups     int               `json:"groups"`
	TopicList  []string          `json:"topicList"`
	GroupList  []string          `json:"groupList"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

var (
	snapTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	snapDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	snapNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#06B6D4"))
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Capture, list, and compare cluster state snapshots",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Capture current cluster state as a named snapshot",
	Example: `  kates snapshot create pre-upgrade
  kates snapshot create production-v3.2`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		snap := ClusterSnapshot{
			Name:       name,
			CapturedAt: time.Now().Format(time.RFC3339),
			Metadata:   map[string]string{},
		}

		cfg := loadConfig()
		snap.Context = cfg.CurrentContext

		info, err := apiClient.ClusterInfo(context.Background())
		if err == nil && info != nil {
			snap.Brokers = len(info.Brokers)
		}

		topics, err := apiClient.Topics(context.Background())
		if err == nil {
			snap.Topics = len(topics)
			snap.TopicList = topics
		}

		groups, err := apiClient.ConsumerGroups(context.Background())
		if err == nil {
			snap.Groups = len(groups)
			for _, g := range groups {
				snap.GroupList = append(snap.GroupList, g.GroupID)
			}
		}

		if err := saveSnapshot(snap); err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Snapshot %q saved (%d brokers, %d topics, %d groups)",
			name, snap.Brokers, snap.Topics, snap.Groups))
		return nil
	},
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		snaps, err := loadAllSnapshots()
		if err != nil {
			return err
		}
		if len(snaps) == 0 {
			output.Hint("No snapshots. Use 'kates snapshot create <name>'")
			return nil
		}

		fmt.Println(snapTitleStyle.Width(60).Render("  Saved Snapshots"))
		fmt.Println()
		fmt.Printf("  %-22s %-10s %-8s %-8s %s\n",
			snapDimStyle.Render("Name"),
			snapDimStyle.Render("Context"),
			snapDimStyle.Render("Brokers"),
			snapDimStyle.Render("Topics"),
			snapDimStyle.Render("Captured"),
		)
		fmt.Println("  " + strings.Repeat("─", 58))

		for _, s := range snaps {
			ts := s.CapturedAt
			if t, err := time.Parse(time.RFC3339, s.CapturedAt); err == nil {
				ts = t.Format("Jan 02 15:04")
			}
			fmt.Printf("  %-22s %-10s %-8d %-8d %s\n",
				snapNameStyle.Render(s.Name),
				s.Context, s.Brokers, s.Topics,
				snapDimStyle.Render(ts),
			)
		}
		fmt.Println()
		return nil
	},
}

var snapshotDiffCmd = &cobra.Command{
	Use:   "diff <name1> <name2>",
	Short: "Compare two snapshots",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s1, err := loadSnapshot(args[0])
		if err != nil {
			return err
		}
		s2, err := loadSnapshot(args[1])
		if err != nil {
			return err
		}

		fmt.Println(snapTitleStyle.Width(60).Render(
			fmt.Sprintf("  Snapshot Diff: %s → %s", s1.Name, s2.Name),
		))
		fmt.Println()

		fmt.Printf("  %-16s %-14s %-14s\n",
			snapDimStyle.Render("Property"),
			snapDimStyle.Render(s1.Name),
			snapDimStyle.Render(s2.Name),
		)
		fmt.Println("  " + strings.Repeat("─", 44))

		printSnapDiff("Brokers", s1.Brokers, s2.Brokers)
		printSnapDiff("Topics", s1.Topics, s2.Topics)
		printSnapDiff("Groups", s1.Groups, s2.Groups)
		fmt.Println()

		added, removed := diffStringLists(s1.TopicList, s2.TopicList)
		if len(added) > 0 {
			fmt.Println("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true).Render("+ Topics added:"))
			for _, t := range added {
				fmt.Printf("    + %s\n", t)
			}
		}
		if len(removed) > 0 {
			fmt.Println("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true).Render("- Topics removed:"))
			for _, t := range removed {
				fmt.Printf("    - %s\n", t)
			}
		}

		addedG, removedG := diffStringLists(s1.GroupList, s2.GroupList)
		if len(addedG) > 0 {
			fmt.Println("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true).Render("+ Groups added:"))
			for _, g := range addedG {
				fmt.Printf("    + %s\n", g)
			}
		}
		if len(removedG) > 0 {
			fmt.Println("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true).Render("- Groups removed:"))
			for _, g := range removedG {
				fmt.Printf("    - %s\n", g)
			}
		}

		fmt.Println()
		return nil
	},
}

func printSnapDiff(label string, a, b int) {
	delta := b - a
	var indicator string
	if delta > 0 {
		indicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render(fmt.Sprintf(" (+%d)", delta))
	} else if delta < 0 {
		indicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render(fmt.Sprintf(" (%d)", delta))
	}
	fmt.Printf("  %-16s %-14d %-14d%s\n", label, a, b, indicator)
}

func diffStringLists(a, b []string) (added, removed []string) {
	setA := map[string]bool{}
	setB := map[string]bool{}
	for _, s := range a {
		setA[s] = true
	}
	for _, s := range b {
		setB[s] = true
	}
	for _, s := range b {
		if !setA[s] {
			added = append(added, s)
		}
	}
	for _, s := range a {
		if !setB[s] {
			removed = append(removed, s)
		}
	}
	return
}

func snapshotDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kates", "snapshots")
}

func saveSnapshot(s ClusterSnapshot) error {
	dir := snapshotDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, s.Name+".json"), data, 0644)
}

func loadSnapshot(name string) (*ClusterSnapshot, error) {
	data, err := os.ReadFile(filepath.Join(snapshotDir(), name+".json"))
	if err != nil {
		return nil, fmt.Errorf("snapshot %q not found", name)
	}
	var s ClusterSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func loadAllSnapshots() ([]ClusterSnapshot, error) {
	dir := snapshotDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snaps []ClusterSnapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s ClusterSnapshot
		if json.Unmarshal(data, &s) == nil {
			snaps = append(snaps, s)
		}
	}
	return snaps, nil
}

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd, snapshotListCmd, snapshotDiffCmd)
	rootCmd.AddCommand(snapshotCmd)
	registerSnapshotCompletions()
}
