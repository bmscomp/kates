package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"github.com/charmbracelet/huh"
)

// runVersionPickerForm is the interactive "Versions" group of kates deploy -i
// (multi-version plan §3.4): scope, operator version, then a Kafka version
// whose options are exactly the chosen operator's window.
func runVersionPickerForm() error {
	pin, err := pinnedStrimziVersion(".")
	if err != nil {
		return err
	}
	if deployStrimziVersion == "" {
		deployStrimziVersion = pin
	}
	if deployKafkaVersion == "" {
		deployKafkaVersion = "latest"
	}

	// Operator options: the pin first, then whatever the catalogue lists
	// (with a short deadline — a picker must not hang on the network).
	opOptions := []huh.Option[string]{huh.NewOption(pin+"  (pinned — tested with this release)", pin)}
	seen := map[string]bool{pin: true}
	if cacheDir, cerr := strimzi.DefaultCacheDir(); cerr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if cat, ferr := strimzi.FetchCatalogue(ctx, defaultRunner, cacheDir, false); ferr == nil {
			for _, e := range cat.Entries {
				if seen[e.Version] {
					continue
				}
				if _, perr := kafkaversion.Parse(e.Version); perr != nil {
					continue
				}
				label := e.Version
				if cmp, _ := compareVersions(e.Version, pin); cmp > 0 {
					label += "  (newer than pinned — untested)"
				} else if old, _ := compareVersions(e.Version, strimziV1); old < 0 {
					label += "  (stores v1beta2)"
				}
				seen[e.Version] = true
				opOptions = append(opOptions, huh.NewOption(label, e.Version))
			}
		}
	}

	// Kafka options follow the operator: read (pull if needed) the chart of
	// the selected version and list its window, newest first.
	kafkaOptions := func() []huh.Option[string] {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		vp, err := resolveVersionPlanChartOnly(ctx, defaultRunner, versionOptions{
			RepoRoot: ".", StrimziVersion: deployStrimziVersion, Scope: deployOperatorScope,
		})
		if err != nil {
			return []huh.Option[string]{huh.NewOption("latest  (could not read the window: "+firstLine(err.Error())+")", "latest")}
		}
		newest, _ := vp.chart.Window.Newest()
		opts := []huh.Option[string]{huh.NewOption(fmt.Sprintf("%s  (newest supported by Strimzi %s)", newest, vp.StrimziVersion), "latest")}
		for i := len(vp.chart.Window) - 2; i >= 0; i-- {
			v := vp.chart.Window[i]
			label := v.String()
			if fl, ferr := kafkaFloor("."); ferr == nil && fl != (kafkaversion.Version{}) && v.Less(fl) {
				label += "  (below this platform's floor — additional clusters only)"
			}
			opts = append(opts, huh.NewOption(label, v.String()))
		}
		return opts
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Strimzi operator scope").
				Description("Cluster: one operator watching every namespace; only its Kafka versions can run under Strimzi here.\nNamespace: one operator per Kafka namespace; older lines can run beside the primary under their own operator.").
				Options(
					huh.NewOption("Mono cluster — one Strimzi, one Kafka version, the operator watching every namespace", scopeCluster),
					huh.NewOption("Namespace-scoped — one operator per Kafka namespace, several Kafka lines side by side", scopeNamespace),
				).
				Value(&deployOperatorScope),

			huh.NewSelect[string]().
				Title("Strimzi operator version").
				Description("The window of Kafka versions follows from this choice.").
				Options(opOptions...).
				Value(&deployStrimziVersion),

			huh.NewSelect[string]().
				Title("Kafka version for the primary cluster").
				DescriptionFunc(func() string {
					return "Versions Strimzi " + deployStrimziVersion + " can run. Others are additional clusters (kates migrate up --from …)."
				}, &deployStrimziVersion).
				OptionsFunc(kafkaOptions, &deployStrimziVersion).
				Value(&deployKafkaVersion),
		),
	).WithTheme(ThemeKates())

	if err := form.Run(); err != nil {
		return err
	}
	deployStrimziVersion = strings.TrimSpace(deployStrimziVersion)
	if deployStrimziVersion == pin {
		deployStrimziVersion = "" // the pin is the default; keep the flag empty so "pinned" is reported
	}
	return nil
}

