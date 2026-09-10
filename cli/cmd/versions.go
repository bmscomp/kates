package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"github.com/spf13/cobra"
)

// kates versions — what can run here, read from the charts and the cluster,
// never from a table in Go (multi-version plan §3.4, §4).

var (
	versionsAll     bool
	versionsRefresh bool
	versionsResolve bool
	versionsStrimzi string
	versionsOffline bool
)

var versionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Strimzi and Kafka versions: the operators on this cluster, their windows, the legacy range",
	Long: `Shows which Kafka versions can run here and how.

With no subcommand: every Strimzi operator installed on the current cluster
(or, without a cluster, the repository's pinned operator), the Kafka versions
each one supports, and the versions the legacy-kafka chart runs without an
operator.

  kates versions strimzi   the catalogue of published operator versions
  kates versions kafka     the Kafka window of one operator version`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVersions(cmd.Context())
	},
}

var versionsStrimziCmd = &cobra.Command{
	Use:   "strimzi",
	Short: "Published Strimzi operator versions and their status relative to the pin",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVersionsStrimzi(cmd.Context())
	},
}

var versionsKafkaCmd = &cobra.Command{
	Use:   "kafka",
	Short: "The Kafka versions one Strimzi operator version supports",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVersionsKafka(cmd.Context())
	},
}

func init() {
	versionsCmd.PersistentFlags().BoolVar(&versionsOffline, "offline", false, "Never touch the network (read only what is cached or pinned)")
	versionsStrimziCmd.Flags().BoolVar(&versionsAll, "all", false, "Include versions below this platform's floor")
	versionsStrimziCmd.Flags().BoolVar(&versionsRefresh, "refresh", false, "Refresh the catalogue cache")
	versionsStrimziCmd.Flags().BoolVar(&versionsResolve, "resolve", false, "Pull every listed chart to show its Kafka window")
	versionsKafkaCmd.Flags().StringVar(&versionsStrimzi, "strimzi-version", "", "Operator version to inspect (default: the pin)")
	versionsCmd.AddCommand(versionsStrimziCmd, versionsKafkaCmd)
	rootCmd.AddCommand(versionsCmd)
}

// versionsRow is one line of `kates versions`.
type versionsRow struct {
	Source   string   `json:"source"` // operator <ns> | pin | legacy
	Version  string   `json:"version"`
	Scope    string   `json:"scope,omitempty"`
	Watches  []string `json:"watches,omitempty"`
	Provider string   `json:"provider"`
	Window   []string `json:"window,omitempty"`
	Range    string   `json:"range,omitempty"`
	Note     string   `json:"note,omitempty"`
}

func runVersions(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pin, err := pinnedStrimziVersion(".")
	if err != nil {
		return err
	}
	var rows []versionsRow
	ops, opErr := strimzi.InstalledOperators(ctx, defaultRunner)
	if opErr == nil && len(ops) > 0 {
		for _, op := range ops {
			note := ""
			if op.Version != pin {
				note = "pin: " + pin
			}
			if op.IsForeign() {
				note = strings.TrimSpace(note + " (not installed by kates)")
			}
			rows = append(rows, versionsRow{
				Source: "operator " + op.Namespace, Version: op.Version, Scope: op.Scope,
				Watches: op.Watches, Provider: string(kafkaversion.ProviderStrimzi),
				Window: op.Window.Strings(), Note: note,
			})
		}
	} else {
		vp, err := resolveVersionPlan(ctx, nil, versionOptions{RepoRoot: ".", Offline: versionsOffline})
		if err != nil {
			return err
		}
		note := "pinned; no operator installed"
		if opErr != nil {
			note = "pinned; cluster not reachable"
		}
		rows = append(rows, versionsRow{
			Source: "pin", Version: vp.StrimziVersion, Provider: string(kafkaversion.ProviderStrimzi),
			Window: vp.Window, Note: note,
		})
	}
	rows = append(rows, legacyRows()...)

	if outputMode == "json" {
		output.JSON(rows)
		return nil
	}
	output.Header("Kafka versions this cluster can run")
	var table [][]string
	for _, r := range rows {
		what := r.Range
		if len(r.Window) > 0 {
			what = strings.Join(r.Window, " ")
		}
		scope := r.Scope
		if len(r.Watches) > 0 && r.Scope == strimzi.ScopeNamespaces {
			scope += " (" + strings.Join(r.Watches, ",") + ")"
		}
		table = append(table, []string{r.Source, r.Version, scope, r.Provider, what, r.Note})
	}
	output.Table([]string{"SOURCE", "STRIMZI", "SCOPE", "PROVIDER", "KAFKA", "NOTE"}, table)
	output.Hint("kates versions strimzi lists other operator versions; kates migrate up --from <old> stands up a source at any of these.")
	return nil
}

