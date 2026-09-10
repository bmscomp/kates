package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show CLI and runtime version information",
	Run: func(cmd *cobra.Command, args []string) {
		if outputMode == "json" {
			output.JSON(map[string]interface{}{
				"cli":        Version,
				"commit":     Commit,
				"buildDate":  BuildDate,
				"executable": runningBinaryPath(),
				"go":         runtime.Version(),
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
			})
			return
		}

		output.Header("Version")
		output.KeyValue("Kates CLI", Version)
		output.KeyValue("Commit", Commit)
		output.KeyValue("Built", BuildDate)
		// Which file is this? A stale copy earlier in PATH is the usual
		// reason a freshly installed feature looks missing, and the answer
		// belongs next to the version it reports.
		output.KeyValue("Binary", runningBinaryPath())
		output.KeyValue("Go", runtime.Version())
		output.KeyValue("OS/Arch", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

		// Try to get API version
		health, err := apiClient.Health(context.Background())
		if err == nil {
			output.KeyValue("API Status", output.StatusBadge(health.Status))
			if eng := health.Engine; eng != nil {
				output.KeyValue("Backend", eng.ActiveBackend)
			}
		} else {
			output.KeyValue("API", output.DimStyle.Render("not reachable"))
		}
	},
}

// runningBinaryPath is the file this process was started from, with
// symlinks resolved, so `kates version` names the copy that actually ran.
func runningBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
