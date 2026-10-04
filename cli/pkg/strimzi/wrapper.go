package strimzi

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WrapperChartDir is the wrapper chart's directory under the repository
// root.
const WrapperChartDir = "charts/strimzi-operator"

// WrapperFor generates the per-version copy of charts/strimzi-operator the
// CLI installs a non-pinned operator through, at <cacheDir>/wrapper/<version>:
// the wrapper's files except Chart.lock and charts/, its Chart.yaml with
// appVersion and the strimzi-kafka-operator dependency version rewritten to
// version (line-wise, every other line and comment kept), and chartTgz —
// which must be that version's chart — copied into charts/. Re-running
// replaces the directory.
func WrapperFor(repoRoot, cacheDir, version, chartTgz string) (string, error) {
	if err := checkVersionSegment(version); err != nil {
		return "", err
	}
	src := filepath.Join(repoRoot, filepath.FromSlash(WrapperChartDir))
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return "", fmt.Errorf("wrapper chart %s: not a directory", src)
	}
	c, err := ReadChart(chartTgz)
	if err != nil {
		return "", err
	}
	if c.Version != version {
		return "", fmt.Errorf("%s is %s %s, not %s", chartTgz, ChartName, c.Version, version)
	}

	dir := filepath.Join(cacheDir, "wrapper", version)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear %s: %w", dir, err)
	}
	if err := copyTree(src, dir, func(rel string) bool {
		return rel == "Chart.lock" || rel == "charts" || strings.HasPrefix(rel, "charts"+string(filepath.Separator))
	}); err != nil {
		return "", err
	}

	chartYAML := filepath.Join(dir, "Chart.yaml")
	data, err := os.ReadFile(chartYAML)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", chartYAML, err)
	}
	rewritten, err := rewriteWrapperChartYAML(data, version)
	if err != nil {
		return "", fmt.Errorf("%s: %w", chartYAML, err)
	}
	if err := os.WriteFile(chartYAML, rewritten, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", chartYAML, err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "charts"), 0o755); err != nil {
		return "", fmt.Errorf("create charts dir: %w", err)
	}
	if err := copyFile(chartTgz, filepath.Join(dir, "charts", ChartFileName(version))); err != nil {
		return "", err
	}
	return dir, nil
}

var (
	topLevelKeyRe = regexp.MustCompile(`^[A-Za-z_][^:]*:`)
	appVersionRe  = regexp.MustCompile(`^(appVersion:\s*)(.*)$`)
	depNameRe     = regexp.MustCompile(`^(\s*-\s*name:\s*)(.*)$`)
	depVersionRe  = regexp.MustCompile(`^(\s*version:\s*)(.*)$`)
	// scalarRe splits a YAML scalar into its quotes, value and trailing
	// comment so the value can be replaced with everything else kept.
	scalarRe = regexp.MustCompile(`^(["']?)([^"'#\s]*)(["']?)(\s*(?:#.*)?)\s*$`)
)

// rewriteWrapperChartYAML sets appVersion and the strimzi-kafka-operator
// dependency's version in the wrapper's Chart.yaml, touching nothing else.
func rewriteWrapperChartYAML(src []byte, version string) ([]byte, error) {
	lines := strings.Split(string(src), "\n")
	inDeps, inDep, appDone, depDone := false, false, false, false
	for i, line := range lines {
		switch {
		case topLevelKeyRe.MatchString(line):
			inDeps, inDep = strings.HasPrefix(line, "dependencies:"), false
			if m := appVersionRe.FindStringSubmatch(line); m != nil {
				lines[i] = m[1] + replaceScalar(m[2], version)
				appDone = true
			}
		case inDeps && depNameRe.MatchString(line):
			name := strings.Trim(strings.TrimSpace(depNameRe.FindStringSubmatch(line)[2]), `"'`)
			inDep = name == ChartName
		case inDeps && inDep && depVersionRe.MatchString(line):
			m := depVersionRe.FindStringSubmatch(line)
			lines[i] = m[1] + replaceScalar(m[2], version)
			depDone, inDep = true, false
		}
	}
	if !appDone {
		return nil, errors.New("no appVersion line")
	}
	if !depDone {
		return nil, fmt.Errorf("no version line for dependency %s", ChartName)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// replaceScalar replaces the value of a YAML scalar, keeping its quoting
// style and any trailing comment.
func replaceScalar(rest, value string) string {
	m := scalarRe.FindStringSubmatch(rest)
	if m == nil {
		return value
	}
	open, closing := m[1], m[3]
	if open != "" && closing == "" {
		closing = open
	}
	return open + value + closing + m[4]
}

// copyTree copies the regular files and directories under src to dst,
// skipping any entry for which skip(rel) is true.
func copyTree(src, dst string, skip func(rel string) bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return nil
		}
	})
}

// copyFile copies a regular file, keeping its permission bits.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}
