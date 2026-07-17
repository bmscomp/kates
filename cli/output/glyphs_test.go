package output

import "testing"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectASCIIEnv(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		want bool
	}{
		// The polarity rule: ASCII only on POSITIVE evidence. An unset locale
		// is how containers, CI, and Windows normally run — and they render
		// UTF-8 fine. Treating "unset" as "limited" would strip glyphs exactly
		// where most output is read.
		{"no env at all stays UTF-8", map[string]string{}, false},
		{"utf-8 locale stays UTF-8", map[string]string{"LANG": "en_US.UTF-8"}, false},
		{"lowercase utf8 spelling stays UTF-8", map[string]string{"LC_ALL": "C.utf8"}, false},

		// Explicit requests engage ASCII.
		{"KATES_ASCII=1", map[string]string{"KATES_ASCII": "1"}, true},
		{"KATES_ASCII=true", map[string]string{"KATES_ASCII": "true"}, true},
		{"C locale is an explicit ASCII declaration", map[string]string{"LANG": "C"}, true},
		{"POSIX locale", map[string]string{"LC_ALL": "POSIX"}, true},
		{"latin1 locale", map[string]string{"LANG": "en_US.ISO-8859-1"}, true},

		// libc precedence: LC_ALL wins over LANG.
		{"LC_ALL=utf8 beats LANG=C", map[string]string{"LC_ALL": "C.UTF-8", "LANG": "C"}, false},
		{"LC_ALL=C beats LANG=utf8", map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectASCIIEnv(env(tt.vars)); got != tt.want {
				t.Errorf("detectASCIIEnv(%v) = %v, want %v", tt.vars, got, tt.want)
			}
		})
	}
}

func TestGlyphs_PlainModeForcesASCII(t *testing.T) {
	origPlain, origForced := plainMode, asciiForced
	t.Cleanup(func() { plainMode, asciiForced = origPlain, origForced })

	plainMode, asciiForced = false, false
	if Glyphs().Check != "✓" {
		t.Errorf("default must be UTF-8, got check=%q", Glyphs().Check)
	}

	plainMode = true
	if Glyphs().Check != "+" {
		t.Errorf("--plain must force ASCII, got check=%q", Glyphs().Check)
	}
}

// Every ASCII glyph must actually be ASCII — the fallback existing is not the
// same as the fallback being complete.
func TestASCIIGlyphsAreASCII(t *testing.T) {
	g := asciiGlyphs
	for name, s := range map[string]string{
		"Check": g.Check, "Cross": g.Cross, "Warn": g.Warn, "Arrow": g.Arrow,
		"Bullet": g.Bullet, "DotOn": g.DotOn, "Diamond": g.Diamond, "Ring": g.Ring,
		"BarFull": g.BarFull, "BarEmpty": g.BarEmpty, "Spark": string(g.Spark),
	} {
		for _, r := range s {
			if r > 127 {
				t.Errorf("asciiGlyphs.%s contains non-ASCII rune %q", name, r)
			}
		}
	}
}