// legacyRows are the shapes the legacy-kafka chart runs without an operator.
func legacyRows() []versionsRow {
	return []versionsRow{
		{Source: "legacy", Version: "-", Provider: string(kafkaversion.ProviderLegacy), Range: "2.1.0 – 3.2.x", Note: "ZooKeeper, built image (kates migrate image build)"},
		{Source: "legacy", Version: "-", Provider: string(kafkaversion.ProviderLegacy), Range: "3.3.0 – 3.6.x", Note: "KRaft, built image"},
		{Source: "legacy", Version: "-", Provider: string(kafkaversion.ProviderLegacy), Range: "3.7.0 and newer", Note: "KRaft, official apache/kafka image"},
	}
}

// catalogueRow is one line of `kates versions strimzi`.
type catalogueRow struct {
	Version string   `json:"version"`
	Window  []string `json:"window,omitempty"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason,omitempty"`
}

func runVersionsStrimzi(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pin, err := pinnedStrimziVersion(".")
	if err != nil {
		return err
	}
	floor, err := kafkaFloor(".")
	if err != nil {
		return err
	}
	cacheDir, err := strimzi.DefaultCacheDir()
	if err != nil {
		return err
	}
	var cat *strimzi.Catalogue
	if versionsOffline {
		cat = cachedCatalogue(cacheDir)
		if cat == nil {
			return fmt.Errorf("no cached catalogue in %s; drop --offline to fetch it", cacheDir)
		}
	} else {
		cat, err = strimzi.FetchCatalogue(ctx, defaultRunner, cacheDir, versionsRefresh)
		if err != nil {
			return fmt.Errorf("reading the Strimzi catalogue: %w", err)
		}
	}

	var rows []catalogueRow
	for _, e := range cat.Entries {
		row := catalogueRow{Version: e.Version}
		if _, perr := kafkaversion.Parse(e.Version); perr != nil {
			continue // pre-releases and odd tags
		}
		cmp, _ := compareVersions(e.Version, pin)
		switch {
		case cmp == 0:
			row.Status = "pinned — tested with this release"
		case cmp > 0:
			row.Status = "newer than pinned"
		default:
			row.Status = "supported"
		}
		// The window is a fact of the chart; show it when the chart is
		// here, pull it when asked.
		path := filepath.Join(cacheDir, strimzi.ChartFileName(e.Version))
		if e.Version == pin {
			if p, perr := strimzi.VendoredChartPath("."); perr == nil {
				path = p
			}
		}
		if _, serr := os.Stat(path); serr != nil && versionsResolve && !versionsOffline {
			if p, perr := strimzi.PullChart(ctx, defaultRunner, cacheDir, e.Version); perr == nil {
				path = p
			}
		}
		if chart, cerr := strimzi.ReadChart(path); cerr == nil {
			row.Window = chart.Window.Strings()
			if fc := strimzi.CheckPrimaryFloor(chart, floor); !fc.OK {
				row.Status = "below floor"
				row.Reason = fc.Reason
			} else if strimzi.Generation(chart) != "v1" {
				row.Reason = "stores v1beta2; cannot sit beside a 1.x operator"
			}
		} else if below, _ := compareVersions(e.Version, strimziV1); below < 0 {
			// Not pulled: the API generation is known from the release line.
			if old, _ := compareVersions(e.Version, "0.49.0"); old < 0 {
				row.Status = "below floor"
				row.Reason = "serves no v1 API (Strimzi < 0.49)"
			} else {
				row.Reason = "stores v1beta2; cannot sit beside a 1.x operator"
			}
		}
		if row.Status == "below floor" && !versionsAll {
			continue
		}
		rows = append(rows, row)
	}

	if outputMode == "json" {
		output.JSON(rows)
		return nil
	}
	output.Header(fmt.Sprintf("Strimzi operator versions (catalogue fetched %s)", cat.FetchedAt.Local().Format(time.RFC822)))
	var table [][]string
	for _, r := range rows {
		w := strings.Join(r.Window, " ")
		if w == "" {
			w = "(--resolve to pull)"
		}
		table = append(table, []string{r.Version, w, r.Status, r.Reason})
	}
	output.Table([]string{"STRIMZI", "KAFKA WINDOW", "STATUS", "NOTE"}, table)
	if !versionsAll {
		output.Hint("Versions below this platform's floor are hidden; --all shows them.")
	}
	return nil
}

