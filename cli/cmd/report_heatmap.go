package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var reportHeatmapCmd = &cobra.Command{
	Use:   "heatmap <id>",
	Short: "Render a latency heatmap as an ASCII chart in the terminal",
	Args:  cobra.ExactArgs(1),
	Example: `  kates report heatmap abc123
  kates report heatmap abc123 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		raw, err := apiClient.ExportHeatmap(context.Background(), id, "json")
		if err != nil {
			return cmdErr("Failed to get heatmap: " + err.Error())
		}

		if outputMode == "json" {
			fmt.Println(raw)
			return nil
		}

		var data heatmapData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return cmdErr("Failed to parse heatmap: " + err.Error())
		}

		output.Banner("Latency Heatmap", "Test: "+truncID(id))
		renderHeatmap(data)
		return nil
	},
}

type heatmapData struct {
	Buckets    []string    `json:"buckets"`
	TimeSlices []timeSlice `json:"timeSlices"`
}

type timeSlice struct {
	Label  string    `json:"label"`
	Values []float64 `json:"values"`
}

var heatBlocks = []string{"░", "▒", "▓", "█"}

var heatGradient = []lipgloss.Color{
	lipgloss.Color("#1a1a2e"),
	lipgloss.Color("#16213e"),
	lipgloss.Color("#0f3460"),
	lipgloss.Color("#533483"),
	lipgloss.Color("#e94560"),
}

func renderHeatmap(data heatmapData) {
	if len(data.TimeSlices) == 0 || len(data.Buckets) == 0 {
		output.Hint("No heatmap data available.")
		return
	}

	maxVal := 0.0
	for _, ts := range data.TimeSlices {
		for _, v := range ts.Values {
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	w := termWidth()
	labelWidth := 10
	availCols := w - labelWidth - 4
	step := 1
	if len(data.TimeSlices) > availCols {
		step = len(data.TimeSlices) / availCols
		if step < 1 {
			step = 1
		}
	}

	for bi := len(data.Buckets) - 1; bi >= 0; bi-- {
		bucket := data.Buckets[bi]
		if len(bucket) > labelWidth-1 {
			bucket = bucket[:labelWidth-1]
		}
		label := fmt.Sprintf("%*s ", labelWidth-1, bucket)

		var row strings.Builder
		for ti := 0; ti < len(data.TimeSlices); ti += step {
			ts := data.TimeSlices[ti]
			val := 0.0
			if bi < len(ts.Values) {
				val = ts.Values[bi]
			}
			intensity := val / maxVal
			block := heatBlock(intensity)
			row.WriteString(block)
		}
		fmt.Printf("%s%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(label), row.String())
	}

	fmt.Printf("%s%s\n",
		strings.Repeat(" ", labelWidth),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render("time →"))

	legend := "  "
	for i, block := range heatBlocks {
		pct := float64(i+1) / float64(len(heatBlocks)) * 100
		legend += fmt.Sprintf("%s %.0f%%  ", colorizeHeat(block, float64(i+1)/float64(len(heatBlocks))), pct)
	}
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render("Legend:") + legend)
}

func heatBlock(intensity float64) string {
	if intensity <= 0 {
		return " "
	}
	idx := int(math.Min(float64(len(heatBlocks)-1), intensity*float64(len(heatBlocks))))
	return colorizeHeat(heatBlocks[idx], intensity)
}

func colorizeHeat(block string, intensity float64) string {
	idx := int(math.Min(float64(len(heatGradient)-1), intensity*float64(len(heatGradient))))
	return lipgloss.NewStyle().Foreground(heatGradient[idx]).Render(block)
}

func init() {
	reportCmd.AddCommand(reportHeatmapCmd)
}