// deployReviewSummary renders the wizard's choices as they will be applied:
// the versions as they resolve from the charts (best effort — the pin
// needs no network), the components, the namespaces. It is the last screen
// before Helm runs, and the same text is what --dry-run would print.
func deployReviewSummary() string {
	var b strings.Builder
	ns := resolveNamespaces()

	// Versions.
	scope := "mono cluster (one operator, cluster-wide)"
	if deployOperatorScope == scopeNamespace {
		scope = "namespace-scoped (one operator per Kafka namespace)"
	}
	fmt.Fprintf(&b, "Operator scope   %s\n", scope)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vp, err := resolveVersionPlanChartOnly(ctx, defaultRunner, versionOptions{
		RepoRoot: ".", StrimziVersion: deployStrimziVersion, KafkaVersion: deployKafkaVersion,
		Scope: deployOperatorScope, KafkaNS: ns.kafka, ConnectNS: ns.connect, WithConnect: deployWithKafkaConnect,
		Topology: deployTopology, SingleNS: deployNamespace,
	})
	if err != nil {
		sv := deployStrimziVersion
		if sv == "" {
			sv = "pinned"
		}
		kv := deployKafkaVersion
		if kv == "" || kv == "latest" {
			kv = "newest supported"
		}
		fmt.Fprintf(&b, "Strimzi          %s\nKafka            %s  (could not read the chart yet: %s)\n", sv, kv, firstLine(err.Error()))
	} else {
		src := vp.StrimziSource
		fmt.Fprintf(&b, "Strimzi          %s (%s)\n", vp.StrimziVersion, src)
		kdesc := vp.KafkaVersion
		if vp.KafkaDefaulted {
			kdesc += " (newest supported)"
		}
		fmt.Fprintf(&b, "Kafka            %s — cluster %q, metadata %s, window %s\n", kdesc, deployKafkaName, vp.Metadata, strings.Join(vp.Window, " "))
		if len(vp.Watch) > 0 {
			fmt.Fprintf(&b, "Watches          %s\n", strings.Join(vp.Watch, ", "))
		}
	}

	ha := "single-node"
	if deployHA {
		ha = "HA (multi-AZ)"
	}
	fmt.Fprintf(&b, "Sizing           %s\n", ha)
	if deployTopology == "single" {
		fmt.Fprintf(&b, "Topology         single namespace — everything in %s\n", deployNamespace)
	} else {
		fmt.Fprintf(&b, "Topology         isolated namespaces\n")
	}

	// Components, as a grid rather than a sentence.
	//
	// This used to be one comma-joined line. At eleven components it wrapped
	// into a run-on that told you nothing you could act on — you cannot scan a
	// paragraph for "is Kyverno in there, and where does MirrorMaker land".
	// The grid answers what, in what order, and into which namespace, by
	// position and colour.
	entries := buildSharedEntries(ns)
	fmt.Fprintf(&b, "\n%s across %d waves, into %s\n\n",
		plural(len(entries), "component", "components"),
		waveCount(entries),
		plural(len(namespaceList(entries)), "namespace", "namespaces"))
	b.WriteString(componentGrid(entries, gridOptions{Width: uiWidth() - 4}))
	return b.String()
}

// waveCount is how many of the three waves this deployment actually uses.
func waveCount(entries []DeploySummaryEntry) int {
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Group] = true
	}
	return len(seen)
}

// runDeployReviewForm shows the summary and asks once. Declining ends the
// command without touching the cluster.
func runDeployReviewForm() error {
	confirm := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Review").
				Description(deployReviewSummary()),
			huh.NewConfirm().
				Title("Deploy with these settings?").
				Affirmative("Deploy").
				Negative("Cancel").
				Value(&confirm),
		),
	).WithTheme(ThemeKates())
	if err := form.Run(); err != nil {
		return err
	}
	if !confirm {
		return fmt.Errorf("deployment cancelled at the review step; nothing was installed")
	}
	return nil
}
