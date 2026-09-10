package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// kates migrate — the cross-version migration lab.
//
// The front door takes a pair of versions (`up --from 2.8.2 [--to 4.3.0]`)
// and stands up an old Kafka beside the platform's primary, a MirrorMaker 2
// between them, a corpus, the verification, the cutover and the teardown;
// the building blocks underneath (`source`, `mirror`, `target`, `image`)
// are what it is composed from. The design is docs/kafka-multi-version-
// deploy-plan.md §3.9 and docs/mirror-maker2-cli-migration-plan.md §4; the
// shell it replaces is scripts/test-mm2-migration.sh, mm2-kafka-cli.sh and
// build-legacy-kafka-image.sh.
//
// Every process — helm, kubectl, docker, kind — runs through defaultRunner
// (cmd/runner.go), the one seam a test replaces; no shell program is ever
// composed, and no credential is ever an argument.

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Cross-version Kafka migration with MirrorMaker 2",
	Long: `Stand up an old Kafka beside the platform's primary, mirror it with
MirrorMaker 2, prove the data and the consumer offsets arrived, rehearse the
cutover and tear everything down — from one pair of versions.

The front door:
  kates migrate pairs                       every old → new pair this cluster can stand up
  kates migrate plan --from 2.8.2           what up would create; nothing changes
  kates migrate up   --from 2.8.2 [--to 4.3.0]
  kates migrate up   --from 2.8.2 --from 3.9.1   several sources into one target
  kates migrate status | verify | cutover | rollback | down [--name m282-430]
  kates migrate run  --from 2.8.2 [--keep] [--skip-build] [-o json]

--read-only-source keeps every MirrorMaker write on the target (KIP-716), so
a source you may only read needs Read and Describe and nothing more.

The building blocks it is made of:
  kates migrate image build --version 2.8.2 --load
  kates migrate source deploy|status|remove
  kates migrate mirror deploy|status|cutover|rollback|remove
  kates migrate target topics|offsets <topic>|groups|group <id>

Run it from the repository root: the charts are addressed as charts/…, the
client image comes from versions.env.`,
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}

// Paths and defaults shared by the migrate commands.
const (
	// migrateBuildDir is where the generated values files land, one
	// directory per lab: .build/migrate/<name>/.
	migrateBuildDir = ".build/migrate"
	// migrateDefaultTimeout is the per-phase wait budget, the scripts' 600s.
	migrateDefaultTimeout = 600
	// migrateMirrorInstallTimeout bounds the mirror's helm install (its
	// preflight hook Job runs inside it).
	migrateMirrorInstallTimeout = "10m"
	// migrateTargetReadyTimeout is how long the prerequisites wait for the
	// target Kafka, the scripts' 300s.
	migrateTargetReadyTimeout = 300 * time.Second
	// migrateClientImageRepo is the repository of the Strimzi Kafka image
	// the target's client pod runs — the image the MirrorMaker 2 workers run,
	// which is what makes its verdicts evidence.
	migrateClientImageRepo = "quay.io/strimzi/kafka"
	// migrateCRDName is the CRD the prerequisites check for.
	migrateCRDName = "kafkamirrormaker2s.kafka.strimzi.io"
	// migrateCutoverSettle is the pause before the cutover rehearsal takes
	// its baseline, and again before it takes the second reading.
	migrateCutoverSettle = 45 * time.Second
	// migrateCutoverExtra is how many records the rehearsal produces after
	// the cutover to prove the target does not move.
	migrateCutoverExtra = 50
)

// The seams the tests replace: waiting and the clock. Every wait in the
// migrate commands goes through migrateSleep so a test can run a 600-second
// poll loop in microseconds, and every deadline is computed from migrateNow
// so the same test can make it expire.
var (
	migrateSleepFn = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	migrateNowFn = time.Now
)

func migrateSleep(ctx context.Context, d time.Duration) error { return migrateSleepFn(ctx, d) }
func migrateNow() time.Time                                   { return migrateNowFn() }

// requireMigrateRepoRoot checks that the charts and the pins the migration
// needs are where the CLI expects them — relative to the working directory,
// the same convention as kates deploy (see cluster_gate.go).
func requireMigrateRepoRoot() error {
	for _, p := range []string{
		filepath.Join(migrate.MirrorChartPath, "Chart.yaml"),
		filepath.Join(migrate.SourceChartPath, "Chart.yaml"),
		migrate.PinsFile,
	} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s is not readable from here.\n"+
				"  kates migrate needs the charts and versions.env of the kates repo — run from the repo root.", p)
		}
	}
	return nil
}

// migrateTarget is what the cluster says about the target Kafka.
type migrateTarget struct {
	Cluster, Namespace string
	// Found is whether the Kafka CR exists.
	Found bool
	// Version is spec.kafka.version, else status.kafkaVersion.
	Version kafkaversion.Version
	Ready   bool
	// Brokers is the broker count summed over the cluster's broker node
	// pools (0 when unknown).
	Brokers int
	// HasSecret says the kates-mm2 credential Secret exists.
	HasSecret bool
}

