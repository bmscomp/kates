package cmd

import (
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
)

var componentGroupNames = map[string]string{
	"A": "Operators & CRDs",
	"B": "Core Infrastructure",
	"C": "Applications",
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame(tick int) string {
	return output.AccentStyle.Render(spinnerFrames[tick%len(spinnerFrames)])
}

// silentErr is an error that has already been printed to the user.
// Cobra sees it as a non-nil error (→ exit code 1) but SilenceErrors
// prevents it from printing again.
type silentErr struct{ msg string }

func (e *silentErr) Error() string { return e.msg }

// exitError ends a command with its own exit code, after printing msg when
// there is one: kates conformity check exits 1 on a gap and 2 when it could
// not tell.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// cmdErr prints a styled error and returns a silentErr so Cobra sets exit code 1.
func cmdErr(msg string) error {
	output.Error(msg)
	if refusedLocalAPI(msg) {
		output.Hint("Nothing answers on that local port: forward the Kates API with kates ports, or point --url or KATES_URL at it.")
	}
	return &silentErr{msg: msg}
}

// refusedLocalAPI is a request that found no listener on this machine, which
// is what an API reached through a port-forward that has stopped looks like.
func refusedLocalAPI(msg string) bool {
	return strings.Contains(msg, "connection refused") &&
		(strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "localhost") || strings.Contains(msg, "[::1]"))
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

// termWidth delegates to the single shared width helper. Three copies of this
// existed with three different fallbacks (120, 80, and 72-capped-80), so the
// same terminal state produced three different layouts depending on which
// command you ran.
func termWidth() int {
	return output.TermWidth()
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
	case "INTEGRITY":
		return "Data integrity verification with CRC checks"
	case "TUNE_REPLICATION":
		return "Measures throughput and latency at one replication factor"
	case "TUNE_ACKS":
		return "Measures throughput and latency at one acks setting"
	case "TUNE_BATCHING":
		return "Measures throughput and latency at one batch.size and linger.ms"
	case "TUNE_COMPRESSION":
		return "Measures throughput and latency with one compression codec"
	case "TUNE_PARTITIONS":
		return "Measures throughput and latency at one partition count"
	default:
		return ""
	}
}

var hintPatterns = []struct {
	pattern string
	hint    string
}{
	{"LZ4Factory", "Missing lz4-java dependency — add it to pom.xml"},
	{"NoClassDefFoundError", "A required class is missing at runtime — check Maven dependencies"},
	{"UnsatisfiedLinkError", "Native library failed to load — if using native image, check JNI config; if JVM, check platform .so/.dylib"},
	{"zstd", "zstd compression requires the zstd-jni native library — use compression=lz4 as a workaround"},
	{"Connection refused", "Kafka broker unreachable — verify bootstrap servers and network"},
	{"TimeoutException", "Operation timed out — increase timeout or check broker health"},
	{"TopicAuthorizationException", "ACL permission denied — check Kafka ACL configuration"},
	{"UnknownTopicOrPartition", "Topic does not exist — create it or check topic name"},
	{"RecordTooLargeException", "Message exceeds max.message.bytes — reduce record size or increase broker limit"},
	{"NotLeaderOrFollower", "Partition leader changed — this is often transient, retry the test"},
	{"OutOfMemoryError", "JVM ran out of memory — increase -Xmx in deployment config"},
	{"NetworkException", "Network error communicating with broker — check cluster connectivity"},
	{"UNKNOWN_TOPIC_OR_PARTITION", "Topic does not exist — create it or check topic name"},
	{"ConnectRestException", "Invalid connector configuration — check the connector properties for missing or invalid values"},
}

func matchHints(errMsg string) []string {
	var hints []string
	lower := strings.ToLower(errMsg)
	for _, p := range hintPatterns {
		if strings.Contains(lower, strings.ToLower(p.pattern)) {
			hints = append(hints, p.hint)
		}
	}
	return hints
}
