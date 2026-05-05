package detect

import "github.com/klster/kates-cli/output"

func RenderJSON(report *DetectReport) {
	output.JSON(report)
}
