package detect

import (
	"encoding/json"
	"io"

	"github.com/klster/kates-cli/output"
)

func RenderJSON(report *DetectReport) {
	output.JSON(report)
}

// RenderJSONTo writes the report as JSON to the given writer.
func RenderJSONTo(report *DetectReport, w io.Writer) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(report)
}
