package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/bmscomp/kates/cli/pkg/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var isTesting = false

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy the Kates stack (Kafka, Kates, Chaos, Schema Registry)",
	Long: `Deploys the entire Kates stack using the detected cluster configuration.
Supports deploying into a single namespace or isolated namespaces.

Examples:
  # Deploy everything into a single namespace (dev mode)
  kates deploy --topology single --namespace kates-stack

  # Deploy components into isolated namespaces (production mode)
  kates deploy --topology isolated --kafka-ns kafka-system --app-ns kates-app --chaos-ns litmus-system

  # Deploy with Apicurio Schema Registry
  kates deploy --with-schema-registry apicurio

  # Choose the operator and Kafka versions (validated before anything is installed)
  kates deploy --strimzi-version 1.0.1 --kafka-version 4.2.0

  # One operator per Kafka namespace, so older lines can run beside the primary
  kates deploy --operator-scope namespace

  # Without cluster admin: the Kafka stack and Kates in namespaces you have,
  # on a Strimzi operator a cluster admin installed
  kates deploy --adminless --kafka-ns team-kafka --connect-ns team-connect --db-ns team-db --app-ns team-kates
  kates deploy --adminless --topology single --namespace team

A cluster admin gets the full profile. For any other account, deploy says so
and offers the admin-less profile instead.

A release build installs the charts its release published to
oci://ghcr.io/bmscomp/charts, at the versions of its tag, pulled fresh into the
user cache on every run (the cached copy serves when the registry cannot be
reached), so no checkout of the repository is needed, and it says when a newer
kates release is out. Each chart's cosign signature is checked before it is
used; KATES_REQUIRE_SIGNED_CHARTS=1 refuses charts it could not check.
Without GHCR, KATES_CHART_REGISTRY names a mirror, and --chart-bundle reads a
bundle that kates charts pull --to wrote, with no network. KATES_IMAGE_REGISTRY
pulls every image from a registry that kates charts push filled. With no reachable cluster, and Docker and kind on the
machine, it offers to create the local 3-zone kind cluster; behind a proxy, its
nodes get HTTP_PROXY, HTTPS_PROXY, NO_PROXY and the CA in PROXY_CA_FILE. A dev build installs
the charts of the checkout it runs in. --checkout (or KATES_CHECKOUT) makes any
build install a checkout's charts:

  kates deploy --checkout ~/src/kates`,
	RunE: runDeploy,
}

var (
	deployTopology           string
	deployNamespace          string
	deployKafkaNS            string
	deployConnectNS          string
	deployDbNS               string
	deployAppNS              string
	deployChaosNS            string
	deployMonitoringNS       string
	deployKafkaUINS          string
	deployWithSchemaRegistry string
	deployHA                 bool
	deployWithChaos          bool
	deployWithMonitoring     bool
	deployWithCertManager    bool
	deployWithKyverno        bool
	deployWithSecretManager  bool
	deployWithStrimzi        bool
	deployWithKafkaConnect   bool
	deployInteractive        bool
	deployVerbose            bool
	deployPortForward        bool
	deployDryRun             bool
	deployWithKafkaUI        bool
	deployWithMirrorMaker2   bool
	deployMM2NS              string
	deployNamespaceLabels    []string
	deployNoWriteProbes      bool
	deployYes                bool
	deployUpgrade            []string

	// Versions and scope (multi-version plan §3.1–3.3).
	deployOperatorScope  string
	deployStrimziVersion string
	deployStrimziChart   string
	deployKafkaVersion   string
	deployKafkaName      string
)