// cachedCatalogue returns the cache file's catalogue regardless of age, or nil.
func cachedCatalogue(cacheDir string) *strimzi.Catalogue {
	// FetchCatalogue with a far-future TTL is not exposed; read via a fake
	// runner that refuses the network so only the cache can answer.
	cat, err := strimzi.FetchCatalogue(context.Background(), strimzi.RunnerFunc(func(context.Context, string, ...string) (string, error) {
		return "", fmt.Errorf("offline")
	}), cacheDir, false)
	if err != nil {
		return nil
	}
	return cat
}

func runVersionsKafka(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	opts := versionOptions{RepoRoot: ".", StrimziVersion: versionsStrimzi, Offline: versionsOffline}
	var r strimzi.Runner
	if !versionsOffline {
		r = defaultRunner
	}
	// Only the chart steps are wanted here: read the chart, print its window.
	vp, err := resolveVersionPlanChartOnly(ctx, r, opts)
	if err != nil {
		return err
	}
	if outputMode == "json" {
		output.JSON(map[string]any{
			"strimziVersion": vp.StrimziVersion,
			"source":         vp.StrimziSource,
			"window":         vp.Window,
			"newest":         vp.KafkaVersion,
			"served":         vp.chart.Served,
			"stored":         vp.chart.Stored,
		})
		return nil
	}
	output.Header(fmt.Sprintf("Strimzi %s (%s)", vp.StrimziVersion, vp.StrimziSource))
	output.KeyValue("Kafka window", strings.Join(vp.Window, " "))
	output.KeyValue("Newest", vp.KafkaVersion)
	output.KeyValue("CRD API", fmt.Sprintf("served %s, stored %s", strings.Join(vp.chart.Served, ","), vp.chart.Stored))
	metas := make([]string, 0, len(vp.chart.Window))
	for _, v := range vp.chart.Window {
		metas = append(metas, fmt.Sprintf("%s→%s", v, kafkaversion.MetadataFor(v, vp.chart.Window)))
	}
	sort.Strings(metas)
	output.KeyValue("Metadata rule", strings.Join(metas, "  "))
	return nil
}

// resolveVersionPlanChartOnly resolves the operator chart and its window
// without the cluster comparison — what `kates versions kafka` needs.
func resolveVersionPlanChartOnly(ctx context.Context, r strimzi.Runner, o versionOptions) (*versionPlan, error) {
	return resolveVersionPlan(ctx, chartOnlyRunner{r}, o)
}

// chartOnlyRunner lets helm through (catalogue, pull) and makes every kubectl
// call fail, so the installed-operator step records a note instead of a
// verdict. A nil inner runner refuses everything (offline).
type chartOnlyRunner struct{ inner strimzi.Runner }

func (c chartOnlyRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "helm" || c.inner == nil {
		return "", fmt.Errorf("%s: not consulted by this command", name)
	}
	return c.inner.Run(ctx, name, args...)
}
