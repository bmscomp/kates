package compliance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var expectLine = regexp.MustCompile(`(?m)^# expect: (\S+)`)

// lintDay is the date the corpus is compiled against.
var lintDay = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

// Every file in testdata/invalid is one the Kates API's reader refuses
// (KafkaReferenceCorpusTest reads the same files); the compiler must refuse
// it too, with an error at the same path, so the two cannot drift apart.
func TestCompilerRefusesWhatTheReaderRefuses(t *testing.T) {
	files, err := filepath.Glob("testdata/invalid/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			m := expectLine.FindSubmatch(src)
			if m == nil {
				t.Fatal("no # expect: line")
			}
			want := string(m[1])
			var got []string
			for _, d := range Compile(src, lintDay).Diagnostics {
				if d.Severity == Error && d.Path == want {
					return
				}
				got = append(got, d.Format(filepath.Base(f)))
			}
			t.Errorf("no error at %s; got:\n%s", want, strings.Join(got, "\n"))
		})
	}
}