func init() {
	// Topology, namespaces and --kafka-name: deploy_flags.go.
	addDeployScopeFlags(deployCmd)

	// Component flags
	deployCmd.Flags().StringVar(&deployWithSchemaRegistry, "with-schema-registry", "apicurio", "Schema Registry to deploy: 'none', 'apicurio', or 'confluent'")
	deployCmd.Flags().BoolVar(&deployHA, "ha", true, "Enable High Availability (Multi-AZ)")
	deployCmd.Flags().BoolVar(&deployWithChaos, "with-chaos", true, "Deploy LitmusChaos engine")
	deployCmd.Flags().BoolVar(&deployWithMonitoring, "with-monitoring", true, "Deploy monitoring components (Prometheus/Grafana/Jaeger)")
	deployCmd.Flags().BoolVar(&deployWithCertManager, "with-cert-manager", true, "Deploy Cert-Manager for TLS certificate management")
	deployCmd.Flags().BoolVar(&deployWithKyverno, "with-kyverno", false, "Deploy Kyverno for cluster policy enforcement")
	deployCmd.Flags().BoolVar(&deployWithSecretManager, "with-secret-manager", false, "Deploy Secret Manager (e.g., External Secrets Operator)")
	deployCmd.Flags().BoolVar(&deployWithStrimzi, "with-strimzi", true, "Deploy Strimzi Operator")
	deployCmd.Flags().BoolVar(&deployWithKafkaConnect, "with-kafka-connect", false, "Deploy Kafka Connect with PostgreSQL CDC (Debezium)")
	deployCmd.Flags().BoolVar(&deployWithKafkaUI, "with-kafka-ui", true, "Deploy Kafka UI for browser-based cluster monitoring")
	deployCmd.Flags().BoolVar(&deployWithMirrorMaker2, "with-mirror-maker2", false, "Deploy MirrorMaker 2 as a loopback mirror of the primary (replication lab; kates migrate for real sources)")
	deployCmd.Flags().StringArrayVar(&deployNamespaceLabels, "namespace-label", nil, "Label to apply to the namespaces deploy ensures (key=value, repeatable; overrides KATES_NAMESPACE_LABELS, which overrides the labels file)")
	addLabelsFileFlag(deployCmd)
	deployCmd.Flags().BoolVar(&deployNoWriteProbes, "no-write-probes", false, "Skip the introspection probes that create a namespace, a Secret or pods (as --dry-run does)")
	deployCmd.Flags().BoolVarP(&deployInteractive, "interactive", "i", false, "Use interactive UI to configure deployment")
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show every kubectl/helm command as it runs")
	deployCmd.Flags().BoolVarP(&deployPortForward, "port-forward", "P", false, "After deploy, run kates ports: forward every service in the background and make the ports context current")
	deployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Show the deployment plan without executing anything")
	deployCmd.Flags().BoolVarP(&deployYes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	deployCmd.Flags().StringSliceVar(&deployUpgrade, "upgrade", nil, "Installed components to upgrade to this deploy's charts: apicurio, connect-cluster, kafka-ui, mm2, monitoring, or all (default: installed components are kept; Kates is always upgraded)")
	deployCmd.Flags().StringVar(&deployOperatorScope, "operator-scope", "cluster", "Strimzi operator scope: 'cluster' (one operator watching every namespace) or 'namespace' (one per Kafka namespace)")
	deployCmd.Flags().StringVar(&deployStrimziVersion, "strimzi-version", "", "Strimzi operator version to install (default: this release's pin; 'latest' for the newest published)")
	deployCmd.Flags().StringVar(&deployStrimziChart, "strimzi-chart", "", "Local strimzi-kafka-operator chart tarball to install from (air-gapped mirrors)")
	deployCmd.Flags().StringVar(&deployKafkaVersion, "kafka-version", "", "Kafka version for the primary cluster (default: the newest the operator supports; 'latest' spells the default)")
	deployCmd.Flags().StringVar(&deployCheckout, "checkout", "", "Install the charts of the Kates checkout in this directory instead of the published ones (env KATES_CHECKOUT)")
	deployCmd.Flags().StringVar(&deployChartBundle, "chart-bundle", "", "Install from the bundle `kates charts pull --to` wrote in this directory, with no network (env KATES_CHART_BUNDLE)")
	deployCmd.Flags().BoolVar(&deployAdminless, "adminless", false, "Install without cluster admin: Kafka, Kafka Connect, MirrorMaker 2, Apicurio, Kafka UI and Kates into existing namespaces placed by --topology and the --*-ns flags (single: --namespace, default the kube context's), on a Strimzi operator already watching them")

	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	deployStartTime := time.Now()
	resetDeployPhases()

	dl = &DashboardController{}

	// A bad namespace label fails before anything asks the cluster.
	namespaceLabels, namespaceLabelSources, err := namespaceUserLabels()
	if err != nil {
		return err
	}
	target, err := prepareDeployTarget()
	if err != nil {
		return err
	}
	defer pinKubeContext(target)()
	PrintDeployBanner(target)

	// Guarded by isInteractive: the forms open /dev/tty directly, so without a
	// terminal they fail with "could not open a new TTY" rather than falling
	// back to the flag defaults. Piped or scripted runs should just use the
	// defaults.
	wizard := (deployInteractive || cmd.Flags().NFlag() == 0) && IsInteractive()

	if err := resolveDeployProfile(cmd, target, wizard); err != nil {
		return err
	}
	if err := configureDeploySelection(cmd, wizard); err != nil {
		return err
	}

	// Offline, the nodes have no registry to pull from: the bundle's images
	// go in before any pod, the pre-flight probes' included. A dry run loads
	// nothing.
	if !deployDryRun {
		if err := loadBundleImages(context.Background(), target, quickDetectKind()); err != nil {
			return err
		}
	}

	executor := defaultExecutor
	report, err := introspectDeployCluster(executor, target, namespaceLabels)
	if err != nil {
		return err
	}

	vp, primary, err := resolveDeployVersions()
	if err != nil {
		return err
	}

	valuesFile, errorLog, err := deployFiles()
	if err != nil {
		return err
	}
	if err := writeDeployValues(executor, valuesFile, report, primary.Name); err != nil {
		return err
	}

	// Resolve namespaces before building entries
	ns := resolveNamespaces()

	// ── Build shared component registry (single source of truth) ──
	sharedEntries := buildSharedEntries(ns)

	// ── Dry-run: show preview and exit ──
	if deployDryRun {
		return runDeployDryRun(sharedEntries, namespaceLabels, namespaceLabelSources)
	}

	// An operator upgrade or a scope change rolls things; say so and ask.
	if deployWithStrimzi {
		if err := vp.confirmOperatorChange(); err != nil {
			return err
		}
	}

	// Record each component's outcome BEFORE the pipeline runs, from the same
	// predicate the skip logic uses: a release that already exists gets skipped
	// ("already present"), one that does not gets deployed. Status was never
	// recorded at all before this — the summary's table read the empty string
	// as "Skipped" while its footer counted the same empty string as
	// "deployed", producing eight Skipped rows above "8 components deployed
	// successfully!".
	markEntryStatuses(sharedEntries, func(release, namespace string) bool {
		return releaseKept(context.Background(), release, namespace)
	})

	// 4. Execution Plan (Helm)
	PrintPhaseHeader(nextDeployPhase(), "Executing Deployment Pipeline")

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dc := newDeployContext(ctx, ns, valuesFile, report, vp, primary)
	res := runDeployPipeline(dc, sharedEntries, deployStartTime)
	if res.dashboardErr != nil {
		// The dashboard failed: the deploy's own error, when it has one, is
		// the more consequential.
		if res.err != nil {
			return res.err
		}
		return res.dashboardErr
	}

	if res.err != nil {
		saveDeployError(errorLog, res.err)
	}

	if res.err == nil && len(res.entries) > 0 {
		finishDeploy(ctx, target, ns, res.entries, res.elapsed)
	}

	return res.err
}

// prepareDeployTarget runs what comes before any question: the --upgrade
// flag, the release notice, the chart root and the target cluster, which
// becomes kubectl's current context. The caller pins that context.
func prepareDeployTarget() (string, error) {
	if err := applyUpgradeFlag(deployUpgrade); err != nil {
		return "", err
	}

	// Before the gate, which may build a kind cluster: a newer release's
	// charts need the newer kates.
	warnIfNewerRelease(context.Background())

	// Before the gate too, so a run with no charts to install stops before it
	// builds a cluster; and before the wizard, whose version screen reads the
	// operator chart.
	if err := resolveChartRoot(context.Background(), deployCheckout, false, deployChartDirs()...); err != nil {
		return "", err
	}

	// ── Target cluster ───────────────────────────────────────────────────
	// This runs FIRST, before the banner and the wizard. There is no point
	// asking eight questions about a deployment and only then discovering
	// there is nowhere to deploy it. When nothing is reachable this offers to
	// build the local 3-zone kind cluster; when several are reachable it asks
	// rather than guessing.
	target, err := resolveClusterFn()
	if err != nil {
		return "", err
	}
	// The chosen cluster becomes kubectl's current context, for the commands
	// the user runs next. The deploy's own kubectl and helm calls name it
	// instead of reading the current context, so a context switched elsewhere
	// mid-deploy cannot split the stack across two clusters
	// (deploy_kubecontext.go).
	if err := useTargetContext(target); err != nil {
		return "", err
	}
	return target, nil
}

// resolveDeployProfile settles the profile. Full for a cluster admin, who
// the wizard also lets pick admin-less. Anyone else is offered the
// admin-less stack (deploy_adminless.go) rather than walked into a refusal
// halfway through. The platform line comes first, before the first question.
func resolveDeployProfile(cmd *cobra.Command, target string, wizard bool) error {
	if deployAdminless {
		printPlatform(target, "admin-less profile (--adminless)")
	} else if err := resolveFullProfile(target, wizard); err != nil {
		return err
	}
	if deployAdminless {
		return applyAdminlessProfile(context.Background(), cmd)
	}
	return nil
}

// resolveFullProfile asks whether the account is a cluster admin. One that
// is not is offered the admin-less profile, and taking it sets
// deployAdminless.
func resolveFullProfile(target string, wizard bool) error {
	admin, ok := clusterAdminFn(context.Background())
	printPlatform(target, accountLabel(admin, ok))
	if ok && admin && wizard {
		if err := profileFormFn(); err != nil {
			return err
		}
	}
	if !ok || admin {
		return nil
	}
	suggestion := adminlessSuggestion(target)
	if deployYes || !IsInteractive() {
		return errors.New(suggestion)
	}
	output.Warn(suggestion)
	yes, err := confirmFn("Install the admin-less stack instead?")
	if err != nil {
		return err
	}
	if !yes {
		return errors.New("deploy cancelled: the full profile needs cluster admin")
	}
	deployAdminless = true
	return nil
}

// accountLabel names the account on the platform line from the cluster-admin
// check: admin is its answer, ok whether it got one.
func accountLabel(admin, ok bool) string {
	switch {
	case ok && admin:
		return "cluster admin"
	case ok:
		return "not cluster admin"
	default:
		return "cluster admin not confirmed"
	}
}

// configureDeploySelection runs the wizard when there is one, then checks
// what it chose: --upgrade against the components, the admin-less
// prerequisites and the chart bundle. It ends by printing the topology and
// the component selection.
func configureDeploySelection(cmd *cobra.Command, wizard bool) error {
	// ── Interactive Forms ────────────────────────────────────────────────
	// Admin-less, they offer only the namespaced components and run before
	// the prerequisites check so it checks the namespaces chosen here.
	if wizard {
		if err := runInteractiveForms(); err != nil {
			return err
		}
		if deployAdminless && deployTopology == "single" && !cmd.Flags().Changed("db-ns") {
			deployDbNS = deployNamespace
		}
	}
	if err := checkUpgradeInDeploy(deployUpgrade, buildSharedEntries(resolveNamespaces())); err != nil {
		return err
	}

	if deployAdminless {
		if err := checkAdminlessPrerequisites(context.Background(), resolveNamespaces()); err != nil {
			return err
		}
		adminlessConformityMissing = missingConformityGrants(context.Background(), resolveNamespaces().kafka)
		if len(adminlessConformityMissing) > 0 {
			output.Warn(conformityRoleHandoff(resolveNamespaces(), adminlessConformityMissing))
		}
	}

	// After the wizard, which can turn these on.
	if chartBundle() != "" {
		if outside := chartsOutsideBundle(); len(outside) > 0 {
			output.Warn("Not in the chart bundle, and pulled from their own repositories unless already installed: " + strings.Join(outside, ", ") + ". Without network, turn them off.")
		}
	}

	// Resolve Topology
	printTopologyResolution()

	// Component Selection
	printComponentSelection()
	return nil
}

// introspectDeployCluster runs the pre-flight and the introspection that
// sizes the values file. Admin-less it runs neither.
func introspectDeployCluster(executor detect.CommandExecutor, target string, namespaceLabels map[string]string) (*detect.DetectReport, error) {
	if deployAdminless {
		// Introspection reads nodes and kube-system, and its probes create
		// namespaces and pods: nothing a namespace account may do. The charts'
		// own defaults stand in for what it would have sized.
		// shortcut: assumes the cluster.local DNS domain; read it from a pod in the namespace if a cluster needs another.
		report := &detect.DetectReport{Context: target, Provider: "unknown"}
		report.Network.ClusterDomain = "cluster.local"
		return report, nil
	}
	PrintPhaseHeader(nextDeployPhase(), "Running Cluster Introspection (Pre-flight)")
	collector := detect.NewCollector(executor)
	// Introspection is not read-only on its own: it creates a namespace and a
	// Secret to audit Secret creation, prober pods to measure inter-AZ
	// latency, and a dns-detect pod when no running pod can be read. The
	// preview uses none of that, so a dry run collects without it.
	collector.ReadOnly = deployDryRun || deployNoWriteProbes
	collector.KubeContext = target
	collector.NamespaceLabels = namespaceLabels
	if err := collector.Preflight(); err != nil {
		output.Error(fmt.Sprintf("Preflight failed: %v", err))
		return nil, err
	}

	// ── Kind storage bootstrap ────────────────────────────────────────────────
	// Create zone-specific StorageClasses (local-storage-alpha, etc.) BEFORE
	// collector.Collect() runs. This ensures detect's matchStorageClass() finds
	// them and writes the correct storageClass names into values-detected.yaml,
	// so no hardcoded pool overrides are needed in values-kind.yaml.
	// A dry run must not write StorageClasses into the cluster, so it skips
	// this, as it skips the introspection probes that create things.
	if quickDetectKind() && !deployDryRun {
		PrintPhaseItem("Kind cluster detected — bootstrapping zone StorageClasses...")
		if err := setupKindStorageClasses(context.Background()); err != nil {
			output.Warn(fmt.Sprintf("StorageClass bootstrap warning: %v", err))
		}
	}

	report, err := collector.Collect(context.Background())
	if err != nil {
		output.Error(fmt.Sprintf("Introspection failed: %v", err))
		return nil, err
	}
	return report, nil
}

// resolveDeployVersions resolves versions and scope before any Helm call:
// the operator version, its chart, the Kafka window it supports, the Kafka
// version (asked for or the newest), the metadata version, and what the
// installed operator allows. A refusal here costs seconds; the same refusal
// from Strimzi would come after a ten-minute Helm timeout.
func resolveDeployVersions() (*versionPlan, primaryCluster, error) {
	PrintPhaseHeader(nextDeployPhase(), "Resolving Versions")
	PrintPhaseItem("Charts: " + chartSource())
	vp, err := resolveVersionPlanFn(context.Background(), defaultRunner, deployVersionOptions())
	if err != nil {
		return nil, primaryCluster{}, err
	}
	vp.describe(PrintPhaseItem)
	return vp, primaryCluster{Name: deployKafkaName, Namespace: resolveNamespaces().kafka, Version: vp.kafka}, nil
}

// writeDeployValues writes the values file the introspection sized.
// Admin-less, nothing was introspected, so the file stays empty.
func writeDeployValues(executor detect.CommandExecutor, valuesFile string, report *detect.DetectReport, kafkaName string) (err error) {
	if err := os.MkdirAll(filepath.Dir(valuesFile), 0o755); err != nil {
		return fmt.Errorf("failed to create values file: %v", err)
	}

	f, err := os.Create(valuesFile)
	if err != nil {
		return fmt.Errorf("failed to create values file: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to write values file: %v", cerr)
		}
	}()

	if deployAdminless {
		return nil
	}
	analyzer := detect.NewAnalyzer(executor)
	analyzer.Analyze(report, detect.ParsedReqs{})

	PrintPhaseItem("Generating " + valuesFile + "...")
	if err := detect.RenderValuesWithReserve(report, kafkaName, 0.30, f); err != nil {
		return fmt.Errorf("failed to write values file: %v", err)
	}
	return nil
}

