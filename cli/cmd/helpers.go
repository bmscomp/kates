package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/klster/kates-cli/output"
	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame(tick int) string {
	return output.AccentStyle.Render(spinnerFrames[tick%len(spinnerFrames)])
}

// mapStr extracts a string value from a map, returning fallback for missing/nil keys.
func mapStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return "—"
	}
	return fmt.Sprintf("%v", v)
}

// mapStrEmpty is like mapStr but returns "" for missing keys (for logic, not display).
func mapStrEmpty(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func numVal(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func fmtNum(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", v/1_000)
	}
	return fmt.Sprintf("%.0f", v)
}

func fmtFloat(v float64, precision int) string {
	return fmt.Sprintf("%.*f", precision, v)
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func formatTime(ts string) string {
	if len(ts) > 19 {
		return ts[:10] + " " + ts[11:19]
	}
	return ts
}

func formatMetricVal(v float64, key, unit string) string {
	if key == "errorRate" {
		return fmt.Sprintf("%.4f%%", v*100)
	}
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM %s", v/1_000_000, unit)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK %s", v/1_000, unit)
	}
	return fmt.Sprintf("%.2f %s", v, unit)
}

func coloredCount(n int) string {
	if n > 0 {
		return output.ErrorStyle.Render(fmt.Sprintf("%d", n))
	}
	return output.DimStyle.Render("0")
}

func padLeftN(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w == 0 {
		return 80
	}
	return w
}

func describeType(t string) string {
	switch t {
	case "LOAD":
		return "Standard load test with target throughput"
	case "STRESS":
		return "High-volume multi-producer stress test"
	case "SPIKE":
		return "Sudden burst of traffic to test elasticity"
	case "ENDURANCE":
		return "Long-running soak test for stability"
	case "VOLUME":
		return "Large message payload throughput test"
	case "CAPACITY":
		return "Maximum capacity planning workload"
	case "ROUND_TRIP":
		return "End-to-end produce → consume latency"
	default:
		return ""
	}
}
