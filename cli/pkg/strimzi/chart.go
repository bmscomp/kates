package strimzi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

const (
	// ChartName is the upstream operator chart's name, and the name of the
	// wrapper chart's dependency.
	ChartName = "strimzi-kafka-operator"
	// KafkaCRDName is the Kafka custom resource definition whose served and
	// stored API versions decide a chart's CRD generation.
	KafkaCRDName = "kafkas.kafka.strimzi.io"

	// maxEntrySize caps how much of one tarball entry is read.
	maxEntrySize = 16 << 20
)

// Chart is what a strimzi-kafka-operator chart tarball says about itself,
// read from its files without rendering a template.
type Chart struct {
	// Version is the chart version, which Strimzi keeps equal to the
	// operator version (Chart.yaml version and appVersion).
	Version string
	// Window is the Kafka versions the operator runs
	// (templates/_kafka_image_map.tpl, STRIMZI_KAFKA_IMAGES).
	Window kafkaversion.Window
	// Served lists the API versions the Kafka CRD serves, in CRD order.
	Served []string
	// Stored is the API version the Kafka CRD stores.
	Stored string
	// ImageTag is values.yaml defaultImageTag: the operator image tag the
	// chart installs by default.
	ImageTag string
	// Path is the tarball the chart was read from.
	Path string
}

// chartFiles is what ReadChart picks out of the tarball.
type chartFiles struct {
	chartYAML, imageMap, valuesYAML []byte
	crds                            map[string][]byte
}

// ReadChart reads an operator chart tarball (as helm pull produces it, with
// every entry under a top-level chart directory) and returns its version,
// Kafka window, Kafka CRD API versions and default image tag. Only files are
// read; no template is rendered.
func ReadChart(tgzPath string) (*Chart, error) {
	files, err := readChartFiles(tgzPath)
	if err != nil {
		return nil, err
	}
	c := &Chart{Path: tgzPath}
	if c.Version, err = parseChartVersion(files.chartYAML); err != nil {
		return nil, fmt.Errorf("%s: %w", tgzPath, err)
	}
	if files.imageMap == nil {
		return nil, fmt.Errorf("%s: no templates/_kafka_image_map.tpl in archive", tgzPath)
	}
	if c.Window, err = kafkaversion.ParseImageMap(string(files.imageMap)); err != nil {
		return nil, fmt.Errorf("%s: templates/_kafka_image_map.tpl: %w", tgzPath, err)
	}
	if c.Served, c.Stored, err = parseKafkaCRD(files.crds); err != nil {
		return nil, fmt.Errorf("%s: %w", tgzPath, err)
	}
	if c.ImageTag, err = parseDefaultImageTag(files.valuesYAML); err != nil {
		return nil, fmt.Errorf("%s: values.yaml: %w", tgzPath, err)
	}
	return c, nil
}

func readChartFiles(tgzPath string) (*chartFiles, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return nil, fmt.Errorf("open chart: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: not a gzip archive: %w", tgzPath, err)
	}
	defer gz.Close()

	files := &chartFiles{crds: map[string][]byte{}}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: read archive: %w", tgzPath, err)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		rel := chartRelPath(hdr.Name)
		if !files.wants(rel) {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxEntrySize))
		if err != nil {
			return nil, fmt.Errorf("%s: read %s: %w", tgzPath, hdr.Name, err)
		}
		files.put(rel, data)
	}
	if files.chartYAML == nil {
		return nil, fmt.Errorf("%s: no Chart.yaml in archive", tgzPath)
	}
	return files, nil
}

func (f *chartFiles) wants(rel string) bool {
	switch rel {
	case "Chart.yaml", "templates/_kafka_image_map.tpl", "values.yaml":
		return true
	}
	return strings.HasPrefix(rel, "crds/") && isYAML(rel)
}

