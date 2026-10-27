package detect

import (
	"encoding/json"
	"io"

	"github.com/bmscomp/kates/cli/output"
)

func RenderJSON(report *DetectReport) {
	output.JSON(report)
}

// RenderJSONTo writes the report as JSON to the given writer, and returns
// the write's error.
func RenderJSONTo(report *DetectReport, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
