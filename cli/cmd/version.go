package cmd

import (
	"fmt"
	"runtime"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

const cliVersion = "1.0.0"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show CLI and runtime version information",
	Run: func(cmd *cobra.Command, args []string) {
		if outputMode == "json" {
			output.JSON(map[string]interface{}{
				"cli":  cliVersion,
				"go":   runtime.Version(),
				"os":   runtime.GOOS,
				"arch": runtime.GOARCH,
			})
			return
		}

		output.Header("Version")
		output.KeyValue("KATES CLI", cliVersion)
		output.KeyValue("Go", runtime.Version())
		output.KeyValue("OS/Arch", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

		// Try to get API version
		health, err := apiClient.Health()
		if err == nil {
			output.KeyValue("API Status", output.StatusBadge(strVal(health, "status")))
			if eng, ok := health["engine"].(map[string]interface{}); ok {
				output.KeyValue("Backend", strVal(eng, "activeBackend"))
			}
		} else {
			output.KeyValue("API", output.DimStyle.Render("not reachable"))
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