// newDeployContext is what the pipeline's steps share. advanceStep is set
// by runDeployPipeline, which knows the step count.
func newDeployContext(ctx context.Context, ns resolvedNamespaces, valuesFile string, report *detect.DetectReport, vp *versionPlan, primary primaryCluster) *deployContext {
	isKind := isKindReport(report)
	return &deployContext{
		ctx:          ctx,
		ns:           ns,
		valuesFile:   valuesFile,
		isKind:       isKind,
		report:       report,
		chartOverlay: func(chartDir string) string { return chartOverlayFor(isKind, chartDir) },
		fileExists:   fileExistsOnDisk,
		versions:     vp,
		primary:      primary,
	}
}

// isKindReport detects the cluster type once: every Helm call uses it to
// pick the values overlay (values-kind.yaml or values-generic.yaml).
func isKindReport(report *detect.DetectReport) bool {
	return report.Network.CNI == "kindnet" || report.Provider == "kind"
}

// chartOverlayFor returns the path to the environment values file for a
// chart directory. Falls back to generic when the cluster is not kind.
func chartOverlayFor(isKind bool, chartDir string) string {
	if isKind {
		return chartDir + "/values-kind.yaml"
	}
	return chartDir + "/values-generic.yaml"
}

func fileExistsOnDisk(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// newDeployProgram builds a bubbletea program only where one can actually
// render. With nil the DashboardController falls back to plain fmt printing
// — that fallback existed all along; the wiring just never used it.
// Previously a piped or headless deploy launched an alt-screen program into
// the pipe, dumping escape sequences into logs.
func newDeployProgram(dashboard deployDashboardModel) *tea.Program {
	if isTesting {
		return tea.NewProgram(dashboard, tea.WithoutRenderer(), tea.WithInput(nil))
	}
	if IsInteractive() && !accessibleUI() {
		theme.FullScreen()
		return tea.NewProgram(dashboard, tea.WithAltScreen())
	}
	return nil
}

// deployPipelineResult is what runDeployPipeline hands back: the summary
// entries and elapsed time of a deploy that succeeded, the deploy's error,
// and the dashboard's own error when its program failed.
type deployPipelineResult struct {
	entries      []DeploySummaryEntry
	elapsed      time.Duration
	err          error
	dashboardErr error
}

// runDeployPipeline runs the namespaces and the components under the
// deployment dashboard (Bubble Tea). It sets dc.advanceStep.
func runDeployPipeline(dc *deployContext, sharedEntries []DeploySummaryEntry, start time.Time) deployPipelineResult {
	totalSteps := countDeploySteps()

	dashboard := NewDeployDashboard(dc.ctx, totalSteps)
	registerDashboardComponents(&dashboard, dc.ns)

	p := newDeployProgram(dashboard)
	dl = &DashboardController{p: p}

	var step int32
	dc.advanceStep = func() {
		current := atomic.AddInt32(&step, 1)
		dl.UpdateProgress(int(current), totalSteps)
	}

	var res deployPipelineResult
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		defer func() {
			if p != nil {
				p.Quit()
			}
		}()

		res.err = func() error {
			if err := ensureDeployNamespaces(dc.ctx, dc); err != nil {
				return err
			}
			if err := runDeployComponents(dc); err != nil {
				return err
			}

			// Use shared entries for summary (single source of truth)
			res.entries = sharedEntries
			res.elapsed = time.Since(start)
			return nil
		}()
	}()

	if p != nil {
		final, err := p.Run()
		// The window is gone: what the deploy still says goes to the terminal.
		dl.closed.Store(true)
		output.KeepHint()
		if m, ok := final.(deployDashboardModel); ok && m.interrupted {
			output.Warn("Deploy window closed; the deploy goes on and prints below. Ctrl-C again stops kates, which can leave a Helm release half-applied.")
		}
		if err != nil {
			// The dashboard is cosmetic; the deploy goroutine is real work.
			// Returning here used to orphan it mid-helm — the process exited
			// while installs were in flight, leaving releases half-applied.
			// Wait it out; the caller reports the more consequential error.
			<-doneCh
			res.dashboardErr = err
			return res
		}
	}

	<-doneCh // Wait for deployment goroutine to fully exit
	return res
}

