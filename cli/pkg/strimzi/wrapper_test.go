package strimzi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wrapperChartYAML is shaped like charts/strimzi-operator/Chart.yaml:
// comments, a top-level version that must not move, the dependency block,
// and a "version:" under annotations that must not move either.
const wrapperChartYAML = `apiVersion: v2
name: strimzi-operator
version: 0.1.0
# Tracks the Strimzi operator version this chart deploys.
appVersion: "1.1.0"
description: The Strimzi Kafka Operator — wraps the upstream chart with pinned kates defaults, an owned CRD-upgrade hook, and a strict values schema
kubeVersion: ">=1.27.0-0"
home: https://github.com/bmscomp/kates
sources:
  - https://github.com/bmscomp/kates
keywords:
  - kafka
  - strimzi
  - operator
  - kraft
  - kates
maintainers:
  - name: bmscomp
    url: https://github.com/bmscomp
dependencies:
  - name: strimzi-kafka-operator
    version: 1.1.0
    repository: oci://quay.io/strimzi-helm
annotations:
  artifacthub.io/category: streaming-messaging
  artifacthub.io/license: Apache-2.0
  artifacthub.io/changes: |
    - kind: added
      description: Initial chart — extracts the Strimzi operator install from ad-hoc helm calls
    - kind: fixed
      description: leaderElection.enable is now the honored key (version: 1.1.0 is not a dependency)
`

// makeWrapperRepo lays out a repository root with a minimal wrapper chart.
func makeWrapperRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "charts", "strimzi-operator")
	writeFile(t, filepath.Join(dir, "Chart.yaml"), wrapperChartYAML)
	writeFile(t, filepath.Join(dir, "Chart.lock"), "dependencies:\n- name: strimzi-kafka-operator\n  version: 1.1.0\ndigest: sha256:abc\n")
	writeFile(t, filepath.Join(dir, "charts", "strimzi-kafka-operator-1.1.0.tgz"), "old vendored tarball")
	writeFile(t, filepath.Join(dir, "values.yaml"), "strimziVersion: \"1.1.0\"\nstrimzi-kafka-operator:\n  watchAnyNamespace: true\n")
	writeFile(t, filepath.Join(dir, "values.schema.json"), "{}\n")
	writeFile(t, filepath.Join(dir, ".helmignore"), ".DS_Store\n")
	writeFile(t, filepath.Join(dir, "templates", "_helpers.tpl"), "{{- define \"x\" -}}x{{- end -}}\n")
	writeFile(t, filepath.Join(dir, "templates", "hooks", "crd-upgrade.yaml"), "kind: Job\n")
	return root
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func chart101Tgz(t *testing.T) string {
	t.Helper()
	files := chart110Files()
	files["Chart.yaml"] = chartYAMLFor("1.0.1", "1.0.1")
	files["values.yaml"] = valuesYAMLFor("1.0.1")
	files["templates/_kafka_image_map.tpl"] = imageMapFor("4.1.0", "4.1.1", "4.1.2", "4.2.0")
	return buildChartTgzNamed(t, filepath.Join(t.TempDir(), "strimzi-kafka-operator-1.0.1.tgz"), ChartName, files)
}

