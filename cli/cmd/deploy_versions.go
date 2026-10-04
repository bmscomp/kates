package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"gopkg.in/yaml.v3"
)

// Operator scope names, as `--operator-scope` accepts them.
const (
	scopeCluster   = "cluster"
	scopeNamespace = "namespace"
)

// Chart paths the resolution reads. They are relative to the repo root like
// every other chart reference in cmd/ (see cluster_gate.go: "run from the
// repo root").
const (
	operatorChartDir = "charts/strimzi-operator"
	kafkaChartDir    = "charts/kafka-cluster"
	kafkaFloorKey    = "kates.io/kafka-floor"
	operatorNS       = "strimzi-operator"
	strimziV1        = "1.0.0"
)

// primaryCluster is the one Kafka the platform points at. Every install that
// used to spell "krafter" reads it from here.
type primaryCluster struct {
	Name      string
	Namespace string
	Version   kafkaversion.Version
}

// Bootstrap returns the in-cluster plain bootstrap address of the primary.
func (p primaryCluster) Bootstrap(clusterDomain string) string {
	return fmt.Sprintf("%s-kafka-bootstrap.%s.svc.%s:9092", p.Name, p.Namespace, clusterDomain)
}

// ReadySelector is the label selector the operator puts on the cluster's pods.
func (p primaryCluster) ReadySelector() string {
	return "strimzi.io/cluster=" + p.Name
}

