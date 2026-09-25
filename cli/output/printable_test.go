package output

import (
	"testing"
	"unicode"
)

func TestPrintable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text", in: "Kill partition leaders", want: "Kill partition leaders"},
		{name: "other scripts and symbols", in: "Größe ✓ 日本 — ok", want: "Größe ✓ 日本 — ok"},
		{name: "colour", in: "Kill \x1b[31mleaders\x1b[0m & <b>", want: "Kill leaders & <b>"},
		{name: "clear screen and cursor home", in: "a\x1b[2J\x1b[Hb", want: "ab"},
		{name: "window title, BEL-terminated", in: "\x1b]0;pwned\x07title", want: "title"},
		{name: "hyperlink, ST-terminated", in: "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", want: "link"},
		{name: "terminal reset, ESC c", in: "a\x1bcb", want: "ab"},
		{name: "a trailing ESC", in: "a\x1b", want: "a"},
		{name: "C0 controls", in: "a\x00b\x07c\x08d", want: "abcd"},
		{name: "DEL and C1 controls", in: "a\x7fb\u0085c\u009bd", want: "abcd"},
		{name: "line breaks and tabs", in: "one\ntwo\r\nthree\tfour", want: "one two  three four"},
		{name: "Unicode line and paragraph separators", in: "a\u2028b\u2029c", want: "a b c"},
		{name: "bidi override", in: "invoice\u202egpj.exe", want: "invoicegpj.exe"},
		{name: "bidi isolates and marks", in: "\u2066a\u2069\u200eb\u200f\u061c", want: "ab"},
		{name: "zero-width characters", in: "a\u200bb\u200cc\u200dd\u2060e\ufeff", want: "abcde"},
		{name: "tag characters", in: "ok\U000E0001\U000E0069\U000E0067", want: "ok"},
		{name: "invalid UTF-8", in: "a\xffb", want: "ab"},
		{name: "empty", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Printable(tt.in)
			if got != tt.want {
				t.Errorf("Printable(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for _, r := range got {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Errorf("Printable(%q) kept %U", tt.in, r)
				}
			}
		})
	}
}