// finishDeploy renders the summary of a deploy that succeeded, stores the
// API key in the active context and, with --port-forward, forwards the ports.
func finishDeploy(ctx context.Context, target string, ns resolvedNamespaces, entries []DeploySummaryEntry, elapsed time.Duration) {
	RenderDeployDashboard(ctx, entries, elapsed)
	// Said again here, where the deploy ends, so it isn't lost above the dashboard.
	if deployAdminless && len(adminlessConformityMissing) > 0 {
		output.Warn(conformityRoleHandoff(ns, adminlessConformityMissing))
	}

	// Store the key from Secret kates-api-key in the active context,
	// unless that context holds a key kates did not put there.
	updateActiveContextAPIKey(ctx, ns.app)

	if deployPortForward {
		RunPortForwards(ctx, target, ns.kafka, ns.app, ns.jaeger)
	}
}

// Helpers

var (
	runExecFn      = runExecDefault
	runExecStdinFn = runExecStdinDefault
	runHelmFn      = runHelmDefault
	// resolveVersionPlanFn reads charts and the cluster to decide versions
	// and scope (deploy_versions.go); tests hand deploy a canned plan.
	resolveVersionPlanFn = resolveVersionPlan
	// Reads a command's output. Every command that INSPECTS the cluster used to
	// call exec.CommandContext directly, which meant the deploy tests really
	// shelled out to kubectl: with no cluster they fell into the readiness wait
	// loops and each test sat there for the full two-minute deadline. Routing
	// reads through an injectable function makes those paths testable in the
	// same way the write paths already were.
	runExecOutputFn                                = runExecOutputDefault
	runExecCombinedFn                              = runExecCombinedDefault
	isHelmReleaseDeployedFn                        = isHelmReleaseDeployedDefault
	defaultExecutor         detect.CommandExecutor = detect.NewOSExecutor()
)