func (f *chartFiles) put(rel string, data []byte) {
	switch rel {
	case "Chart.yaml":
		f.chartYAML = data
	case "templates/_kafka_image_map.tpl":
		f.imageMap = data
	case "values.yaml":
		f.valuesYAML = data
	default:
		f.crds[rel] = data
	}
}

// chartRelPath strips the top-level chart directory ("strimzi-kafka-operator/")
// and any leading "./" from a tarball entry name.
func chartRelPath(name string) string {
	name = path.Clean(strings.TrimPrefix(name, "./"))
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return ""
}

func isYAML(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// parseChartVersion reads Chart.yaml, checks it is the operator chart and
// that version and appVersion agree (the platform relies on the agreement:
// the wrapper's appVersion, the CRD bundle URL and helm's APP VERSION all
// follow the chart version).
func parseChartVersion(chartYAML []byte) (string, error) {
	var meta struct {
		Name       string `yaml:"name"`
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(chartYAML, &meta); err != nil {
		return "", fmt.Errorf("Chart.yaml: %w", err)
	}
	if meta.Name != ChartName {
		return "", fmt.Errorf("Chart.yaml: not a %s chart (name: %q)", ChartName, meta.Name)
	}
	if meta.Version == "" {
		return "", errors.New("Chart.yaml: no version")
	}
	if meta.AppVersion != "" && meta.AppVersion != meta.Version {
		return "", fmt.Errorf("Chart.yaml: version %s and appVersion %s disagree", meta.Version, meta.AppVersion)
	}
	return meta.Version, nil
}

// parseKafkaCRD finds the Kafka CRD among the crds/ files and returns the
// API versions it serves and the one it stores.
func parseKafkaCRD(crds map[string][]byte) (served []string, stored string, err error) {
	type crd struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Versions []struct {
				Name    string `yaml:"name"`
				Served  bool   `yaml:"served"`
				Storage bool   `yaml:"storage"`
			} `yaml:"versions"`
		} `yaml:"spec"`
	}
	names := make([]string, 0, len(crds))
	for name := range crds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := crds[name]
		if !bytes.Contains(data, []byte(KafkaCRDName)) {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var doc crd
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, "", fmt.Errorf("crds/%s: %w", path.Base(name), err)
			}
			if doc.Metadata.Name != KafkaCRDName {
				continue
			}
			for _, v := range doc.Spec.Versions {
				if v.Served {
					served = append(served, v.Name)
				}
				if v.Storage {
					stored = v.Name
				}
			}
			if stored == "" {
				return nil, "", fmt.Errorf("crds/%s: %s has no storage version", path.Base(name), KafkaCRDName)
			}
			return served, stored, nil
		}
	}
	return nil, "", fmt.Errorf("no %s CRD under crds/", KafkaCRDName)
}

// parseDefaultImageTag reads values.yaml defaultImageTag; a chart without a
// values.yaml or without the key yields "".
func parseDefaultImageTag(valuesYAML []byte) (string, error) {
	if valuesYAML == nil {
		return "", nil
	}
	var values struct {
		DefaultImageTag string `yaml:"defaultImageTag"`
	}
	if err := yaml.Unmarshal(valuesYAML, &values); err != nil {
		return "", err
	}
	return values.DefaultImageTag, nil
}