// migrateEnv is the environment a lab is planned against: the operators on
// the cluster, the one that owns the target's namespace, its window and
// scope, the target Kafka, whether the cluster is kind, and the repo pins.
type migrateEnv struct {
	Operators []strimzi.Operator
	// Primary is the operator whose watch set contains the target's
	// namespace (or "*"); nil when planned against the pin.
	Primary *strimzi.Operator
	// Scope is migrate.ScopeCluster or migrate.ScopeNamespace.
	Scope string
	// Window is the primary operator's Kafka window.
	Window kafkaversion.Window
	// StrimziVersion is the primary operator's version (the pin without an
	// operator).
	StrimziVersion string
	// OperatorNamespace is where the primary operator runs.
	OperatorNamespace string
	Target            migrateTarget
	IsKind            bool
	Pins              migrate.Pins
	// FromPin says no operator was found and the window is the vendored
	// chart's (plan and pairs only).
	FromPin bool
	// Notes are what discovery wants the user to know.
	Notes []string
}

// primaryScope is the lab's spelling of the primary operator's scope.
func (e *migrateEnv) primaryScope() string {
	if e.Primary != nil && e.Primary.Scope == strimzi.ScopeNamespaces {
		return migrate.ScopeNamespace
	}
	return migrate.ScopeCluster
}

// operatorLine describes the primary operator for a plan or a report
// header: "Strimzi 1.1.0 in strimzi-operator (cluster scope, watches *)".
func (e *migrateEnv) operatorLine() string {
	if e.Primary == nil {
		return fmt.Sprintf("Strimzi %s (pinned — no operator found on the cluster)", e.StrimziVersion)
	}
	scope := "cluster scope"
	if e.Primary.Scope == strimzi.ScopeNamespaces {
		scope = "namespace scope"
	}
	return fmt.Sprintf("Strimzi %s in %s (%s, watches %s)", e.Primary.Version, e.Primary.Namespace, scope, strings.Join(e.Primary.Watches, ","))
}

// clientImage is the Strimzi Kafka image of the target's line: the primary
// operator's version paired with the target's Kafka version, the pin when
// either is unknown.
func (e *migrateEnv) clientImage() string {
	if e.StrimziVersion != "" && e.Target.Version != (kafkaversion.Version{}) {
		return fmt.Sprintf("%s:%s-kafka-%s", migrateClientImageRepo, e.StrimziVersion, e.Target.Version)
	}
	return e.Pins.KafkaImage
}

// discoverMigrateEnv reads the cluster: the installed operators
// (strimzi.InstalledOperators), the primary among them, the target Kafka CR,
// its broker pools, the kates-mm2 Secret, the kind probe and versions.env.
// With allowPin, a cluster without any operator is planned against the
// vendored chart's window instead of failing — for plan and pairs, which
// create nothing.
func discoverMigrateEnv(ctx context.Context, targetCluster, targetNamespace string, allowPin bool) (*migrateEnv, error) {
	if err := requireMigrateRepoRoot(); err != nil {
		return nil, err
	}
	pins, err := migrate.ReadPins(".")
	if err != nil {
		return nil, err
	}
	env := &migrateEnv{Pins: pins, Target: migrateTarget{Cluster: targetCluster, Namespace: targetNamespace}}

	ops, opsErr := strimzi.InstalledOperators(ctx, defaultRunner)
	if opsErr != nil && !allowPin {
		return nil, fmt.Errorf("discover the Strimzi operators (is the cluster reachable? kubectl cluster-info): %w", opsErr)
	}
	env.Operators = ops
	if len(ops) == 0 || opsErr != nil {
		if !allowPin {
			return nil, errors.New("no Strimzi Cluster Operator found on this cluster (no pod labelled strimzi.io/kind=cluster-operator) — run kates deploy first")
		}
		if err := env.planAgainstPin(opsErr); err != nil {
			return nil, err
		}
	} else {
		primary, err := pickPrimaryOperator(ops, targetNamespace)
		if err != nil {
			return nil, err
		}
		env.Primary = primary
		env.Window = primary.Window
		env.StrimziVersion = primary.Version
		env.OperatorNamespace = primary.Namespace
		if primary.AppVersion != "" && primary.AppVersion != primary.Version {
			env.Notes = append(env.Notes, fmt.Sprintf("operator image tag %s and Helm APP VERSION %s disagree — someone changed one by hand", primary.Version, primary.AppVersion))
		}
	}
	env.Scope = env.primaryScope()

	if err := env.readTarget(ctx); err != nil {
		if !allowPin {
			return nil, err
		}
		env.Notes = append(env.Notes, err.Error())
	}
	if !env.Target.Found {
		if !allowPin {
			return nil, fmt.Errorf("no Kafka %q in namespace %s — the target must exist before a lab is created (kates deploy)", targetCluster, targetNamespace)
		}
		if newest, ok := env.Window.Newest(); ok {
			env.Target.Version = newest
		}
		env.Notes = append(env.Notes, fmt.Sprintf("no Kafka %q in %s: planned against the newest version of the window, %s", targetCluster, targetNamespace, env.Target.Version))
	}
	if env.Target.Version == (kafkaversion.Version{}) {
		if newest, ok := env.Window.Newest(); ok {
			env.Target.Version = newest
			env.Notes = append(env.Notes, fmt.Sprintf("Kafka %s states no version: assuming the operator's default, %s", targetCluster, newest))
		}
	}
	env.IsKind = migrateDetectKind(ctx)
	return env, nil
}