func TestWrapperFor(t *testing.T) {
	root := makeWrapperRepo(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	tgz := chart101Tgz(t)

	dir, err := WrapperFor(root, cacheDir, "1.0.1", tgz)
	if err != nil {
		t.Fatalf("WrapperFor: %v", err)
	}
	if want := filepath.Join(cacheDir, "wrapper", "1.0.1"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	wantTree := []string{
		".helmignore",
		"Chart.yaml",
		"charts/strimzi-kafka-operator-1.0.1.tgz",
		"templates/_helpers.tpl",
		"templates/hooks/crd-upgrade.yaml",
		"values.schema.json",
		"values.yaml",
	}
	if got := listTree(t, dir); !slices.Equal(got, wantTree) {
		t.Errorf("tree = %v, want %v", got, wantTree)
	}
	if readFile(t, filepath.Join(dir, "charts", "strimzi-kafka-operator-1.0.1.tgz")) != readFile(t, tgz) {
		t.Error("charts/ tarball is not a copy of the pulled chart")
	}
	src := filepath.Join(root, "charts", "strimzi-operator")
	for _, f := range []string{"values.yaml", "values.schema.json", ".helmignore", "templates/_helpers.tpl", "templates/hooks/crd-upgrade.yaml"} {
		if readFile(t, filepath.Join(dir, f)) != readFile(t, filepath.Join(src, f)) {
			t.Errorf("%s differs from the wrapper's", f)
		}
	}

	// Chart.yaml: exactly the two version lines change
	got := strings.Split(readFile(t, filepath.Join(dir, "Chart.yaml")), "\n")
	want := strings.Split(wrapperChartYAML, "\n")
	if len(got) != len(want) {
		t.Fatalf("Chart.yaml has %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	var changed []string
	for i := range want {
		if got[i] != want[i] {
			changed = append(changed, got[i])
		}
	}
	if !slices.Equal(changed, []string{`appVersion: "1.0.1"`, `    version: 1.0.1`}) {
		t.Errorf("changed lines = %q", changed)
	}

	t.Run("idempotent", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "charts", "stale.tgz"), "stale")
		writeFile(t, filepath.Join(dir, "Chart.lock"), "stale")
		again, err := WrapperFor(root, cacheDir, "1.0.1", tgz)
		if err != nil {
			t.Fatalf("WrapperFor again: %v", err)
		}
		if again != dir {
			t.Errorf("dir = %q, want %q", again, dir)
		}
		if got := listTree(t, dir); !slices.Equal(got, wantTree) {
			t.Errorf("tree after rerun = %v", got)
		}
	})

	t.Run("version and chart must agree", func(t *testing.T) {
		_, err := WrapperFor(root, cacheDir, "1.0.0", tgz)
		if err == nil || !strings.Contains(err.Error(), "not 1.0.0") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no wrapper chart in repo", func(t *testing.T) {
		if _, err := WrapperFor(t.TempDir(), cacheDir, "1.0.1", tgz); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("unsafe version", func(t *testing.T) {
		if _, err := WrapperFor(root, cacheDir, "../1.0.1", tgz); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("wrapper without the dependency", func(t *testing.T) {
		root := makeWrapperRepo(t)
		writeFile(t, filepath.Join(root, "charts", "strimzi-operator", "Chart.yaml"), "apiVersion: v2\nname: strimzi-operator\nversion: 0.1.0\nappVersion: \"1.1.0\"\n")
		_, err := WrapperFor(root, filepath.Join(t.TempDir(), "c"), "1.0.1", tgz)
		if err == nil || !strings.Contains(err.Error(), "dependency strimzi-kafka-operator") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestWrapperForRealRepository(t *testing.T) {
	if _, err := os.Stat(vendoredChart); err != nil {
		t.Skip("vendored chart not found")
	}
	root := "../../.."
	cacheDir := t.TempDir()
	dir, err := WrapperFor(root, cacheDir, "1.1.0", vendoredChart)
	if err != nil {
		t.Fatalf("WrapperFor: %v", err)
	}
	// rewriting the pin to itself leaves Chart.yaml byte-identical
	if readFile(t, filepath.Join(dir, "Chart.yaml")) != readFile(t, filepath.Join(root, "charts", "strimzi-operator", "Chart.yaml")) {
		t.Error("Chart.yaml changed although the version is the pin")
	}
	if _, err := os.Stat(filepath.Join(dir, "Chart.lock")); err == nil {
		t.Error("Chart.lock was copied")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "charts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "strimzi-kafka-operator-1.1.0.tgz" {
		t.Errorf("charts/ = %v", entries)
	}
	for _, f := range []string{"values.yaml", "values.schema.json", "templates"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing from the wrapper copy", f)
		}
	}
}

func TestRewriteWrapperChartYAML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "quoted appVersion, bare dependency version, comments kept",
			in:   "name: w\nversion: 0.1.0\n# keep me\nappVersion: \"1.1.0\"\ndependencies:\n  - name: other\n    version: 9.9.9\n  - name: strimzi-kafka-operator\n    version: 1.1.0 # the pin\n    repository: oci://quay.io/strimzi-helm\nannotations:\n  version: 1.1.0\n",
			want: "name: w\nversion: 0.1.0\n# keep me\nappVersion: \"1.0.1\"\ndependencies:\n  - name: other\n    version: 9.9.9\n  - name: strimzi-kafka-operator\n    version: 1.0.1 # the pin\n    repository: oci://quay.io/strimzi-helm\nannotations:\n  version: 1.1.0\n",
		},
		{
			name: "single quotes and quoted dependency name",
			in:   "appVersion: '1.1.0'\ndependencies:\n- name: \"strimzi-kafka-operator\"\n  version: \"1.1.0\"\n",
			want: "appVersion: '1.0.1'\ndependencies:\n- name: \"strimzi-kafka-operator\"\n  version: \"1.0.1\"\n",
		},
		{
			name: "bare appVersion and CRLF",
			in:   "appVersion: 1.1.0\r\ndependencies:\r\n  - name: strimzi-kafka-operator\r\n    version: 1.1.0\r\n",
			want: "appVersion: 1.0.1\r\ndependencies:\r\n  - name: strimzi-kafka-operator\r\n    version: 1.0.1\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriteWrapperChartYAML([]byte(tt.in), "1.0.1")
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}

	errs := map[string]string{
		"no appVersion":        "dependencies:\n  - name: strimzi-kafka-operator\n    version: 1.1.0\n",
		"no dependency":        "appVersion: 1.1.0\ndependencies:\n  - name: other\n    version: 1.1.0\n",
		"dependency elsewhere": "appVersion: 1.1.0\nother:\n  - name: strimzi-kafka-operator\n    version: 1.1.0\n",
	}
	for name, in := range errs {
		t.Run(name, func(t *testing.T) {
			if _, err := rewriteWrapperChartYAML([]byte(in), "1.0.1"); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
