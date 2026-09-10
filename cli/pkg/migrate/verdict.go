package migrate

import (
	"regexp"
	"strings"
)

// Verdict statuses, from the icon the preflight probe prints.
const (
	VerdictPass = "PASS"
	VerdictFail = "FAIL"
	VerdictSkip = "SKIP"
)

// Verdict codes the chart's preflight Job classifies a probe into
// (charts/mirror-maker2/templates/preflight-job.yaml). The Job is the one
// classifier; this parser only reads its lines.
const (
	CodeHandshake  = "HANDSHAKE"
	CodeProtocol   = "PROTOCOL"
	CodeDNS        = "DNS"
	CodeTLS        = "TLS"
	CodeAuth       = "AUTH"
	CodeListener   = "LISTENER"
	CodeNetwork    = "NETWORK"
	CodeCredential = "CREDENTIAL"
)

// Verdict is one line of judgement from the preflight probe about one
// source.
type Verdict struct {
	// Source, Bootstrap and Mode come from the "══ source: <alias> ──
	// <bootstrap>  [<mode>]" header the verdict sits under.
	Source, Bootstrap, Mode string
	// Status is VerdictPass (✅), VerdictFail (❌) or VerdictSkip (⏭ — the
	// probe could not check something and says so).
	Status string
	// Code is the classification, one of the Code constants.
	Code string
	// Detail is the probe's explanation, its continuation lines joined into
	// one sentence.
	Detail string
	// Output is what the probe echoed of the client's own output after the
	// explanation (at most a few lines), for diagnostics.
	Output []string
}

// Failed reports whether the verdict is a failure.
func (v Verdict) Failed() bool {
	return v.Status == VerdictFail
}

// Preflight is a whole preflight log, read.
type Preflight struct {
	// Client is the image the probe ran, from the log's first line.
	Client   string
	Verdicts []Verdict
	// Concluded says the log reached its summary line; Passed is that
	// line's verdict. A log cut short (the Job killed by its deadline) has
	// neither.
	Concluded, Passed bool
}

var (
	// "  ✅ HANDSHAKE broker answered …", "  ❌ DNS       the bootstrap …",
	// "  ⏭  TLS       reachable and …". The summary lines ("❌ preflight:
	// 1 source(s) failed") do not match: their word is lowercase.
	verdictLine = regexp.MustCompile(`^\s*(✅|❌|⏭)\s+([A-Z]+)\s+(.*)$`)
	// "══ source: legacy ── host:9092  [plaintext]"
	sourceLine = regexp.MustCompile(`^══ source: (\S+) ── (\S+)\s+\[([^\]]+)\]`)
	// The probe indents continuation lines and echoed client output by
	// fifteen spaces.
	continuation = regexp.MustCompile(`^\s{10,}(\S.*)$`)
	// Lines that are the Kafka client's own output rather than the probe's
	// prose: log4j records, exceptions, stack frames, the ApiVersions
	// listing.
	clientOutput = regexp.MustCompile(`^(\[\d{4}-|Exception in thread|Caused by:|at [a-z]+\.|java\.|org\.apache\.|\S+:\d+ \(id: )`)
	clientLine   = regexp.MustCompile(`^MirrorMaker 2 preflight — client (\S+)`)
	summaryLine  = regexp.MustCompile(`^(✅|❌) preflight:`)
)

// ParseVerdicts reads the preflight Job's log and returns every verdict it
// printed, in order, with the source each belongs to. A log with no verdict
// lines yields nil.
func ParseVerdicts(log string) []Verdict {
	return ParsePreflight(log).Verdicts
}

// ParsePreflight reads the whole preflight log: the client image, the
// verdicts, and whether the probe concluded and passed.
func ParsePreflight(log string) Preflight {
	var p Preflight
	var source, bootstrap, mode string
	var cur *Verdict
	inOutput := false
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := clientLine.FindStringSubmatch(line); m != nil {
			p.Client = m[1]
			cur = nil
			continue
		}
		if m := sourceLine.FindStringSubmatch(line); m != nil {
			source, bootstrap, mode = m[1], m[2], m[3]
			cur = nil
			continue
		}
		if m := summaryLine.FindStringSubmatch(line); m != nil {
			p.Concluded = true
			p.Passed = m[1] == "✅"
			cur = nil
			continue
		}
		if m := verdictLine.FindStringSubmatch(line); m != nil {
			p.Verdicts = append(p.Verdicts, Verdict{
				Source: source, Bootstrap: bootstrap, Mode: mode,
				Status: iconStatus(m[1]), Code: m[2], Detail: strings.TrimSpace(m[3]),
			})
			cur = &p.Verdicts[len(p.Verdicts)-1]
			inOutput = false
			continue
		}
		if cur == nil {
			continue
		}
		m := continuation.FindStringSubmatch(line)
		if m == nil {
			// A blank or unindented line ends the verdict's block.
			cur = nil
			continue
		}
		text := strings.TrimSpace(m[1])
		if inOutput || clientOutput.MatchString(text) {
			inOutput = true
			cur.Output = append(cur.Output, text)
			continue
		}
		cur.Detail = strings.TrimSpace(cur.Detail + " " + text)
	}
	return p
}

// AnyFailed reports whether any verdict is a failure.
func AnyFailed(verdicts []Verdict) bool {
	for _, v := range verdicts {
		if v.Failed() {
			return true
		}
	}
	return false
}

func iconStatus(icon string) string {
	switch icon {
	case "✅":
		return VerdictPass
	case "❌":
		return VerdictFail
	default:
		return VerdictSkip
	}
}
