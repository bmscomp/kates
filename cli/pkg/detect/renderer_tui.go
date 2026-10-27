package detect

import (
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
)

// RenderTUI prints the detect report to the terminal.
func RenderTUI(report *DetectReport) {
	renderTUIDoc(BuildReportDoc(report))
}

// renderTUIDoc prints every section, then the verdict, so the verdict is
// the last thing on the screen.
func renderTUIDoc(doc *ReportDoc) {
	for _, s := range doc.Sections {
		renderTUISection(s)
	}
	output.Banner("RESULT: "+doc.Verdict.Label, doc.Verdict.Summary)
}

// tuiLine prints one line in the style its status calls for.
func tuiLine(st Status, text string) {
	switch st {
	case StatusOK:
		output.Success(text)
	case StatusWarn:
		output.Warn(text)
	case StatusFail:
		output.Error(text)
	default:
		output.Hint(text)
	}
}

// tuiMark is a cell's text with its status glyph in front, when it has one.
func tuiMark(c Cell) string {
	g := output.Glyphs()
	mark := map[Status]string{StatusWarn: g.Warn, StatusFail: g.Cross}[c.Status]
	if mark == "" {
		return c.Text
	}
	return mark + " " + c.Text
}

func renderTUISection(s Section) {
	output.Header(s.Title)
	for _, p := range s.Paragraphs {
		tuiLine(s.Status, p)
	}
	for _, f := range s.Facts {
		if f.Status == StatusNone {
			output.KeyValue(f.Label+":", f.Value)
			continue
		}
		tuiLine(f.Status, f.Label+": "+f.Value)
	}
	for _, m := range s.Meters {
		output.KeyValue(m.Label+":", fmt.Sprintf("%.0f%% (%s)", m.Percent, m.Detail))
	}
	for _, t := range s.Tables {
		headers := make([]string, len(t.Headers))
		for i, h := range t.Headers {
			headers[i] = strings.ToUpper(h)
		}
		rows := make([][]string, len(t.Rows))
		for i, row := range t.Rows {
			rows[i] = make([]string, len(row))
			for j, c := range row {
				rows[i][j] = tuiMark(c)
			}
		}
		output.Table(headers, rows)
	}
	for _, line := range s.Code {
		output.Warn(line)
	}
	for _, n := range s.Notes {
		output.Hint(n)
	}
	for _, h := range s.Hints {
		renderTUIHint(h)
	}
}

func renderTUIHint(h Hint) {
	tuiLine(h.Status, h.Title)
	arrow := output.Glyphs().Arrow
	for _, cmd := range h.Commands {
		fmt.Fprintf(output.Out, "    %s %s\n", arrow, cmd)
	}
	if h.DocURL != "" {
		fmt.Fprintf(output.Out, "    docs: %s\n", h.DocURL)
	}
	fmt.Fprintln(output.Out)
}
