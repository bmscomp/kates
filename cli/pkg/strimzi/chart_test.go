package strimzi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

func TestReadChartSynthetic110(t *testing.T) {
	path := buildChartTgz(t, chart110Files())
	c, err := ReadChart(path)
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if c.Version != "1.1.0" {
		t.Errorf("Version = %q", c.Version)
	}
	if got := c.Window.Strings(); !slices.Equal(got, []string{"4.2.0", "4.2.1", "4.3.0"}) {
		t.Errorf("Window = %v", got)
	}
	if !slices.Equal(c.Served, []string{"v1"}) || c.Stored != "v1" {
		t.Errorf("Served = %v, Stored = %q", c.Served, c.Stored)
	}
	if c.ImageTag != "1.1.0" {
		t.Errorf("ImageTag = %q", c.ImageTag)
	}
	if c.Path != path {
		t.Errorf("Path = %q, want %q", c.Path, path)
	}
	if got := Generation(c); got != "v1" {
		t.Errorf("Generation = %q", got)
	}
}

func TestReadChartSynthetic051(t *testing.T) {
	c, err := ReadChart(buildChartTgz(t, chart051Files()))
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if c.Version != "0.51.0" || c.ImageTag != "0.51.0" {
		t.Errorf("Version = %q, ImageTag = %q", c.Version, c.ImageTag)
	}
	if got := c.Window.Strings(); !slices.Equal(got, []string{"4.1.0", "4.1.1", "4.2.0"}) {
		t.Errorf("Window = %v", got)
	}
	if !slices.Equal(c.Served, []string{"v1beta2", "v1"}) || c.Stored != "v1beta2" {
		t.Errorf("Served = %v, Stored = %q", c.Served, c.Stored)
	}
	if got := Generation(c); got != "v1beta2" {
		t.Errorf("Generation = %q", got)
	}
}

func TestReadChartVendored(t *testing.T) {
	if _, err := os.Stat(vendoredChart); err != nil {
		t.Skipf("vendored chart not found: %v", err)
	}
	c, err := ReadChart(vendoredChart)
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if c.Version != "1.1.0" {
		t.Errorf("Version = %q, want 1.1.0", c.Version)
	}
	if got := c.Window.Strings(); !slices.Equal(got, []string{"4.2.0", "4.2.1", "4.3.0"}) {
		t.Errorf("Window = %v, want [4.2.0 4.2.1 4.3.0]", got)
	}
	if !slices.Equal(c.Served, []string{"v1"}) {
		t.Errorf("Served = %v, want [v1]", c.Served)
	}
	if c.Stored != "v1" {
		t.Errorf("Stored = %q, want v1", c.Stored)
	}
	if c.ImageTag != "1.1.0" {
		t.Errorf("ImageTag = %q, want 1.1.0", c.ImageTag)
	}
	if fc := CheckPrimaryFloor(c, kafkaversion.MustParse("4.2.0")); !fc.OK {
		t.Errorf("CheckPrimaryFloor(pin) = %+v", fc)
	}
}

func TestReadChartVariants(t *testing.T) {
	t.Run("entries with ./ prefix and other chart directory name", func(t *testing.T) {
		path := buildChartTgzNamed(t, filepath.Join(t.TempDir(), "x.tgz"), "./mirror-of-operator", chart110Files())
		c, err := ReadChart(path)
		if err != nil {
			t.Fatalf("ReadChart: %v", err)
		}
		if c.Version != "1.1.0" || len(c.Window) != 3 {
			t.Errorf("chart = %+v", c)
		}
	})
	t.Run("kafka CRD under another file name", func(t *testing.T) {
		files := chart110Files()
		files["crds/kafka.yml"] = files["crds/040-Crd-kafka.yaml"]
		delete(files, "crds/040-Crd-kafka.yaml")
		c, err := ReadChart(buildChartTgz(t, files))
		if err != nil {
			t.Fatalf("ReadChart: %v", err)
		}
		if c.Stored != "v1" {
			t.Errorf("Stored = %q", c.Stored)
		}
	})
	t.Run("no appVersion is tolerated", func(t *testing.T) {
		files := chart110Files()
		files["Chart.yaml"] = "apiVersion: v2\nname: strimzi-kafka-operator\nversion: 1.1.0\n"
		if _, err := ReadChart(buildChartTgz(t, files)); err != nil {
			t.Fatalf("ReadChart: %v", err)
		}
	})
	t.Run("no values.yaml gives an empty tag", func(t *testing.T) {
		files := chart110Files()
		delete(files, "values.yaml")
		c, err := ReadChart(buildChartTgz(t, files))
		if err != nil {
			t.Fatalf("ReadChart: %v", err)
		}
		if c.ImageTag != "" {
			t.Errorf("ImageTag = %q", c.ImageTag)
		}
	})
}

