package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/klster/kates-cli/output"
)

func printDryRun(label string, payload interface{}) {
	output.Warn(fmt.Sprintf("DRY RUN — %s", label))
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		output.Error("Failed to serialize: " + err.Error())
		return
	}
	fmt.Println(string(data))
	output.Hint("No changes were made. Remove --dry-run to execute.")
}
