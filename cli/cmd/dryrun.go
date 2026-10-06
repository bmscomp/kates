package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bmscomp/kates/cli/output"
)

// printDryRun prints what the command would send. <, > and & stay as they
// are: json.Marshal escapes them for HTML, which printed a probe's comparator
// "<=" as "\u003c=".
func printDryRun(label string, payload interface{}) {
	output.Warn(fmt.Sprintf("DRY RUN — %s", label))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		output.Error("Failed to serialize: " + err.Error())
		return
	}
	fmt.Print(buf.String())
	output.Hint("No changes were made. Remove --dry-run to execute.")
}
