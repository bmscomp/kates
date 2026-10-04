package migrate

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PinsFile is the repository's version-pin file, relative to the repo root.
const PinsFile = "versions.env"

// Pins are the version pins the migration reads from versions.env. There is
// deliberately no literal fallback for any of them: a fallback would be one
// more version site that scripts/check-versions.sh does not guard.
type Pins struct {
	// StrimziVersion is STRIMZI_VERSION, the operator the platform is tested
	// with.
	StrimziVersion string
	// KafkaVersion is the Kafka line of STRIMZI_KAFKA_VERSION
	// ("1.1.0-kafka-4.3.0" → "4.3.0"), the primary's Kafka and the line the
	// client image runs.
	KafkaVersion string
	// KafkaImage is KAFKA_IMAGE with its ${…} references resolved: the
	// client image, which is the one the MirrorMaker 2 workers run — that
	// is what makes its verdicts evidence.
	KafkaImage string
	// All holds every KEY=value pair of the file, resolved, for pins this
	// struct does not name.
	All map[string]string
}

var (
	pinLine   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	pinRef    = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	kafkaTag  = regexp.MustCompile(`-kafka-(\d+\.\d+\.\d+)$`)
	errNoPins = errors.New("no pins found")
)

// ReadPins reads <repoRoot>/versions.env. A missing file is an error that
// names it — the CLI runs from the repository root, like `kates deploy`.
func ReadPins(repoRoot string) (Pins, error) {
	path := filepath.Join(repoRoot, PinsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Pins{}, fmt.Errorf("%s not found at %s: run kates from the repository root", PinsFile, repoRoot)
		}
		return Pins{}, fmt.Errorf("read %s: %w", path, err)
	}
	p, err := ParsePins(string(data))
	if err != nil {
		return Pins{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// ParsePins reads the content of a versions.env: KEY="value" or KEY=value
// lines, # comments, blank lines, and ${VAR} / $VAR references to keys
// defined earlier in the file (the way a shell sourcing it would resolve
// them). Quotes around a value are removed. The three named pins must be
// present and STRIMZI_KAFKA_VERSION must carry a -kafka-x.y.z suffix.
func ParsePins(text string) (Pins, error) {
	all := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		m := pinLine.FindStringSubmatch(line)
		if m == nil {
			return Pins{}, fmt.Errorf("line %d: not a KEY=value assignment: %q", n, line)
		}
		key, value := m[1], strings.TrimSpace(m[2])
		if i := strings.Index(value, " #"); i >= 0 && !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, "'") {
			value = strings.TrimSpace(value[:i])
		}
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		var unresolved string
		value = pinRef.ReplaceAllStringFunc(value, func(ref string) string {
			name := strings.Trim(ref, "${}")
			v, ok := all[name]
			if !ok {
				unresolved = name
			}
			return v
		})
		if unresolved != "" {
			return Pins{}, fmt.Errorf("line %d: %s references %s, which is not defined above it", n, key, unresolved)
		}
		all[key] = value
	}
	if err := sc.Err(); err != nil {
		return Pins{}, fmt.Errorf("scan: %w", err)
	}
	if len(all) == 0 {
		return Pins{}, errNoPins
	}
	p := Pins{All: all}
	for _, key := range []string{"STRIMZI_VERSION", "STRIMZI_KAFKA_VERSION", "KAFKA_IMAGE"} {
		if all[key] == "" {
			return Pins{}, fmt.Errorf("%s is not set", key)
		}
	}
	p.StrimziVersion = all["STRIMZI_VERSION"]
	p.KafkaImage = all["KAFKA_IMAGE"]
	m := kafkaTag.FindStringSubmatch(all["STRIMZI_KAFKA_VERSION"])
	if m == nil {
		return Pins{}, fmt.Errorf("STRIMZI_KAFKA_VERSION %q does not end in -kafka-x.y.z", all["STRIMZI_KAFKA_VERSION"])
	}
	p.KafkaVersion = m[1]
	return p, nil
}
