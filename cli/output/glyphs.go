package output

import (
	"os"
	"strings"
)

// GlyphSet is the CLI's glyph vocabulary. Every status mark, arrow, and bar
// segment renders through one of these two sets — never as a scattered
// literal — so the ASCII fallback is complete by construction.
type GlyphSet struct {
	Check  string
	Cross  string
	Warn   string
	Arrow  string
	Bullet string
	// Status ring variants: running, degraded, off.
	DotOn   string
	Diamond string
	Ring    string
	// Skip marks a step bypassed because it was already done.
	Skip string
	// Bar segments for progress bars.
	BarFull  string
	BarEmpty string
	// Rules: thin table/section separators and the heavy header bar.
	Rule      string
	HeavyRule string
	// Spark holds the sparkline ramp from lowest to highest.
	Spark []rune
}

var utf8Glyphs = GlyphSet{
	Check:     "✓",
	Cross:     "✖",
	Warn:      "⚠",
	Arrow:     "▸",
	Bullet:    "●",
	DotOn:     "◉",
	Diamond:   "◈",
	Ring:      "○",
	BarFull:   "█",
	BarEmpty:  "░",
	Rule:      "─",
	HeavyRule: "━",
	Spark:     []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'},
}

var asciiGlyphs = GlyphSet{
	Check:     "+",
	Cross:     "x",
	Warn:      "!",
	Arrow:     ">",
	Bullet:    "*",
	DotOn:     "*",
	Diamond:   "!",
	Ring:      "o",
	BarFull:   "#",
	BarEmpty:  "-",
	Rule:      "-",
	HeavyRule: "=",
	Spark:     []rune{'.', ':', '-', '=', '+', '*', '#', '@'},
}

// asciiForced is the environment's standing request for ASCII, computed once.
// plainMode is consulted live in Glyphs() because SetPlain runs after init.
var asciiForced = detectASCIIEnv(os.Getenv)

// detectASCIIEnv decides whether the environment positively asks for ASCII.
//
// The polarity matters: UTF-8 is the default, and ASCII engages only on
// explicit evidence — KATES_ASCII, or a locale EXPLICITLY set to a non-UTF-8
// value. An unset locale is NOT evidence of a limited terminal: containers,
// CI, and Windows commonly run with no locale set and render UTF-8 fine.
// Treating "unset" as "limited" would strip glyphs exactly where most output
// is read.
func detectASCIIEnv(getenv func(string) string) bool {
	if v := getenv("KATES_ASCII"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	// First set locale variable wins, mirroring libc precedence.
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := getenv(key)
		if v == "" {
			continue
		}
		lower := strings.ToLower(v)
		if strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8") {
			return false
		}
		// "C" and "POSIX" are explicit ASCII declarations.
		return true
	}
	return false
}

// Glyphs returns the active glyph set. --plain implies ASCII: plain output is
// a statement that a machine (or the most limited terminal) is reading.
func Glyphs() GlyphSet {
	if plainMode || asciiForced {
		return asciiGlyphs
	}
	return utf8Glyphs
}