// planAgainstPin fills the window from the vendored operator chart when the
// cluster has no operator to ask.
func (e *migrateEnv) planAgainstPin(cause error) error {
	tgz, err := strimzi.VendoredChartPath(".")
	if err != nil {
		return fmt.Errorf("no Strimzi operator on the cluster and no vendored chart to plan against: %w", err)
	}
	c, err := strimzi.ReadChart(tgz)
	if err != nil {
		return fmt.Errorf("read the vendored operator chart: %w", err)
	}
	e.Window = c.Window
	e.StrimziVersion = c.Version
	e.OperatorNamespace = "strimzi-operator"
	e.FromPin = true
	note := fmt.Sprintf("no Strimzi operator found on the cluster — planned against the pinned Strimzi %s (window %s)", c.Version, c.Window)
	if cause != nil {
		note = fmt.Sprintf("could not read the cluster's operators (%v) — planned against the pinned Strimzi %s (window %s)", cause, c.Version, c.Window)
	}
	e.Notes = append(e.Notes, note)
	return nil
}

// pickPrimaryOperator finds the operator that reconciles the target's
// namespace: the cluster-wide one, or the namespace-scoped one whose watch
// set has it. A mixed installation is refused, as deploy refuses it.
func pickPrimaryOperator(ops []strimzi.Operator, targetNamespace string) (*strimzi.Operator, error) {
	if strimzi.Shape(ops) == strimzi.ShapeMixed {
		var lines []string
		for _, op := range ops {
			lines = append(lines, fmt.Sprintf("%s/%s (%s, watches %s)", op.Namespace, op.Name, op.Version, strings.Join(op.Watches, ",")))
		}
		return nil, fmt.Errorf("a cluster-wide Strimzi operator and a namespace-scoped one are both installed — a mixed installation is refused until fixed (kates doctor):\n  %s", strings.Join(lines, "\n  "))
	}
	var matches []*strimzi.Operator
	for i := range ops {
		op := &ops[i]
		for _, w := range op.Watches {
			if w == strimzi.WatchAll || w == targetNamespace {
				matches = append(matches, op)
				break
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		var names []string
		for _, op := range ops {
			names = append(names, fmt.Sprintf("%s (%s, watches %s)", op.Namespace, op.Version, strings.Join(op.Watches, ",")))
		}
		return nil, fmt.Errorf("no Strimzi operator watches namespace %s; installed: %s", targetNamespace, strings.Join(names, "; "))
	default:
		var names []string
		for _, op := range matches {
			names = append(names, op.Namespace+" ("+op.Version+")")
		}
		return nil, fmt.Errorf("%d Strimzi operators reconcile namespace %s (%s) — every watch set must be disjoint", len(matches), targetNamespace, strings.Join(names, ", "))
	}
}

// readTarget reads the target Kafka CR, its broker pools and its kates-mm2
// Secret.
func (e *migrateEnv) readTarget(ctx context.Context) error {
	t := &e.Target
	out, err := defaultRunner.Run(ctx, "kubectl", "-n", t.Namespace, "get", "kafka", t.Cluster, "--ignore-not-found", "-o", "json")
	if err != nil {
		return fmt.Errorf("read Kafka %s/%s: %w", t.Namespace, t.Cluster, err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var cr struct {
		Spec struct {
			Kafka struct {
				Version  string `json:"version"`
				Replicas int    `json:"replicas"`
			} `json:"kafka"`
		} `json:"spec"`
		Status struct {
			KafkaVersion string `json:"kafkaVersion"`
			Conditions   []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		return fmt.Errorf("parse Kafka %s/%s: %w", t.Namespace, t.Cluster, err)
	}
	t.Found = true
	for _, v := range []string{cr.Spec.Kafka.Version, cr.Status.KafkaVersion} {
		if v == "" {
			continue
		}
		parsed, err := kafkaversion.Parse(v)
		if err != nil {
			return fmt.Errorf("Kafka %s/%s states version %q: %w", t.Namespace, t.Cluster, v, err)
		}
		t.Version = parsed
		break
	}
	for _, c := range cr.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			t.Ready = true
		}
	}
	t.Brokers = cr.Spec.Kafka.Replicas
	if pools, err := defaultRunner.Run(ctx, "kubectl", "-n", t.Namespace, "get", "kafkanodepool", "-l", "strimzi.io/cluster="+t.Cluster, "-o", "json"); err == nil {
		if n := brokerCountFromPools(pools); n > 0 {
			t.Brokers = n
		}
	}
	secret, err := defaultRunner.Run(ctx, "kubectl", "-n", t.Namespace, "get", "secret", migrate.TargetUser, "--ignore-not-found", "-o", "jsonpath={.metadata.name}")
	if err != nil {
		return fmt.Errorf("read Secret %s/%s: %w", t.Namespace, migrate.TargetUser, err)
	}
	t.HasSecret = strings.TrimSpace(secret) != ""
	return nil
}

// brokerCountFromPools sums spec.replicas over the KafkaNodePools that carry
// the broker role.
func brokerCountFromPools(listJSON string) int {
	var list struct {
		Items []struct {
			Spec struct {
				Replicas int      `json:"replicas"`
				Roles    []string `json:"roles"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		return 0
	}
	n := 0
	for _, p := range list.Items {
		for _, r := range p.Spec.Roles {
			if r == "broker" {
				n += p.Spec.Replicas
				break
			}
		}
	}
	return n
}

// migrateDetectKind is deploy's quickDetectKind probe (the kindnet
// DaemonSet) issued through the runner, so a test can script it.
func migrateDetectKind(ctx context.Context) bool {
	out, err := defaultRunner.Run(ctx, "kubectl", "get", "daemonset", "kindnet", "-n", "kube-system", "--ignore-not-found", "--no-headers")
	return err == nil && strings.TrimSpace(out) != ""
}

// migratePairFlags are the flags of up, plan and run.
type migratePairFlags struct {
	// From is repeatable: one entry per source cluster mirrored into the
	// target by the one MirrorMaker 2 release.
	From                 []string
	To                   string
	Name                 string
	SourceProvider       string
	SourceStrimziVersion string
	Policy               string
	Topics               string
	Messages             int
	SASL, Plaintext      bool
	ReadOnlySource       bool
	ValuesSource         string
	ValuesMirror         string
	Yes                  bool
	Interactive          bool
	TargetCluster        string
	TargetNamespace      string
	Registry             string
	Timeout              int
	// run only
	Keep, SkipBuild bool
	// SourceVersion is the script-era alias of --from on run.
	SourceVersion string
}

// addMigratePairFlags binds the pair flags to cmd.
func addMigratePairFlags(cmd *cobra.Command, f *migratePairFlags) {
	cmd.Flags().StringArrayVar(&f.From, "from", nil, "Source Kafka version, x.y.z (required; repeat it to mirror several sources into one target)")
	cmd.Flags().StringVar(&f.To, "to", "", "Target Kafka version (default: the primary as it runs)")
	cmd.Flags().StringVar(&f.Name, "name", "", "Lab name (default: m<from>[-<from>…]-<to> with the dots removed, e.g. m282-430)")
	cmd.Flags().StringVar(&f.SourceProvider, "source-provider", "auto", "Who runs the source: auto, strimzi or legacy")
	cmd.Flags().StringVar(&f.SourceStrimziVersion, "source-strimzi-version", "", "Operator version of a Strimzi source in its own namespace (namespace scope)")
	cmd.Flags().StringVar(&f.Policy, "policy", migrate.PolicyIdentity, "Replication policy: identity (keep topic names) or default (<alias>.<topic>)")
	cmd.Flags().StringVar(&f.Topics, "topics", "", "Corpus topics to seed and mirror, comma-separated (default "+migrate.DefaultTopic+", and "+migrate.DefaultTopic+".<alias> per source with several --from)")
	cmd.Flags().IntVar(&f.Messages, "messages", migrate.DefaultMessages, "Records to produce per topic")
	cmd.Flags().BoolVar(&f.SASL, "sasl", false, "Put the legacy source behind its SASL/PLAIN listener")
	cmd.Flags().BoolVar(&f.ReadOnlySource, "read-only-source", false,
		"Write nothing to the source: offset-syncs go to the target (KIP-716), so the source principal needs Read and Describe only.\n"+
			"The lab owns its own legacy-kafka source and does not need this; it is for a --from-bootstrap cluster you may only read.")
	cmd.Flags().BoolVar(&f.Plaintext, "plaintext", false, "Keep the legacy source on its plaintext listener (the default)")
	cmd.Flags().StringVar(&f.ValuesSource, "values-source", "", "Extra values file layered on the source's generated values")
	cmd.Flags().StringVar(&f.ValuesMirror, "values-mirror", "", "Extra values file layered on the mirror's generated values")
	cmd.Flags().BoolVarP(&f.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	cmd.Flags().BoolVarP(&f.Interactive, "interactive", "i", false, "Pick the pair, the policy and the corpus interactively")
	cmd.Flags().StringVar(&f.TargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Name of the target Kafka (the platform's primary)")
	cmd.Flags().StringVar(&f.TargetNamespace, "target-namespace", migrate.DefaultTargetNamespace, "Namespace of the target Kafka")
	cmd.Flags().StringVar(&f.Registry, "registry", kafkaversion.DefaultLegacyRegistry, "Registry of the built legacy images (kates migrate image build --registry)")
	cmd.Flags().IntVar(&f.Timeout, "timeout", migrateDefaultTimeout, "Per-phase wait budget in seconds")
}

// migrateResolution is a pair resolved against the environment: the lab,
// what the user should know about how it was resolved, and what this
// version of the CLI cannot yet create.
type migrateResolution struct {
	Env  *migrateEnv
	Opts migrate.Options
	Lab  *migrate.Lab
	// SourceOperator describes who runs the first source, for the plan and
	// the report header; Sources carries the same for every source.
	SourceOperator string
	Sources        []resolvedSource
	// Notes are printed once by plan and up; Warnings likewise, styled as
	// warnings.
	Notes, Warnings []string
	// Unimplemented is non-empty when plan can describe the lab but up
	// cannot create it in this version.
	Unimplemented []string
	// TargetAdditional says --to asked for a target other than the primary.
	TargetAdditional bool
}

// Timeout is the per-phase budget as a duration.
func (f *migratePairFlags) timeout() time.Duration {
	if f.Timeout <= 0 {
		return migrateDefaultTimeout * time.Second
	}
	return time.Duration(f.Timeout) * time.Second
}

// splitTopics reads a comma-separated topic list.
func splitTopics(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolveMigratePair applies the §3.9 rules to the flags and the
// environment and derives the lab. Every refusal names the window, the
// scope and the way out. With several --from values every source is
// resolved by the same rules and the lab fans them into one release.
func resolveMigratePair(env *migrateEnv, f *migratePairFlags) (*migrateResolution, error) {
	if len(f.From) == 0 && f.SourceVersion != "" {
		f.From = []string{f.SourceVersion}
	}
	if len(f.From) == 0 {
		return nil, errors.New("--from is required: the source Kafka version, x.y.z (kates migrate pairs lists the candidates)")
	}
	froms := make([]kafkaversion.Version, 0, len(f.From))
	for _, s := range f.From {
		v, err := kafkaversion.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("--from: %w", err)
		}
		froms = append(froms, v)
	}
	from := froms[0]
	if f.SASL && f.Plaintext {
		return nil, errors.New("--sasl and --plaintext are mutually exclusive")
	}
	switch f.SourceProvider {
	case "", "auto", string(kafkaversion.ProviderStrimzi), string(kafkaversion.ProviderLegacy):
	default:
		return nil, fmt.Errorf("--source-provider %q: want auto, strimzi or legacy", f.SourceProvider)
	}

	res := &migrateResolution{Env: env}

	// The source.
	resolved := make([]resolvedSource, 0, len(froms))
	for _, v := range froms {
		r, err := resolveMigrateSource(env, f, v, res)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, r)
	}
	provider, mode, other := resolved[0].Provider, resolved[0].Mode, resolved[0].Operator
	res.SourceOperator = resolved[0].OperatorLine
	res.Sources = resolved

	// The target.
	to := env.Target.Version
	targetIsPrimary := true
	if f.To != "" {
		parsed, err := kafkaversion.Parse(f.To)
		if err != nil {
			return nil, fmt.Errorf("--to: %w", err)
		}
		if parsed != env.Target.Version {
			if !env.Window.Supports(parsed) {
				return nil, fmt.Errorf("--to %s is not in the primary operator's window (Strimzi %s supports: %s).\n"+
					"  A newer target needs a newer operator: kates deploy --strimzi-version <v> (kates versions strimzi lists them);\n"+
					"  an older one is a source, not a target.", parsed, env.StrimziVersion, env.Window)
			}
			targetIsPrimary = false
			res.TargetAdditional = true
			res.Unimplemented = append(res.Unimplemented, fmt.Sprintf("an additional target (Kafka %s beside the %s primary, reconciled by the primary's operator) is not yet implemented in this version — omit --to to migrate onto the primary", parsed, env.Target.Version))
		}
		to = parsed
	}
	if to == (kafkaversion.Version{}) {
		return nil, errors.New("the target's Kafka version is unknown: pass --to, or deploy the primary first")
	}
	if !from.Less(to) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("--from %s is not older than --to %s — MirrorMaker does not care about direction, only the user does", from, to))
	}

	opts := migrate.Options{
		Name:                 f.Name,
		From:                 from,
		To:                   to,
		SourceProvider:       provider,
		SourceMode:           mode,
		SourceStrimziVersion: f.SourceStrimziVersion,
		TargetIsPrimary:      targetIsPrimary,
		Policy:               f.Policy,
		Topics:               splitTopics(f.Topics),
		Messages:             f.Messages,
		SASL:                 f.SASL,
		ReadOnlySource:       f.ReadOnlySource,
		Registry:             f.Registry,
		StrimziVersion:       env.StrimziVersion,
		Scope:                env.Scope,
	}
	if targetIsPrimary {
		opts.TargetCluster, opts.TargetNamespace = env.Target.Cluster, env.Target.Namespace
	}
	if provider == kafkaversion.ProviderStrimzi && f.SourceStrimziVersion == "" && other != nil {
		opts.SourceStrimziVersion = other.Version
	}
	for _, r := range resolved[1:] {
		spec := migrate.SourceSpec{From: r.From, Provider: r.Provider, Mode: r.Mode}
		if r.Provider == kafkaversion.ProviderStrimzi {
			spec.StrimziVersion = f.SourceStrimziVersion
			if spec.StrimziVersion == "" && r.Operator != nil {
				spec.StrimziVersion = r.Operator.Version
			}
		}
		opts.AlsoFrom = append(opts.AlsoFrom, spec)
	}
	lab, err := migrate.New(opts)
	if err != nil {
		return nil, err
	}
	res.Opts, res.Lab = lab.Options, lab
	return res, nil
}

// resolvedSource is one --from resolved against the environment: who runs
// it, in which mode, and the sentence the plan and the report print for it.
type resolvedSource struct {
	From         kafkaversion.Version
	Provider     kafkaversion.Provider
	Mode         kafkaversion.LegacyMode
	OperatorLine string
	// Operator is the installed namespace-scoped operator that would run it,
	// when there is one.
	Operator *strimzi.Operator
}

// resolveMigrateSource applies the §3.9 rules to one source version and
// records its notes, warnings and refusals on res. Several --from values are
// each put through it, so a Strimzi source in the set keeps the same
// "not yet implemented" answer it has on its own — plan describes it, up
// refuses it — rather than being widened silently by the company it keeps.
func resolveMigrateSource(env *migrateEnv, f *migratePairFlags, from kafkaversion.Version, res *migrateResolution) (resolvedSource, error) {
	provider, mode, err := kafkaversion.ProviderFor(from, env.Window)
	if err != nil {
		return resolvedSource{}, fmt.Errorf("--from %s refused: %w", from, err)
	}
	out := resolvedSource{From: from}
	category, other := classifyFrom(from, env)
	out.Operator = other
	opName := fmt.Sprintf("Strimzi %s", env.StrimziVersion)
	explicit := kafkaversion.Provider(f.SourceProvider)
	switch {
	case explicit == kafkaversion.ProviderLegacy:
		provider, mode = kafkaversion.ProviderLegacy, kafkaversion.LegacyModeFor(from)
		if category == fromInWindow {
			res.Notes = append(res.Notes, fmt.Sprintf("Kafka %s is in the primary operator's window (%s); running it under the legacy-kafka chart as asked (%s, no operator)", from, env.Window, legacyModeWords(mode)))
		}
	case category == fromInWindow:
		provider, mode = kafkaversion.ProviderStrimzi, ""
		if env.Scope == migrate.ScopeNamespace {
			out.OperatorLine = fmt.Sprintf("%s, a co-located operator at the primary's version in its own namespace", opName)
		} else {
			out.OperatorLine = fmt.Sprintf("%s, the primary's operator (cluster-wide)", opName)
		}
		res.Unimplemented = append(res.Unimplemented, fmt.Sprintf("a Strimzi source (Kafka %s is in the primary operator's window %s) is not yet implemented in this version — use --source-provider legacy to run it under the legacy-kafka chart (%s, no operator)", from, env.Window, legacyModeWords(kafkaversion.LegacyModeFor(from))))
	case category == fromDroppedLine:
		if env.Scope == migrate.ScopeCluster {
			if explicit == kafkaversion.ProviderStrimzi {
				return resolvedSource{}, errors.New(clusterScopeRefusal(from, env))
			}
			provider, mode = kafkaversion.ProviderLegacy, kafkaversion.LegacyModeFor(from)
			res.Notes = append(res.Notes, clusterScopeSentence(from, env)+" The source runs under the legacy-kafka chart instead ("+legacyModeWords(mode)+").")
		} else {
			provider, mode = kafkaversion.ProviderStrimzi, ""
			switch {
			case f.SourceStrimziVersion != "":
				out.OperatorLine = fmt.Sprintf("Strimzi %s, co-located in the source's namespace (older than the primary's %s)", f.SourceStrimziVersion, env.StrimziVersion)
			case other != nil:
				out.OperatorLine = fmt.Sprintf("Strimzi %s in %s, an installed namespace-scoped operator whose window has %s", other.Version, other.Namespace, from)
			default:
				out.OperatorLine = "the newest eligible Strimzi whose window has " + from.String() + " (kates versions strimzi lists them; --source-strimzi-version chooses one)"
			}
			res.Unimplemented = append(res.Unimplemented, fmt.Sprintf("a Strimzi source under its own operator (Kafka %s, the dropped line beside the %s primary) is not yet implemented in this version — use --source-provider legacy to run it under the legacy-kafka chart (%s, no operator)", from, env.StrimziVersion, legacyModeWords(kafkaversion.LegacyModeFor(from))))
		}
	default: // fromLegacyOnly, fromUnlisted
		if explicit == kafkaversion.ProviderStrimzi {
			return resolvedSource{}, fmt.Errorf("%s\n  Run it without an operator: kates migrate up --from %s --source-provider legacy", noStrimziReason(from, env), from)
		}
		provider, mode = kafkaversion.ProviderLegacy, kafkaversion.LegacyModeFor(from)
		res.Notes = append(res.Notes, noStrimziReason(from, env))
	}
	if provider == kafkaversion.ProviderLegacy {
		out.OperatorLine = "none — plain StatefulSets from charts/legacy-kafka"
	}
	out.Provider, out.Mode = provider, mode
	return out, nil
}

// fromCategory classifies a source version against the environment.
type fromCategory int

const (
	// fromInWindow: the primary operator runs it.
	fromInWindow fromCategory = iota
	// fromDroppedLine: the same Kafka line as the window but older than its
	// oldest entry — a line the primary's operator has dropped and an
	// eligible older operator of the same CRD generation may still run.
	fromDroppedLine
	// fromLegacyOnly: an older line (2.x, 3.x) that no Strimzi of this
	// platform's CRD generation ever ran.
	fromLegacyOnly
	// fromUnlisted: the same line, not older than the window's oldest, but
	// not listed (a patch the operator skipped).
	fromUnlisted
)

// classifyFrom decides the category and, when an installed namespace-scoped
// operator other than the primary runs the version, returns it.
func classifyFrom(v kafkaversion.Version, env *migrateEnv) (fromCategory, *strimzi.Operator) {
	if env.Window.Supports(v) {
		return fromInWindow, nil
	}
	for i := range env.Operators {
		op := &env.Operators[i]
		if env.Primary != nil && op.Namespace == env.Primary.Namespace && op.Name == env.Primary.Name {
			continue
		}
		if op.Scope == strimzi.ScopeNamespaces && op.Window.Supports(v) {
			return fromDroppedLine, op
		}
	}
	oldest := kafkaversion.Version{}
	if len(env.Window) > 0 {
		oldest = env.Window[0]
	}
	switch {
	case oldest == (kafkaversion.Version{}):
		return fromLegacyOnly, nil
	case v.Major < oldest.Major:
		return fromLegacyOnly, nil
	case v.Less(oldest):
		return fromDroppedLine, nil
	default:
		return fromUnlisted, nil
	}
}

// clusterScopeSentence is the §3.1 statement: the cluster-wide operator's
// window is the law, and no second operator can sit beside it.
func clusterScopeSentence(v kafkaversion.Version, env *migrateEnv) string {
	return fmt.Sprintf("Kafka %s cannot run under Strimzi %s, the cluster-wide operator on this cluster (supported: %s). A cluster-wide operator watches every namespace, so no second Strimzi can be installed beside it.",
		v, env.StrimziVersion, env.Window)
}

// clusterScopeRefusal is the sentence with both ways out, printed when
// --source-provider strimzi asks for what cluster scope cannot give.
func clusterScopeRefusal(v kafkaversion.Version, env *migrateEnv) string {
	return fmt.Sprintf("%s\n"+
		"  - run it without an operator:   kates migrate up --from %s --source-provider legacy\n"+
		"  - or give it its own operator, which needs namespace-scoped operators:\n"+
		"        kates deploy --operator-scope namespace\n"+
		"        kates migrate up --from %s --source-provider strimzi --source-strimzi-version <an operator whose window has %s — kates versions strimzi lists them>",
		clusterScopeSentence(v, env), v, v, v)
}

// noStrimziReason says why no Strimzi can run a 2.x or 3.x source beside
// this platform (§2.3 of the multi-version plan).
func noStrimziReason(v kafkaversion.Version, env *migrateEnv) string {
	return fmt.Sprintf("No Strimzi that can sit beside this platform's CRDs runs Kafka %s: the operators that ran the 2.x and 3.x lines serve the v1beta2 API only, this platform renders v1, and the cluster's Strimzi %s supports %s. The legacy-kafka chart (plain StatefulSets, no operator) is the design for it, not a fallback.",
		v, env.StrimziVersion, env.Window)
}

// legacyModeWords spells a legacy mode for a message.
func legacyModeWords(m kafkaversion.LegacyMode) string {
	switch m {
	case kafkaversion.LegacyZooKeeper:
		return "ZooKeeper, built image"
	case kafkaversion.LegacyKRaftBuilt:
		return "KRaft, built image"
	case kafkaversion.LegacyKRaftOfficial:
		return "KRaft, official apache/kafka image"
	default:
		return string(m)
	}
}

// needsBuiltImage says a source's image is one Dockerfile.legacy-kafka
// builds (no upstream image exists below 3.7.0).
func needsBuiltImage(s *migrate.Source) bool {
	return s.Provider == kafkaversion.ProviderLegacy && s.Mode != kafkaversion.LegacyKRaftOfficial
}

// labBuildDir is where a lab's generated files live.
func labBuildDir(name string) string {
	return filepath.Join(migrateBuildDir, name)
}

// sourceValuesPath and mirrorValuesPath are the generated values files.
func sourceValuesPath(name string) string {
	return filepath.Join(labBuildDir(name), "source-values.yaml")
}

// sourceValuesPathFor is the generated values file of one source: the lab's
// single source-values.yaml, or one file per source in a fan-in.
func sourceValuesPathFor(l *migrate.Lab, s *migrate.Source) string {
	if !l.FanIn() {
		return sourceValuesPath(l.Name)
	}
	return filepath.Join(labBuildDir(l.Name), "source-values-"+s.Alias+".yaml")
}
func mirrorValuesPath(name string) string {
	return filepath.Join(labBuildDir(name), "mirror-values.yaml")
}
func rollbackValuesPath(name string) string {
	return filepath.Join(labBuildDir(name), "rollback-values.yaml")
}

// helmSourceArgs is the helm command that installs one legacy source:
// upgrade --install <release> charts/legacy-kafka -n <ns> -f <generated>
// [-f <user overlay>] --wait --timeout <T>s.
func helmSourceArgs(s *migrate.Source, valuesPath, userValues string, timeout time.Duration) []string {
	args := []string{"upgrade", "--install", s.Release, migrate.SourceChartPath, "-n", s.Namespace, "-f", valuesPath}
	if userValues != "" {
		args = append(args, "-f", userValues)
	}
	return append(args, "--wait", "--timeout", formatSeconds(timeout))
}

// helmMirrorArgs is the helm command that installs the mirror: the kind
// overlay first when on kind (Helm replaces lists wholesale, and that
// overlay's mirrors: is a loopback the era preset must override), then the
// era preset, then the generated values, then the user's overlay.
func helmMirrorArgs(l *migrate.Lab, isKind bool, valuesPath, userValues string) []string {
	args := []string{"upgrade", "--install", l.MirrorRelease, migrate.MirrorChartPath, "-n", l.MirrorNamespace}
	if isKind {
		args = append(args, "-f", migrate.MirrorChartPath+"/values-kind.yaml")
	}
	args = append(args, "-f", migrate.EraOverlayPath(l.From), "-f", valuesPath)
	if userValues != "" {
		args = append(args, "-f", userValues)
	}
	return append(args, "--timeout", migrateMirrorInstallTimeout)
}

// helmCutoverArgs applies the cutover overlay on top of whatever the
// release was installed with (--reuse-values keeps every file's values).
func helmCutoverArgs(release, namespace string, timeout time.Duration) []string {
	return []string{"upgrade", release, migrate.MirrorChartPath, "-n", namespace, "--reuse-values",
		"-f", migrate.CutoverValues(), "--timeout", formatSeconds(timeout)}
}

// helmRollbackArgs reverses a cutover with a generated overlay that turns
// the cutover block off again.
func helmRollbackArgs(release, namespace, rollbackValues string, timeout time.Duration) []string {
	return []string{"upgrade", release, migrate.MirrorChartPath, "-n", namespace, "--reuse-values",
		"-f", rollbackValues, "--timeout", formatSeconds(timeout)}
}

// rollbackValues is the overlay that reverses values-cutover.yaml: the
// cutover block off, automatic restarts back on (the chart's default).
const rollbackValues = `# Written by kates migrate rollback: reverses charts/mirror-maker2/values-cutover.yaml.
# stopped → running resumes from the committed offsets; anything produced to the
# TARGET in the meantime is not replicated backwards — a rollback after producers
# have moved is a data merge, not a switch.
cutover:
  enabled: false
autoRestart:
  enabled: true
`

func formatSeconds(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs) + "s"
}

// writeLabFile writes a generated file under the lab's build directory with
// owner-only permissions (a source values file may carry a SASL password).
func writeLabFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// printMigrateReport prints a report: the header as key/value lines, the
// assertion table (RESULT, ASSERTION, DETAIL — the scripts' columns, styled
// by output.Table) and the counts; -o json prints the document instead.
func printMigrateReport(r *migrate.Report) {
	if outputMode == "json" {
		data, err := r.JSON()
		if err != nil {
			output.Error(err.Error())
			return
		}
		output.RawJSON(data)
		return
	}
	fmt.Fprintln(output.Out)
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		output.KeyValue(k, r.Header[k])
	}
	if len(keys) > 0 {
		fmt.Fprintln(output.Out)
	}
	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, []string{row.Status, row.Name, row.Detail})
	}
	output.Table([]string{"RESULT", "ASSERTION", "DETAIL"}, rows)
	pass, fail, skip := r.Counts()
	output.Hint(fmt.Sprintf("%d passed, %d failed, %d skipped", pass, fail, skip))
}

// reportFailure is the error a command returns after printing a report with
// failed rows: exit code 1, one line, the table already on screen.
func reportFailure(r *migrate.Report, what string) error {
	_, fail, _ := r.Counts()
	if fail == 0 {
		return nil
	}
	return cmdErr(fmt.Sprintf("%d assertion(s) failed for %s", fail, what))
}

// migrateConfirm asks before a step that changes the cluster, unless --yes
// was given. Without a terminal the prompt refuses and names the flag.
func migrateConfirm(yes bool, question string) error {
	if yes {
		return nil
	}
	ok, err := confirm(question)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cancelled")
	}
	return nil
}

// printNotes prints the resolution's notes and warnings once.
func printNotes(res *migrateResolution) {
	for _, n := range res.Env.Notes {
		migrateHint(n)
	}
	for _, n := range res.Notes {
		migrateHint(n)
	}
	for _, w := range res.Warnings {
		migrateWarn(w)
	}
}

// Progress lines. With -o json the document is the only thing on stdout,
// so hints, warnings and successes go to stderr — where a script's log
// keeps them and its parser does not see them.
func migrateHint(msg string) {
	if outputMode == "json" {
		fmt.Fprintln(output.Err, "  "+msg)
		return
	}
	output.Hint(msg)
}

func migrateWarn(msg string) {
	if outputMode == "json" {
		fmt.Fprintln(output.Err, "  warning: "+msg)
		return
	}
	output.Warn(msg)
}

func migrateSuccess(msg string) {
	if outputMode == "json" {
		fmt.Fprintln(output.Err, "  "+msg)
		return
	}
	output.Success(msg)
}
