package detect

import (
	"bytes"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

// captureTUI runs the terminal view with the output package writing to a
// buffer.
func captureTUI(t *testing.T, doc *ReportDoc) string {
	t.Helper()
	out, errOut := output.Out, output.Err
	t.Cleanup(func() { output.Out, output.Err = out, errOut })
	buf := output.ResetForTesting()
	renderTUIDoc(doc)
	return buf.String()
}

// pdfContent is the PDF's page content, uncompressed, so a test can find the
// text it draws (string operands escape parentheses).
func pdfContent(t *testing.T, doc *ReportDoc) string {
	t.Helper()
	pdf := renderPDFDoc(doc)
	pdf.SetCompression(false)
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("PDF render: %v", err)
	}
	return buf.String()
}

// Every section BuildReportDoc makes is in every view: the HTML report
// used to leave out the nodes, storage and Strimzi parts.
func TestEverySectionInEveryView(t *testing.T) {
	pdfEscape := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			doc := fixtureDoc(f.report())
			var md, page bytes.Buffer
			renderMarkdownDoc(doc, &md)
			if err := renderHTMLDoc(doc, &page); err != nil {
				t.Fatal(err)
			}
			views := map[string]string{
				"markdown": md.String(),
				"html":     page.String(),
				"tui":      captureTUI(t, doc),
				"pdf":      pdfContent(t, doc),
			}
			for _, s := range doc.Sections {
				want := map[string]string{
					"markdown": s.Title,
					"html":     html.EscapeString(s.Title),
					"tui":      s.Title,
					"pdf":      pdfEscape.Replace(s.Title),
				}
				for view, text := range views {
					if !strings.Contains(text, want[view]) {
						t.Errorf("%s view lacks section %q", view, s.Title)
					}
				}
			}
			for view, text := range views {
				if !strings.Contains(text, doc.Verdict.Label) {
					t.Errorf("%s view lacks the verdict %q", view, doc.Verdict.Label)
				}
			}
		})
	}
}

func TestRenderPDFFixtures(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), f.name+".pdf")
			if err := RenderPDF(f.report(), path); err != nil {
				t.Fatalf("RenderPDF: %v", err)
			}
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Fatalf("no PDF written: %v", err)
			}
		})
	}
}

// The terminal view says the same thing the files do: the verdict label is
// the shared one, not the old PARTIAL.
func TestRenderTUIWording(t *testing.T) {
	r := healthyReport()
	r.Verdict.Warns = 1
	text := captureTUI(t, fixtureDoc(r))
	if !strings.Contains(text, "RESULT: COMPATIBLE WITH WARNINGS") || strings.Contains(text, "PARTIAL") {
		t.Errorf("TUI verdict is not the shared label:\n%s", text)
	}
	text = captureTUI(t, fixtureDoc(degradedReport()))
	if !strings.Contains(text, "Secret creation: blocked by admission policy restrict-secrets") {
		t.Errorf("TUI lacks the Secret audit fact:\n%s", text)
	}
}

func TestRenderHTMLGolden(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderHTMLDoc(fixtureDoc(f.report()), &buf); err != nil {
				t.Fatal(err)
			}
			checkHTMLStructure(t, buf.String())
			checkGolden(t, f.name+".html", buf.Bytes())
		})
	}
}

// htmlTag matches a start or end tag. golang.org/x/net/html is not a
// dependency of this module, so the page is checked by a tag stack: every
// element opens and closes in order, and the only script is the page's own.
var htmlTag = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)

var voidElements = map[string]bool{"meta": true, "br": true, "hr": true, "img": true, "input": true, "link": true}

func checkHTMLStructure(t *testing.T, page string) {
	t.Helper()
	if n := strings.Count(page, "<script"); n != 1 {
		t.Errorf("page holds %d <script elements, want only its own", n)
	}
	// style and script bodies are raw text: drop them before the tag walk.
	for _, raw := range []string{"style", "script"} {
		re := regexp.MustCompile(`(?s)<` + raw + `>.*?</` + raw + `>`)
		page = re.ReplaceAllString(page, "<"+raw+"></"+raw+">")
	}
	var stack []string
	for _, m := range htmlTag.FindAllStringSubmatch(page, -1) {
		name := strings.ToLower(m[2])
		switch {
		case voidElements[name]:
		case m[1] == "":
			stack = append(stack, name)
		case len(stack) == 0 || stack[len(stack)-1] != name:
			t.Fatalf("</%s> closes %v", name, stack)
		default:
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		t.Errorf("unclosed elements %v", stack)
	}
}