// versionPlan is the resolved answer to "which operator, at which version,
// watching what, running which Kafka" — computed before any Helm call, so a
// refusal happens in seconds and --dry-run can show the real facts. See the
// resolution order at the top of §3 of the multi-version plan.
type versionPlan struct {
	Scope          string `json:"scope"`
	StrimziVersion string `json:"strimziVersion"`
	StrimziSource  string `json:"strimziSource"` // pinned | cache | pulled | chart flag | installed
	Pinned         bool   `json:"pinned"`
	// Adopted is true when no version was requested and the operator already
	// on the cluster is newer than the pin, so deploy keeps it.
	Adopted        bool     `json:"adoptedInstalled,omitempty"`
	PinnedVersion  string   `json:"pinnedVersion"`
	ChartPath      string   `json:"chartPath"`
	ChartDir       string   `json:"chartDir"` // the wrapper directory helm installs from
	Window         []string `json:"window"`
	KafkaVersion   string   `json:"kafkaVersion"`
	KafkaDefaulted bool     `json:"kafkaDefaulted"`
	Metadata       string   `json:"metadataVersion"`
	KafkaFloor     string   `json:"kafkaFloor"`
	Watch          []string `json:"watchNamespaces,omitempty"`
	OperatorAction string   `json:"operatorAction"` // install | same | upgrade
	Installed      string   `json:"installedVersion,omitempty"`
	ScopeChange    string   `json:"scopeChange,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	RollingCRs     []string `json:"rollingResources,omitempty"`

	chart     *strimzi.Chart
	kafka     kafkaversion.Version
	operators []strimzi.Operator
	primaryOp *strimzi.Operator
}

// versionOptions carries the deploy flags the resolution needs, so the same
// function serves deploy, --dry-run, the picker and the tests.
type versionOptions struct {
	Scope          string
	StrimziVersion string
	StrimziChart   string
	KafkaVersion   string
	KafkaName      string
	KafkaNS        string
	ConnectNS      string
	WithConnect    bool
	Topology       string
	SingleNS       string
	CacheDir       string
	RepoRoot       string
	Offline        bool // never touch the network (tests, kates versions on a laptop without it)
}

func (o versionOptions) watchList() []string {
	if o.Topology == "single" {
		return []string{o.SingleNS}
	}
	seen := map[string]bool{o.KafkaNS: true}
	list := []string{o.KafkaNS}
	if o.WithConnect && !seen[o.ConnectNS] {
		list = append(list, o.ConnectNS)
	}
	return list
}

// deployVersionOptions builds the options from the deploy flags.
func deployVersionOptions() versionOptions {
	return versionOptions{
		Scope:          deployOperatorScope,
		StrimziVersion: deployStrimziVersion,
		StrimziChart:   deployStrimziChart,
		KafkaVersion:   deployKafkaVersion,
		KafkaName:      deployKafkaName,
		KafkaNS:        deployKafkaNS,
		ConnectNS:      deployConnectNS,
		WithConnect:    deployWithKafkaConnect,
		Topology:       deployTopology,
		SingleNS:       deployNamespace,
		RepoRoot:       ".",
	}
}

// pinnedStrimziVersion reads the wrapper chart's appVersion — the repo's pin.
func pinnedStrimziVersion(repoRoot string) (string, error) {
	var c struct {
		AppVersion string `yaml:"appVersion"`
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, operatorChartDir, "Chart.yaml"))
	if err != nil {
		return "", fmt.Errorf("reading the operator chart pin: %w (run from the repo root)", err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parsing %s/Chart.yaml: %w", operatorChartDir, err)
	}
	if c.AppVersion == "" {
		return "", fmt.Errorf("%s/Chart.yaml has no appVersion", operatorChartDir)
	}
	return c.AppVersion, nil
}

// kafkaFloor reads the platform's own Kafka floor from the kafka-cluster
// chart annotation (§3.7). Missing annotation → no floor beyond the operator's.
func kafkaFloor(repoRoot string) (kafkaversion.Version, error) {
	var c struct {
		Annotations map[string]string `yaml:"annotations"`
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, kafkaChartDir, "Chart.yaml"))
	if err != nil {
		return kafkaversion.Version{}, fmt.Errorf("reading %s/Chart.yaml: %w", kafkaChartDir, err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return kafkaversion.Version{}, err
	}
	raw := strings.TrimSpace(c.Annotations[kafkaFloorKey])
	if raw == "" {
		return kafkaversion.Version{}, nil
	}
	v, err := kafkaversion.Parse(raw)
	if err != nil {
		return kafkaversion.Version{}, fmt.Errorf("annotation %s: %w", kafkaFloorKey, err)
	}
	return v, nil
}

// pinnedOperatorChart returns the pinned operator tarball, fetching it with
// `helm dependency build` when charts/strimzi-operator/charts/ is empty. The
// tarball is a build artifact (charts/*/charts/ is gitignored), so a fresh
// clone has to fetch it once; after that it is read offline.
func pinnedOperatorChart(ctx context.Context, repoRoot string, offline bool) (string, error) {
	path, err := strimzi.VendoredChartPath(repoRoot)
	if err == nil {
		return path, nil
	}
	if offline {
		return "", fmt.Errorf("the pinned operator chart is not fetched yet: run `helm dependency build %s`", operatorChartDir)
	}
	if err := runHelmFn(ctx, "dependency", "build", filepath.Join(repoRoot, operatorChartDir)); err != nil {
		return "", fmt.Errorf("fetching the pinned operator chart: %w", err)
	}
	return strimzi.VendoredChartPath(repoRoot)
}

// resolveVersionPlan runs the resolution order: scope, operator version,
// chart, window and API, Kafka version, installed operators, derived settings.
// It installs nothing. r reaches kubectl/helm; when it is nil the cluster
// steps are skipped (used by `kates versions` without a cluster).
func resolveVersionPlan(ctx context.Context, r strimzi.Runner, o versionOptions) (*versionPlan, error) {
	vp := &versionPlan{}

	// 1. Scope.
	switch o.Scope {
	case "", scopeCluster:
		vp.Scope = scopeCluster
	case scopeNamespace:
		vp.Scope = scopeNamespace
		vp.Watch = o.watchList()
	default:
		return nil, fmt.Errorf("--operator-scope must be %q or %q, got %q", scopeCluster, scopeNamespace, o.Scope)
	}

	// 2. Operator version.
	pin, err := pinnedStrimziVersion(o.RepoRoot)
	if err != nil {
		return nil, err
	}
	vp.PinnedVersion = pin
	cacheDir := o.CacheDir
	if cacheDir == "" {
		if cacheDir, err = strimzi.DefaultCacheDir(); err != nil {
			return nil, err
		}
	}
	requested := strings.TrimSpace(o.StrimziVersion)

	// 2b. What the cluster already runs outranks the pin.
	//
	// The pin is a default, and a default that refuses to deploy is a bug. A
	// cluster running a NEWER operator than this platform release was tested
	// with is an ordinary state — someone ran --strimzi-version latest, or the
	// checkout is older than the cluster — and `kates deploy` with no version
	// flag should keep what is there rather than propose a downgrade nobody
	// asked for and then refuse its own proposal.
	//
	// An explicit --strimzi-version or --strimzi-chart is a request rather
	// than a default, so it is left alone: it reaches the downgrade rail in
	// compareInstalled and is refused there, with the reason.
	if r != nil {
		if err := vp.readInstalled(ctx, r, o); err != nil {
			return nil, err
		}
	}
	if requested == "" && o.StrimziChart == "" && vp.primaryOp != nil {
		if newer, cerr := compareVersions(vp.primaryOp.Version, pin); cerr == nil && newer > 0 {
			requested = vp.primaryOp.Version
			vp.Adopted = true
		}
	}

	// 3. Operator chart.
	path, source, pinned, err := resolveOperatorChart(ctx, r, o, requested, pin, cacheDir)
	if err != nil {
		if vp.Adopted {
			return nil, fmt.Errorf("Strimzi %s is installed, which is newer than this platform's pin (%s), so deploy keeps it — but its chart could not be read: %w\n  Ways forward:\n    - fetch it once:                 helm pull oci://quay.io/strimzi-helm/%s --version %s\n    - or deploy against the pin:     kates clean, then kates deploy (an operator downgrade needs the teardown)",
				vp.primaryOp.Version, pin, err, strimzi.ChartName, vp.primaryOp.Version)
		}
		return nil, err
	}
	vp.ChartPath, vp.StrimziSource, vp.Pinned = path, source, pinned
	if vp.Adopted {
		vp.StrimziSource = "installed"
	}
	chart, err := strimzi.ReadChart(vp.ChartPath)
	if err != nil {
		return nil, fmt.Errorf("reading the operator chart %s: %w", vp.ChartPath, err)
	}
	vp.chart = chart
	vp.StrimziVersion = chart.Version
	vp.Window = chart.Window.Strings()
	if vp.Pinned && chart.Version != pin {
		return nil, fmt.Errorf("the fetched operator chart is %s but the pin is %s: run `helm dependency build %s`", chart.Version, pin, operatorChartDir)
	}

	// 4. Window and API: the floor.
	floor, err := kafkaFloor(o.RepoRoot)
	if err != nil {
		return nil, err
	}
	vp.KafkaFloor = floor.String()
	if fc := strimzi.CheckPrimaryFloor(chart, floor); !fc.OK {
		return nil, fmt.Errorf("Strimzi %s cannot run this platform's primary cluster: %s", chart.Version, fc.Reason)
	}
	if cmp, _ := compareVersions(chart.Version, pin); cmp > 0 {
		if vp.Adopted {
			vp.Notes = append(vp.Notes, fmt.Sprintf("Strimzi %s is already installed and newer than this platform's pin (%s): keeping it, and using its Kafka window. Pass --strimzi-version to choose another; going back to %s needs `kates clean` first.", chart.Version, pin, pin))
		} else {
			vp.Notes = append(vp.Notes, fmt.Sprintf("Strimzi %s is newer than the version this platform release was tested with (%s)", chart.Version, pin))
		}
	}

	// 5. Kafka version.
	if o.KafkaVersion == "" || o.KafkaVersion == "latest" {
		newest, _ := chart.Window.Newest()
		vp.kafka = newest
		vp.KafkaDefaulted = true
	} else {
		v, err := kafkaversion.Parse(o.KafkaVersion)
		if err != nil {
			return nil, fmt.Errorf("--kafka-version %q: expected x.y.z or latest", o.KafkaVersion)
		}
		if !chart.Window.Supports(v) {
			return nil, unsupportedKafkaError(v, chart, vp.Scope, o)
		}
		if floor != (kafkaversion.Version{}) && v.Less(floor) {
			return nil, fmt.Errorf("Kafka %s is below this platform's floor %s (%s) — the primary's configuration needs share groups; run it as an additional cluster instead: kates clusters add --version %s", v, floor, kafkaFloorKey, v)
		}
		vp.kafka = v
	}
	vp.KafkaVersion = vp.kafka.String()
	vp.Metadata = kafkaversion.MetadataFor(vp.kafka, chart.Window)

	// 6. Installed operators.
	vp.OperatorAction = "install"
	if r != nil {
		if err := vp.compareInstalled(ctx, r, o); err != nil {
			return nil, err
		}
	}

	// 7. Derived settings: the chart directory helm installs from.
	if vp.Pinned {
		vp.ChartDir = filepath.Join(o.RepoRoot, operatorChartDir)
	} else {
		dir, err := strimzi.WrapperFor(o.RepoRoot, cacheDir, chart.Version, vp.ChartPath)
		if err != nil {
			return nil, fmt.Errorf("preparing the operator chart for %s: %w", chart.Version, err)
		}
		vp.ChartDir = dir
	}
	return vp, nil
}

// resolveOperatorChart finds the tarball for the operator version being
// deployed: the flag's own file, the vendored pin, or a version pulled from
// the catalogue (cache first). It returns the path, where it came from, and
// whether it is the pin — resolveVersionPlan needs all three, and needs them
// as a value it can wrap an error around, which is why this is not inline.
func resolveOperatorChart(ctx context.Context, r strimzi.Runner, o versionOptions, requested, pin, cacheDir string) (path, source string, pinned bool, err error) {
	switch {
	case o.StrimziChart != "":
		if strings.Contains(o.StrimziChart, "://") {
			return "", "", false, fmt.Errorf("--strimzi-chart takes a local tarball in this version; pull the OCI reference with `helm pull` first")
		}
		return o.StrimziChart, "chart flag", false, nil

	case requested == "" || requested == pin:
		p, err := pinnedOperatorChart(ctx, o.RepoRoot, o.Offline)
		if err != nil {
			return "", "", false, err
		}
		return p, "pinned", true, nil

	case requested == "latest":
		if o.Offline || r == nil {
			return "", "", false, errors.New("--strimzi-version latest needs the catalogue, which needs the network")
		}
		cat, err := strimzi.FetchCatalogue(ctx, r, cacheDir, false)
		if err != nil {
			return "", "", false, fmt.Errorf("reading the Strimzi catalogue: %w", err)
		}
		newest, ok := cat.Newest()
		if !ok {
			return "", "", false, errors.New("the Strimzi catalogue is empty")
		}
		return resolveOperatorChart(ctx, r, o, newest.Version, pin, cacheDir)

	default:
		if _, err := kafkaversion.Parse(requested); err != nil {
			return "", "", false, fmt.Errorf("--strimzi-version %q: expected x.y.z or latest", requested)
		}
		cached := filepath.Join(cacheDir, strimzi.ChartFileName(requested))
		if _, err := os.Stat(cached); err == nil {
			return cached, "cache", false, nil
		}
		if o.Offline || r == nil {
			return "", "", false, fmt.Errorf("Strimzi %s is not in the cache (%s) and the network is off", requested, cacheDir)
		}
		p, err := strimzi.PullChart(ctx, r, cacheDir, requested)
		if err != nil {
			return "", "", false, fmt.Errorf("pulling Strimzi %s: %w", requested, err)
		}
		return p, "pulled", false, nil
	}
}

// unsupportedKafkaError is the §3.1/§3.3 refusal: the window, and every way out.
func unsupportedKafkaError(v kafkaversion.Version, chart *strimzi.Chart, scope string, o versionOptions) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Kafka %s cannot run under Strimzi %s (supported: %s).", v, chart.Version, strings.Join(chart.Window.Strings(), " "))
	if scope == scopeCluster {
		b.WriteString("\n  A cluster-wide operator watches every namespace, so no second Strimzi can be installed beside it.")
	}
	b.WriteString("\n  Ways forward:")
	fmt.Fprintf(&b, "\n    - a supported version for the primary:            kates deploy --kafka-version %s", chart.Window.Strings()[len(chart.Window)-1])
	b.WriteString("\n    - a different operator whose window has it:        kates versions strimzi")
	if v.AtLeast(kafkaversion.MinMirrorSource) {
		fmt.Fprintf(&b, "\n    - run it as an additional cluster, not the primary: kates migrate up --from %s", v)
	}
	return errors.New(b.String())
}

// readInstalled discovers the operators on the cluster. It runs before the
// operator version is chosen, because what is installed is an input to that
// choice (step 2b) as well as to the rails that judge it (step 6) — one
// reading, used twice, so the two can never disagree.
func (vp *versionPlan) readInstalled(ctx context.Context, r strimzi.Runner, o versionOptions) error {
	ops, err := strimzi.InstalledOperators(ctx, r)
	if err != nil {
		// No cluster access is not a resolution failure: deploy's own
		// preflight already decided the cluster is reachable, so this is
		// "no operator yet".
		vp.Notes = append(vp.Notes, "could not list installed operators: "+firstLine(err.Error()))
		return nil
	}
	vp.operators = ops
	if len(ops) == 0 {
		return nil
	}
	if strimzi.Shape(ops) == strimzi.ShapeMixed {
		return errors.New("this cluster has both a cluster-wide and namespace-scoped Strimzi operator; kates doctor lists them — remove one before deploying")
	}
	primary := primaryOperator(ops, o.KafkaNS)
	if primary == nil {
		vp.Notes = append(vp.Notes, fmt.Sprintf("%d operator(s) installed, none watches %s; a new one will be installed", len(ops), o.KafkaNS))
		return nil
	}
	vp.primaryOp = primary
	vp.Installed = primary.Version
	return nil
}

// compareInstalled judges the chosen version against the operator already
// running: install / same / upgrade, refusing downgrades, generation
// crossings and out-of-window running clusters (§3.5). It reads what
// readInstalled found rather than asking the cluster again.
func (vp *versionPlan) compareInstalled(ctx context.Context, r strimzi.Runner, o versionOptions) error {
	primary := vp.primaryOp
	if primary == nil {
		return nil
	}

	// Scope changes.
	installedScope := scopeCluster
	if primary.Scope == strimzi.ScopeNamespaces {
		installedScope = scopeNamespace
	}
	if installedScope != vp.Scope {
		if vp.Scope == scopeCluster {
			for _, op := range vp.operators {
				if op.Namespace != primary.Namespace {
					return fmt.Errorf("cannot switch to a cluster-wide operator while an additional operator runs in %s (Strimzi %s); remove it first (kates migrate down / kates clusters remove)", op.Namespace, op.Version)
				}
			}
		}
		vp.ScopeChange = installedScope + " → " + vp.Scope
	}

	kind, err := strimzi.UpgradeKind(primary.Version, vp.StrimziVersion)
	if err != nil {
		return err
	}
	switch kind {
	case strimzi.UpgradeSame:
		vp.OperatorAction = "same"
		return nil
	case strimzi.UpgradeDowngrade:
		// Reaching this means the older version was asked for by name:
		// resolution keeps a newer installed operator when no version was
		// requested (step 2b), so a default can no longer refuse itself.
		return fmt.Errorf("Strimzi %s is installed and %s was requested: Strimzi does not support downgrading the operator.\n  Ways forward:\n    - keep the installed operator: kates deploy (with no --strimzi-version)\n    - go back to %s:               kates clean, then deploy again",
			primary.Version, vp.StrimziVersion, vp.StrimziVersion)
	}
	vp.OperatorAction = "upgrade"

	// The 1.0 boundary.
	if below, _ := compareVersions(primary.Version, strimziV1); below < 0 {
		if at, _ := compareVersions(vp.StrimziVersion, strimziV1); at >= 0 {
			stored, err := strimzi.StoredVersions(ctx, r)
			if err != nil {
				return fmt.Errorf("checking the stored CRD versions before crossing Strimzi 1.0: %w", err)
			}
			for crd, versions := range stored {
				if len(versions) != 1 || versions[0] != "v1" {
					return fmt.Errorf("%s still stores %s: Strimzi %s removes v1beta2, so every resource must be converted first (Strimzi's API conversion procedure), then deploy again", crd, strings.Join(versions, ","), vp.StrimziVersion)
				}
			}
		}
	}

	// Every Kafka the upgraded operator will find must be in its window.
	running, err := runningKafkaVersions(ctx, r, primary)
	if err != nil {
		vp.Notes = append(vp.Notes, "could not list running Kafka clusters: "+firstLine(err.Error()))
	}
	for _, k := range running {
		v, perr := kafkaversion.Parse(k.version)
		if perr != nil {
			continue
		}
		vp.RollingCRs = append(vp.RollingCRs, fmt.Sprintf("%s/%s (%s)", k.namespace, k.name, k.version))
		if !vp.chart.Window.Supports(v) {
			return fmt.Errorf("%s/%s runs Kafka %s; Strimzi %s supports %s — upgrade Kafka first (kates deploy --kafka-version …), or move through an operator whose window has both (kates versions strimzi)",
				k.namespace, k.name, k.version, vp.StrimziVersion, strings.Join(vp.chart.Window.Strings(), " "))
		}
	}
	return nil
}

type runningKafka struct{ namespace, name, version string }

// runningKafkaVersions lists the Kafka CRs the operator watches, with their versions.
func runningKafkaVersions(ctx context.Context, r strimzi.Runner, op *strimzi.Operator) ([]runningKafka, error) {
	out, err := r.Run(ctx, "kubectl", "get", "kafka", "-A", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Kafka struct {
					Version string `json:"version"`
				} `json:"kafka"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parsing kafka list: %w", err)
	}
	var res []runningKafka
	for _, it := range list.Items {
		if !operatorWatches(op, it.Metadata.Namespace) {
			continue
		}
		res = append(res, runningKafka{it.Metadata.Namespace, it.Metadata.Name, it.Spec.Kafka.Version})
	}
	return res, nil
}