func TestReadChartErrors(t *testing.T) {
	broken := func(mutate func(files map[string]string)) string {
		files := chart110Files()
		mutate(files)
		return buildChartTgz(t, files)
	}
	notGzip := filepath.Join(t.TempDir(), "plain.tgz")
	writeFile(t, notGzip, "not a tarball")

	tests := map[string]struct {
		path string
		want string
	}{
		"missing file":  {path: filepath.Join(t.TempDir(), "nope.tgz"), want: "open chart"},
		"not gzip":      {path: notGzip, want: "not a gzip archive"},
		"no Chart.yaml": {path: broken(func(f map[string]string) { delete(f, "Chart.yaml") }), want: "no Chart.yaml"},
		"other chart": {path: broken(func(f map[string]string) {
			f["Chart.yaml"] = "apiVersion: v2\nname: strimzi-drain-cleaner\nversion: 1.1.0\n"
		}), want: "not a strimzi-kafka-operator chart"},
		"no version": {path: broken(func(f map[string]string) {
			f["Chart.yaml"] = "apiVersion: v2\nname: strimzi-kafka-operator\n"
		}), want: "no version"},
		"version and appVersion disagree": {path: broken(func(f map[string]string) {
			f["Chart.yaml"] = chartYAMLFor("1.1.0", "1.0.1")
		}), want: "disagree"},
		"no image map": {path: broken(func(f map[string]string) { delete(f, "templates/_kafka_image_map.tpl") }), want: "_kafka_image_map.tpl"},
		"empty map":    {path: broken(func(f map[string]string) { f["templates/_kafka_image_map.tpl"] = "nothing here" }), want: "STRIMZI_KAFKA_IMAGES"},
		"no kafka CRD": {path: broken(func(f map[string]string) { delete(f, "crds/040-Crd-kafka.yaml") }), want: KafkaCRDName},
		"no storage": {path: broken(func(f map[string]string) {
			f["crds/040-Crd-kafka.yaml"] = kafkaCRDFor(KafkaCRDName, crdVersion{"v1", true, false})
		}), want: "no storage version"},
		"bad CRD yaml":   {path: broken(func(f map[string]string) { f["crds/040-Crd-kafka.yaml"] = "metadata: [\n  name: " + KafkaCRDName }), want: "crds/040-Crd-kafka.yaml"},
		"bad values":     {path: broken(func(f map[string]string) { f["values.yaml"] = "defaultImageTag: [\n" }), want: "values.yaml"},
		"bad chart yaml": {path: broken(func(f map[string]string) { f["Chart.yaml"] = "name: [\n" }), want: "Chart.yaml"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ReadChart(tt.path)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestVendoredChartPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "charts", "strimzi-operator", "charts")
	if _, err := VendoredChartPath(root); err == nil {
		t.Error("no charts dir: want error")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := VendoredChartPath(root); err == nil {
		t.Error("empty charts dir: want error")
	}
	writeFile(t, filepath.Join(dir, "strimzi-kafka-operator-1.1.0.tgz"), "x")
	writeFile(t, filepath.Join(dir, "README.md"), "not a chart")
	got, err := VendoredChartPath(root)
	if err != nil {
		t.Fatalf("VendoredChartPath: %v", err)
	}
	if got != filepath.Join(dir, "strimzi-kafka-operator-1.1.0.tgz") {
		t.Errorf("VendoredChartPath = %q", got)
	}
	writeFile(t, filepath.Join(dir, "strimzi-kafka-operator-1.0.1.tgz"), "y")
	_, err = VendoredChartPath(root)
	if err == nil || !strings.Contains(err.Error(), "2 chart tarballs") {
		t.Errorf("two tarballs: err = %v", err)
	}

	t.Run("real repository", func(t *testing.T) {
		if _, err := os.Stat(vendoredChart); err != nil {
			t.Skip("vendored chart not found")
		}
		got, err := VendoredChartPath("../../..")
		if err != nil {
			t.Fatalf("VendoredChartPath: %v", err)
		}
		if filepath.Base(got) != "strimzi-kafka-operator-1.1.0.tgz" {
			t.Errorf("VendoredChartPath = %q", got)
		}
	})
}

func TestGeneration(t *testing.T) {
	tests := map[string]string{"v1": "v1", "v1beta2": "v1beta2", "v1beta1": "v1beta1", "": ""}
	for stored, want := range tests {
		if got := Generation(&Chart{Stored: stored}); got != want {
			t.Errorf("Generation(%q) = %q, want %q", stored, got, want)
		}
	}
}

func window(t *testing.T, vs ...string) kafkaversion.Window {
	t.Helper()
	out := make([]kafkaversion.Version, len(vs))
	for i, v := range vs {
		out[i] = kafkaversion.MustParse(v)
	}
	return kafkaversion.NewWindow(out...)
}

func TestCheckPrimaryFloor(t *testing.T) {
	floor := kafkaversion.MustParse("4.2.0")
	tests := []struct {
		name   string
		chart  *Chart
		ok     bool
		reason string
	}{
		{name: "1.1.0", chart: &Chart{Version: "1.1.0", Served: []string{"v1"}, Stored: "v1", Window: window(t, "4.2.0", "4.2.1", "4.3.0")}, ok: true},
		{name: "1.0.1", chart: &Chart{Version: "1.0.1", Served: []string{"v1"}, Stored: "v1", Window: window(t, "4.1.0", "4.1.1", "4.1.2", "4.2.0")}, ok: true},
		{name: "0.51.0 serves v1 and reaches the floor", chart: &Chart{Version: "0.51.0", Served: []string{"v1beta2", "v1"}, Stored: "v1beta2", Window: window(t, "4.1.0", "4.1.1", "4.2.0")}, ok: true},
		{name: "0.50.0 window below the floor", chart: &Chart{Version: "0.50.0", Served: []string{"v1beta2", "v1"}, Stored: "v1beta2", Window: window(t, "4.0.0", "4.0.1", "4.1.0", "4.1.1")}, reason: "no Kafka >= 4.2.0 in its window (4.0.0 4.0.1 4.1.0 4.1.1)"},
		{name: "0.48.0 serves no v1", chart: &Chart{Version: "0.48.0", Served: []string{"v1beta2"}, Stored: "v1beta2", Window: window(t, "4.0.0", "4.1.0")}, reason: "serves no v1 API (served: v1beta2); no Kafka >= 4.2.0 in its window (4.0.0 4.1.0)"},
		{name: "serves no v1 but window fine", chart: &Chart{Served: []string{"v1beta2"}, Stored: "v1beta2", Window: window(t, "4.2.0")}, reason: "serves no v1 API (served: v1beta2)"},
		{name: "empty chart", chart: &Chart{}, reason: "serves no v1 API (served: none); no Kafka >= 4.2.0 in its window (none)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckPrimaryFloor(tt.chart, floor)
			if got.OK != tt.ok || got.Reason != tt.reason {
				t.Errorf("CheckPrimaryFloor = %+v, want OK=%v Reason=%q", got, tt.ok, tt.reason)
			}
		})
	}

	t.Run("floor moves with the platform", func(t *testing.T) {
		c := &Chart{Served: []string{"v1"}, Stored: "v1", Window: window(t, "4.2.0", "4.2.1", "4.3.0")}
		if got := CheckPrimaryFloor(c, kafkaversion.MustParse("4.4.0")); got.OK {
			t.Errorf("floor 4.4.0 should refuse the pin: %+v", got)
		}
	})
}

func TestEligibleAdditional(t *testing.T) {
	primary := &Chart{Version: "1.1.0", Served: []string{"v1"}, Stored: "v1"}
	tests := []struct {
		name      string
		candidate *Chart
		primary   *Chart
		want      Eligibility
	}{
		{name: "1.0.1 beside 1.1.0", candidate: &Chart{Version: "1.0.1", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: true}},
		{name: "1.0.0 beside 1.1.0", candidate: &Chart{Version: "1.0.0", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: true}},
		{name: "same version", candidate: &Chart{Version: "1.1.0", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: true}},
		{name: "1.1.0 beside 1.1.1 (patch ignored)", candidate: &Chart{Version: "1.1.0", Served: []string{"v1"}, Stored: "v1"}, primary: &Chart{Version: "1.1.1", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: true}},
		{name: "1.0.1 beside 1.2.0: two behind", candidate: &Chart{Version: "1.0.1", Served: []string{"v1"}, Stored: "v1"}, primary: &Chart{Version: "1.2.0", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: false}},
		{name: "1.1.1 beside 1.1.0: patch newer", candidate: &Chart{Version: "1.1.1", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{Reason: "newer than the primary's operator (1.1.1 > 1.1.0)"}},
		{name: "1.2.0 beside 1.1.0", candidate: &Chart{Version: "1.2.0", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{Reason: "newer than the primary's operator (1.2.0 > 1.1.0)"}},
		{name: "0.51.0 beside 1.1.0", candidate: &Chart{Version: "0.51.0", Served: []string{"v1beta2", "v1"}, Stored: "v1beta2"}, want: Eligibility{Reason: "different CRD generation (v1beta2 vs v1)"}},
		{name: "0.48.0 beside 1.1.0", candidate: &Chart{Version: "0.48.0", Served: []string{"v1beta2"}, Stored: "v1beta2"}, want: Eligibility{Reason: "serves no v1 API (served: v1beta2); different CRD generation (v1beta2 vs v1)"}},
		{name: "0.50.0 beside 0.51.0 primary", candidate: &Chart{Version: "0.50.0", Served: []string{"v1beta2", "v1"}, Stored: "v1beta2"}, primary: &Chart{Version: "0.51.0", Served: []string{"v1beta2", "v1"}, Stored: "v1beta2"}, want: Eligibility{OK: true, Adjacent: true}},
		{name: "1.0.1 beside 2.0.0: other major", candidate: &Chart{Version: "1.0.1", Served: []string{"v1"}, Stored: "v1"}, primary: &Chart{Version: "2.0.0", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{OK: true, Adjacent: false}},
		{name: "unparseable candidate", candidate: &Chart{Version: "latest", Served: []string{"v1"}, Stored: "v1"}, want: Eligibility{Reason: `unparseable chart version "latest"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.primary
			if p == nil {
				p = primary
			}
			if got := EligibleAdditional(tt.candidate, p); got != tt.want {
				t.Errorf("EligibleAdditional = %+v, want %+v", got, tt.want)
			}
		})
	}
}
