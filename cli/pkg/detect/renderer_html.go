package detect

import (
	_ "embed"
	"html/template"
	"io"
	"strings"
)

//go:embed report.html.tmpl
var reportHTML string

// htmlReport is the page template. html/template escapes every value by
// the context it lands in, so nothing from the cluster needs escaping by
// hand.
var htmlReport = template.Must(template.New("report").Funcs(template.FuncMap{
	"rowStatus": func(row []Cell) Status {
		var st Status
		for _, c := range row {
			st = worse(st, c.Status)
		}
		return st
	},
	"cellClass": func(matrix bool, i int, c Cell) string {
		var classes []string
		if matrix && i == 0 {
			classes = append(classes, "matrix-head")
		} else if matrix {
			classes = append(classes, "matrix-cell")
		}
		if c.Status != StatusNone {
			classes = append(classes, "status-"+string(c.Status))
		}
		return strings.Join(classes, " ")
	},
	"verdictClass": func(st Status) string {
		return map[Status]string{StatusOK: "pass", StatusWarn: "warn", StatusFail: "fail"}[st]
	},
}).Parse(reportHTML))

// RenderHTML writes the detect report as a single self-contained HTML page.
func RenderHTML(report *DetectReport, w io.Writer) error {
	return renderHTMLDoc(BuildReportDoc(report), w)
}

func renderHTMLDoc(doc *ReportDoc, w io.Writer) error {
	return htmlReport.Execute(w, doc)
}