var execMutex sync.Mutex

func runExecDefault(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	if deployVerbose {
		// Verbose streams to the terminal, so commands take turns: the lock
		// keeps the output lines of parallel commands from interweaving.
		execMutex.Lock()
		defer execMutex.Unlock()
		// Show the command being run
		dl.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Non-verbose: capture output, show stderr only on error. The command
	// runs outside the lock; its output goes to buffers of its own, so
	// nothing interweaves. It used to run under the lock, which made the
	// deploy groups' goroutines queue behind one ten-minute helm install.
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// Show the error details so failures are never silent
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(outBuf.String())
		}
		if errMsg != "" {
			execMutex.Lock()
			dl.Printf("    %s\n", output.ErrorStyle.Render(errMsg))
			execMutex.Unlock()
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

// runExecOutputDefault captures stdout, like exec.Cmd.Output().
func runExecOutputDefault(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// runExecCombinedDefault captures stdout and stderr, like exec.Cmd.CombinedOutput().
// Used where the caller inspects error text (e.g. "not found") rather than data.
func runExecCombinedDefault(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func runExecStdinDefault(ctx context.Context, name string, args []string, stdinData string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	go func() {
		// A failed write or close ends the child's input early; the child
		// then fails on what it read, and cmd.Run reports that below.
		defer func() { _ = stdin.Close() }()
		_, _ = io.WriteString(stdin, stdinData)
	}()

	execMutex.Lock()
	defer execMutex.Unlock()

	if deployVerbose {
		dl.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
		// stderr is also kept for the error, so a caller can still tell why
		// kubectl failed (ensureDeployNamespace looks for an RBAC refusal).
		var errBuf bytes.Buffer
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
		if runErr := cmd.Run(); runErr != nil {
			if errMsg := strings.TrimSpace(errBuf.String()); errMsg != "" {
				return fmt.Errorf("%w: %s", runErr, errMsg)
			}
			return runErr
		}
		return nil
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(outBuf.String())
		}
		if errMsg != "" {
			dl.Printf("    %s\n", output.ErrorStyle.Render(errMsg))
			return fmt.Errorf("%w: %s", runErr, errMsg)
		}
		return runErr
	}
	return nil
}

func runHelmDefault(ctx context.Context, args ...string) error {
	return runExecFn(ctx, "helm", args...)
}

func isHelmReleaseDeployedDefault(ctx context.Context, release, namespace string) bool {
	// The release must exist AND be in "deployed" status: a failed or
	// half-applied release is reinstalled, not skipped. helm list reads the
	// status with the rest of the row; helm status exited 0 for a failed
	// release too.
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	releases, err := helmReleases(checkCtx, runExecOutputFn, namespace)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(releases, func(r helmRelease) bool { return r.Name == release && r.Deployed() })
}

// cleanupStaleClusterResource removes a cluster-scoped resource that a Helm
// release in another namespace installed, once that release is gone: a
// topology switch leaves clusterrole/kates annotated with the old namespace,
// and Helm refuses to adopt it. While the release still exists the resource
// is its, and is left alone with a note — it used to be deleted whichever
// namespace owned it, which broke a second tenant's stack (clean is careful
// about the same case; deploy was not).
func cleanupStaleClusterResource(ctx context.Context, kind, name, expectedNS string) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runExecOutputFn(checkCtx, "kubectl", "get", kind, name, "-o", `jsonpath={.metadata.annotations.meta\.helm\.sh/release-namespace}{"\t"}{.metadata.annotations.meta\.helm\.sh/release-name}`)
	if err != nil {
		return // resource doesn't exist, nothing to clean
	}
	existingNS, release, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	if existingNS == "" || existingNS == expectedNS {
		return
	}
	if release == "" {
		release = name
	}
	switch live, err := helmReleaseExists(ctx, release, existingNS); {
	case err != nil:
		dl.Printf("    - Keeping %s/%s: owned by release %s in %q, which could not be checked (%v)\n", kind, name, release, existingNS, err)
	case live:
		dl.Printf("    - Keeping %s/%s: release %s in %q still owns it (deploying to %q)\n", kind, name, release, existingNS, expectedNS)
	default:
		dl.Printf("    - Cleaning stale %s/%s (owned by namespace %q, whose release %s is gone; deploying to %q)\n", kind, name, existingNS, release, expectedNS)
		delCtx, delCancel := context.WithTimeout(ctx, 10*time.Second)
		defer delCancel()
		_, _ = runExecCombinedFn(delCtx, "kubectl", "delete", kind, name, "--ignore-not-found")
	}
}

// helmReleaseExists says whether helm knows release in namespace, in any
// status. Only helm's own "not found" counts as gone; any other failure is
// returned, so a cluster that does not answer never reads as "gone".
func helmReleaseExists(ctx context.Context, release, namespace string) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runExecCombinedFn(checkCtx, "helm", "status", release, "-n", namespace)
	switch {
	case err == nil:
		return true, nil
	case strings.Contains(strings.ToLower(string(out)), "not found"):
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s", err, firstLine(strings.TrimSpace(string(out))))
	}
}