func operatorWatches(op *strimzi.Operator, ns string) bool {
	if op == nil {
		return false
	}
	for _, w := range op.Watches {
		if w == strimzi.WatchAll || w == ns {
			return true
		}
	}
	return false
}

// primaryOperator is the operator that watches the primary's namespace: the
// cluster-wide one, or the namespaced one whose list contains it.
func primaryOperator(ops []strimzi.Operator, kafkaNS string) *strimzi.Operator {
	for i := range ops {
		if ops[i].Scope == strimzi.ScopeCluster {
			return &ops[i]
		}
	}
	for i := range ops {
		if operatorWatches(&ops[i], kafkaNS) {
			return &ops[i]
		}
	}
	return nil
}

// compareVersions orders two x.y.z strings.
func compareVersions(a, b string) (int, error) {
	va, err := kafkaversion.Parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := kafkaversion.Parse(b)
	if err != nil {
		return 0, err
	}
	return kafkaversion.Compare(va, vb), nil
}

// operatorHelmArgs returns the --set arguments the operator install needs for
// this plan: the version (for the CRD hook's bundle URL) when not the pin,
// and the scope.
func (vp *versionPlan) operatorHelmArgs() []string {
	var args []string
	if !vp.Pinned {
		args = append(args, "--set", "strimziVersion="+vp.StrimziVersion)
	}
	if vp.Scope == scopeNamespace {
		args = append(args,
			"--set", "strimzi-kafka-operator.watchAnyNamespace=false",
			"--set", "strimzi-kafka-operator.watchNamespaces={"+strings.Join(vp.Watch, ",")+"}",
		)
	}
	return args
}

