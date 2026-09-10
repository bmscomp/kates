package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Record is one line of the corpus: a JSON object the producer writes to the
// source and the verifier expects back, byte for byte, from the target.
type Record struct {
	ID      int    `json:"id"`
	Lab     string `json:"lab"`
	Payload string `json:"payload"`
}

// Records builds the corpus for a lab: n JSON lines with ids 1..n, each
// carrying the lab name and a payload derived from both, so a record from
// another lab or another run on the same topic is not mistaken for one of
// ours. The output is deterministic: the same lab and n give the same lines.
func Records(lab string, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		line, err := json.Marshal(Record{ID: i, Lab: lab, Payload: payload(lab, i)})
		if err != nil {
			// Marshalling a struct of ints and strings cannot fail.
			panic(err)
		}
		out = append(out, string(line))
	}
	return out
}

func payload(lab string, id int) string {
	sum := sha256.Sum256([]byte(lab + ":" + jsonInt(id)))
	return hex.EncodeToString(sum[:8])
}

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// ParseRecord reads one consumed line back into a Record; ok is false for
// anything that is not one of our JSON objects.
func ParseRecord(line string) (Record, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return Record{}, false
	}
	var r Record
	if err := json.Unmarshal([]byte(line), &r); err != nil || r.ID <= 0 || r.Lab == "" {
		return Record{}, false
	}
	return r, true
}

// ParseConsumed reads a console consumer's output and keeps the corpus
// lines: trimmed, non-empty, and JSON records (ParseRecord). The consumer's
// own chatter — "Processed a total of 200 messages", log4j lines, a stray
// prompt — is dropped. Duplicates are kept: MirrorMaker 2 is at-least-once
// and Distinct counts what matters.
func ParseConsumed(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		if _, ok := ParseRecord(line); ok {
			out = append(out, line)
		}
	}
	return out
}

// Distinct counts the distinct non-empty lines.
func Distinct(lines []string) int {
	seen := map[string]bool{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			seen[l] = true
		}
	}
	return len(seen)
}

// Diff compares two corpora as sets: missing are the expected lines that
// never arrived (in expected order), extra the received lines that were
// not expected (in first-seen order). It is the scripts' comm(1) check —
// every record 1..N present — without the sort, the pipe or the subshell.
func Diff(expected, actual []string) (missing, extra []string) {
	got := map[string]bool{}
	for _, a := range actual {
		got[strings.TrimSpace(a)] = true
	}
	want := map[string]bool{}
	for _, e := range expected {
		e = strings.TrimSpace(e)
		want[e] = true
		if !got[e] {
			missing = append(missing, e)
		}
	}
	seen := map[string]bool{}
	for _, a := range actual {
		a = strings.TrimSpace(a)
		if a == "" || want[a] || seen[a] {
			continue
		}
		seen[a] = true
		extra = append(extra, a)
	}
	return missing, extra
}

// IDs returns the record ids of the given corpus lines, in order, skipping
// lines that are not records — for a report detail such as "missing from
// the target: 3 4 5".
func IDs(lines []string) []int {
	var out []int
	for _, l := range lines {
		if r, ok := ParseRecord(l); ok {
			out = append(out, r.ID)
		}
	}
	return out
}
