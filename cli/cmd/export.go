package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	testExportFormat string
	testExportFile   string
)

var testExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export test results to a file (CSV or JSON)",
	Example: `  kates test export 69acdf31
  kates test export 69acdf31 --format json
  kates test export 69acdf31 -f results.csv`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		outPath := testExportFile
		if outPath == "" {
			outPath = fmt.Sprintf("%s.%s", id, testExportFormat)
		}

		switch testExportFormat {
		case "csv":
			data, err := apiClient.ExportCSV(ctx, id)
			if err != nil {
				return cmdErr("Failed to export CSV: " + err.Error())
			}
			if err := writeExportFile(outPath, []byte(data)); err != nil {
				return cmdErr("Failed to write file: " + err.Error())
			}

		case "json":
			result, err := apiClient.GetTest(ctx, id)
			if err != nil {
				return cmdErr("Test not found: " + err.Error())
			}
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return cmdErr("Failed to marshal JSON: " + err.Error())
			}
			if err := writeExportFile(outPath, data); err != nil {
				return cmdErr("Failed to write file: " + err.Error())
			}

		default:
			return cmdErr("Unknown format: " + testExportFormat + " (use csv or json)")
		}

		output.Success(fmt.Sprintf("Exported %s → %s (%s)", truncID(id), outPath, testExportFormat))
		return nil
	},
}

func writeExportFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		os.MkdirAll(dir, 0755)
	}
	return os.WriteFile(path, data, 0644)
}

func init() {
	testExportCmd.Flags().StringVar(&testExportFormat, "format", "csv", "Export format: csv or json")
	testExportCmd.Flags().StringVarP(&testExportFile, "file", "f", "", "Output file path (default: <id>.<format>)")
	testCmd.AddCommand(testExportCmd)
}