// kafkaHelmArgs returns the --set arguments the primary's install needs.
func (vp *versionPlan) kafkaHelmArgs(p primaryCluster) []string {
	return []string{
		"--set", "clusterName=" + p.Name,
		"--set-string", "kafkaVersion=" + vp.KafkaVersion,
		"--set-string", "kafka.metadataVersion=" + vp.Metadata,
		"--set-string", "strimziVersion=" + vp.StrimziVersion,
	}
}

// connectHelmArgs pins Connect's Kafka version to the primary's (§3.3).
func (vp *versionPlan) connectHelmArgs() []string {
	return []string{"--set-string", "version=" + vp.KafkaVersion}
}

// describe prints the plan as a deploy phase.
func (vp *versionPlan) describe(println func(string)) {
	src := vp.StrimziSource
	if vp.Pinned {
		src = "pinned"
	}
	scope := "cluster-wide"
	if vp.Scope == scopeNamespace {
		scope = "namespace-scoped, watching " + strings.Join(vp.Watch, ", ")
	}
	action := vp.OperatorAction
	if vp.Installed != "" && action != "same" {
		action = fmt.Sprintf("%s from %s", action, vp.Installed)
	}
	println(fmt.Sprintf("%-14s → %s (%s; %s)", "Strimzi", vp.StrimziVersion, src, scope))
	println(fmt.Sprintf("%-14s → %s", "Operator", action))
	kdesc := vp.KafkaVersion
	if vp.KafkaDefaulted {
		kdesc += fmt.Sprintf(" (newest supported by Strimzi %s; --kafka-version to choose another)", vp.StrimziVersion)
	}
	println(fmt.Sprintf("%-14s → %s", "Kafka", kdesc))
	println(fmt.Sprintf("%-14s → %s (window: %s)", "Metadata", vp.Metadata, strings.Join(vp.Window, " ")))
	if vp.ScopeChange != "" {
		println(fmt.Sprintf("%-14s → %s", "Scope change", vp.ScopeChange))
	}
	for _, n := range vp.Notes {
		println(output.WarningStyle.Render("⚠ ") + n)
	}
}

// confirmOperatorChange asks before an operator upgrade or a scope change,
// listing what will roll. --yes answers; no terminal fails and says so.
func (vp *versionPlan) confirmOperatorChange() error {
	if vp.OperatorAction != "upgrade" && vp.ScopeChange == "" {
		return nil
	}
	what := fmt.Sprintf("upgrade the Strimzi operator %s → %s", vp.Installed, vp.StrimziVersion)
	if vp.OperatorAction != "upgrade" {
		what = "change the operator scope " + vp.ScopeChange
	} else if vp.ScopeChange != "" {
		what += " and change its scope " + vp.ScopeChange
	}
	if len(vp.RollingCRs) > 0 {
		sort.Strings(vp.RollingCRs)
		output.Warn(fmt.Sprintf("This will %s. Kafka clusters that will roll: %s", what, strings.Join(vp.RollingCRs, ", ")))
	} else {
		output.Warn("This will " + what + ".")
	}
	if deployYes {
		return nil
	}
	if !IsInteractive() {
		return fmt.Errorf("refusing to %s without confirmation; pass --yes", what)
	}
	ok, err := confirmFn("Continue?")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("aborted")
	}
	return nil
}