// VendoredChartPath returns the operator chart tarball vendored under
// charts/strimzi-operator/charts/ — the pin. It is an error to find none or
// more than one.
func VendoredChartPath(repoRoot string) (string, error) {
	dir := filepath.Join(repoRoot, "charts", "strimzi-operator", "charts")
	matches, err := filepath.Glob(filepath.Join(dir, "*.tgz"))
	if err != nil {
		return "", fmt.Errorf("list %s: %w", dir, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no chart tarball under %s (run helm dependency build charts/strimzi-operator)", dir)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%d chart tarballs under %s, want exactly one: %s", len(matches), dir, strings.Join(baseNames(matches), " "))
	}
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// Generation is the CRD API generation a chart belongs to: the API version
// its Kafka CRD stores — "v1" for Strimzi ≥ 1.0.0, "v1beta2" for 0.x. Two
// operators can share a cluster only within one generation (the CRDs are one
// set per cluster, owned by the newest operator).
func Generation(c *Chart) string {
	switch c.Stored {
	case "v1":
		return "v1"
	case "v1beta2":
		return "v1beta2"
	default:
		return c.Stored
	}
}

// FloorCheck is the verdict on a chart as the primary's operator.
type FloorCheck struct {
	OK bool
	// Reason says why not, in the words the CLI prints; empty when OK.
	Reason string
}

// CheckPrimaryFloor decides whether a chart can be the primary's operator:
// its Kafka CRD must serve v1 (the only API this platform renders) and its
// window must contain a Kafka at or above kafkaFloor (the platform's
// declared floor, 4.2.0 today because the primary's configuration needs
// share groups). Both shortfalls are reported when both apply.
func CheckPrimaryFloor(c *Chart, kafkaFloor kafkaversion.Version) FloorCheck {
	var reasons []string
	if r := servesV1(c); r != "" {
		reasons = append(reasons, r)
	}
	if !slices.ContainsFunc(c.Window, func(v kafkaversion.Version) bool { return v.AtLeast(kafkaFloor) }) {
		reasons = append(reasons, fmt.Sprintf("no Kafka >= %s in its window (%s)", kafkaFloor, windowOrNone(c.Window)))
	}
	return FloorCheck{OK: len(reasons) == 0, Reason: strings.Join(reasons, "; ")}
}

// servesV1 returns the refusal text when the chart's Kafka CRD serves no v1.
func servesV1(c *Chart) string {
	if slices.Contains(c.Served, "v1") {
		return ""
	}
	served := strings.Join(c.Served, " ")
	if served == "" {
		served = "none"
	}
	return fmt.Sprintf("serves no v1 API (served: %s)", served)
}

func windowOrNone(w kafkaversion.Window) string {
	if len(w) == 0 {
		return "none"
	}
	return w.String()
}

// Eligibility is the verdict on a chart as an additional, namespace-scoped
// operator beside the primary's.
type Eligibility struct {
	OK bool
	// Adjacent is true when the candidate is at most one minor release
	// behind the primary (the only concurrent configuration Strimzi tests);
	// patch levels do not count.
	Adjacent bool
	// Reason says why not eligible, in the words the CLI prints; empty when
	// OK.
	Reason string
}

// EligibleAdditional applies the rules for a second operator: it must serve
// v1, share the primary's CRD generation, and be no newer than the primary's
// operator. Adjacent reports whether the pair is one Strimzi tests.
func EligibleAdditional(candidate, primary *Chart) Eligibility {
	var reasons []string
	if r := servesV1(candidate); r != "" {
		reasons = append(reasons, r)
	}
	if cg, pg := Generation(candidate), Generation(primary); cg != pg {
		reasons = append(reasons, fmt.Sprintf("different CRD generation (%s vs %s)", cg, pg))
	}
	cv, err := kafkaversion.Parse(candidate.Version)
	if err != nil {
		reasons = append(reasons, fmt.Sprintf("unparseable chart version %q", candidate.Version))
	}
	pv, err := kafkaversion.Parse(primary.Version)
	if err != nil {
		reasons = append(reasons, fmt.Sprintf("unparseable primary chart version %q", primary.Version))
	}
	if len(reasons) > 0 {
		return Eligibility{Reason: strings.Join(reasons, "; ")}
	}
	if pv.Less(cv) {
		return Eligibility{Reason: fmt.Sprintf("newer than the primary's operator (%s > %s)", cv, pv)}
	}
	adjacent := cv.Major == pv.Major && pv.Minor-cv.Minor <= 1
	return Eligibility{OK: true, Adjacent: adjacent}
}
