package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// kates migrate mirror — the MirrorMaker 2 release on its own: the lower-
// level form of up's mirror step, cutover and rollback, and remove.

// migrateMirrorFlags are the flags of mirror deploy|status|cutover|
// rollback|remove.
type migrateMirrorFlags struct {
	From            string
	FromBootstrap   string
	SourceVersion   string
	SourceSecret    string
	SourceUser      string
	SourceMechanism string
	Policy          string
	Topics          string
	ReadOnlySource  bool
	Release         string
	Namespace       string
	TargetCluster   string
	Values          string
	DryRun          bool
	Wait            bool
	Yes             bool
	Name            string
	Timeout         int
}

func (f *migrateMirrorFlags) timeout() time.Duration {
	if f.Timeout <= 0 {
		return migrateDefaultTimeout * time.Second
	}
	return time.Duration(f.Timeout) * time.Second
}

var (
	migrateMirrorDeployFlags   migrateMirrorFlags
	migrateMirrorStatusFlags   migrateMirrorFlags
	migrateMirrorCutoverFlags  migrateMirrorFlags
	migrateMirrorRollbackFlags migrateMirrorFlags
	migrateMirrorRemoveFlags   migrateMirrorFlags

	migrateMirrorCmd = &cobra.Command{
		Use:   "mirror",
		Short: "The MirrorMaker 2 release on its own: deploy, status, cutover, rollback, remove",
	}

	migrateMirrorDeployCmd = &cobra.Command{
		Use:   "deploy --from <source name> | --from-bootstrap host:9092 --source-version <v>",
		Short: "Install MirrorMaker 2 from a source cluster to the target, with the era preset and generated values",
		Long: `Installs charts/mirror-maker2 in the target's namespace for one source:
--from names a source by its lab or release (resolved through the cluster,
like kates clusters list), --from-bootstrap gives a raw address with
--source-version and, for an authenticated listener, --source-secret,
--source-user and --source-mechanism. The values are written from the pair
(policy, minSourceVersion, preflight, the topic list, replication factors from
the target's broker count) on top of the era preset; --read-only-source keeps
every MirrorMaker write on the target, for a source you may only read;
--dry-run prints the helm command and the values file and stops.`,
		Example: `  kates migrate mirror deploy --from m282-430-src
  kates migrate mirror deploy --from-bootstrap old.example:9092 --source-version 3.9.1 --policy default --dry-run
  kates migrate mirror deploy --from-bootstrap old.example:9092 --source-version 3.9.1 --read-only-source`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateMirrorDeploy(cmd.Context(), &migrateMirrorDeployFlags)
		},
	}

	migrateMirrorStatusCmd = &cobra.Command{
		Use:   "status [--release <r>] [--wait]",
		Short: "CR readiness and every connector's state; --wait blocks until every connector runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateMirrorStatus(cmd.Context(), &migrateMirrorStatusFlags)
		},
	}

	migrateMirrorCutoverCmd = &cobra.Command{
		Use:   "cutover [--release <r>] [--yes] [--dry-run]",
		Short: "Apply values-cutover.yaml: source connector stopped, checkpoints running",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateMirrorCutover(cmd.Context(), &migrateMirrorCutoverFlags, false)
		},
	}

	migrateMirrorRollbackCmd = &cobra.Command{
		Use:   "rollback [--release <r>] [--yes] [--dry-run]",
		Short: "Reverse a cutover: both connectors running again",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateMirrorCutover(cmd.Context(), &migrateMirrorRollbackFlags, true)
		},
	}

	migrateMirrorRemoveCmd = &cobra.Command{
		Use:   "remove [--release <r>] [--yes]",
		Short: "helm uninstall the mirror and delete the kept CR",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateMirrorRemove(cmd.Context(), &migrateMirrorRemoveFlags)
		},
	}
)

func init() {
	d := migrateMirrorDeployCmd.Flags()
	d.StringVar(&migrateMirrorDeployFlags.From, "from", "", "Source cluster by lab or release name (kates migrate source deploy)")
	d.StringVar(&migrateMirrorDeployFlags.FromBootstrap, "from-bootstrap", "", "Source bootstrap address, host:port (with --source-version)")
	d.StringVar(&migrateMirrorDeployFlags.SourceVersion, "source-version", "", "Kafka version of the --from-bootstrap source")
	d.StringVar(&migrateMirrorDeployFlags.SourceSecret, "source-secret", "", "Secret in the mirror's namespace holding the source password under \"password\"")
	d.StringVar(&migrateMirrorDeployFlags.SourceUser, "source-user", "", "Username for the source listener")
	d.StringVar(&migrateMirrorDeployFlags.SourceMechanism, "source-mechanism", migrate.MechanismPlain, "SASL mechanism of the source: plain, scram-sha-256 or scram-sha-512")
	d.StringVar(&migrateMirrorDeployFlags.Policy, "policy", migrate.PolicyIdentity, "Replication policy: identity or default")
	d.StringVar(&migrateMirrorDeployFlags.Topics, "topics", migrate.DefaultTopic, "Topics to mirror, comma-separated")
	d.BoolVar(&migrateMirrorDeployFlags.ReadOnlySource, "read-only-source", false,
		"Write nothing to the source: offset-syncs go to the target (KIP-716), so the source principal needs Read and Describe only.\n"+
			"The lab's own legacy-kafka source does not need this; it is for a --from-bootstrap cluster you may only read.")
	d.StringVar(&migrateMirrorDeployFlags.Release, "release", "", "Helm release of the mirror (default: mm2-<lab>)")
	d.StringVar(&migrateMirrorDeployFlags.Namespace, "namespace", migrate.DefaultTargetNamespace, "Namespace of the mirror (the target's)")
	d.StringVar(&migrateMirrorDeployFlags.TargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Target Kafka cluster")
	d.StringVar(&migrateMirrorDeployFlags.Values, "values", "", "Extra values file layered on the generated values")
	d.StringVar(&migrateMirrorDeployFlags.Name, "name", "", "Lab name (default: the source's, or m<from>-<to>)")
	d.BoolVar(&migrateMirrorDeployFlags.DryRun, "dry-run", false, "Print the helm command and the values file, install nothing")
	d.BoolVarP(&migrateMirrorDeployFlags.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	d.IntVar(&migrateMirrorDeployFlags.Timeout, "timeout", migrateDefaultTimeout, "Readiness budget in seconds")

	for _, c := range []struct {
		cmd *cobra.Command
		f   *migrateMirrorFlags
		yes bool
	}{
		{migrateMirrorStatusCmd, &migrateMirrorStatusFlags, false},
		{migrateMirrorCutoverCmd, &migrateMirrorCutoverFlags, true},
		{migrateMirrorRollbackCmd, &migrateMirrorRollbackFlags, true},
		{migrateMirrorRemoveCmd, &migrateMirrorRemoveFlags, true},
	} {
		c.cmd.Flags().StringVar(&c.f.Release, "release", "", "Helm release of the mirror (default: the single lab's mm2-<lab>)")
		c.cmd.Flags().StringVar(&c.f.Namespace, "namespace", migrate.DefaultTargetNamespace, "Namespace of the mirror")
		c.cmd.Flags().StringVar(&c.f.Name, "name", "", "Lab name whose mirror to act on")
		c.cmd.Flags().IntVar(&c.f.Timeout, "timeout", migrateDefaultTimeout, "Wait budget in seconds")
		if c.yes {
			c.cmd.Flags().BoolVarP(&c.f.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
		}
	}
	migrateMirrorStatusCmd.Flags().BoolVar(&migrateMirrorStatusFlags.Wait, "wait", false, "Block until the CR is Ready and every connector RUNNING")
	migrateMirrorCutoverCmd.Flags().BoolVar(&migrateMirrorCutoverFlags.DryRun, "dry-run", false, "Print the helm command and stop")
	migrateMirrorRollbackCmd.Flags().BoolVar(&migrateMirrorRollbackFlags.DryRun, "dry-run", false, "Print the helm command and stop")

	migrateMirrorCmd.AddCommand(migrateMirrorDeployCmd, migrateMirrorStatusCmd, migrateMirrorCutoverCmd, migrateMirrorRollbackCmd, migrateMirrorRemoveCmd)
	migrateCmd.AddCommand(migrateMirrorCmd)
}

// resolveMirrorRelease finds the mirror a command acts on: --release, else
// the lab's (--name, or the single lab).
func resolveMirrorRelease(ctx context.Context, f *migrateMirrorFlags) (release, namespace, cr string, err error) {
	if f.Release != "" {
		return f.Release, f.Namespace, f.Release + "-mirror-maker2", nil
	}
	d, err := discoverLab(ctx, f.Name)
	if err != nil {
		return "", "", "", err
	}
	if d.MirrorRelease == "" {
		return "", "", "", fmt.Errorf("lab %s has no mirror", d.Name)
	}
	return d.MirrorRelease, d.MirrorNamespace, d.MirrorCR, nil
}

// runMigrateMirrorDeploy is `kates migrate mirror deploy`.
func runMigrateMirrorDeploy(ctx context.Context, f *migrateMirrorFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if (f.From == "") == (f.FromBootstrap == "") {
		return errors.New("pass exactly one of --from <source name> or --from-bootstrap host:port")
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.Namespace, false)
	if err != nil {
		return err
	}
	opts := migrate.Options{
		Name: f.Name, To: env.Target.Version, SourceProvider: kafkaversion.ProviderLegacy,
		TargetIsPrimary: true, TargetCluster: env.Target.Cluster, TargetNamespace: env.Target.Namespace,
		Policy: f.Policy, Topics: splitTopics(f.Topics), StrimziVersion: env.StrimziVersion, Scope: env.Scope,
		ReadOnlySource: f.ReadOnlySource,
	}
	var bootstrap, sourceUser, sourceSecret, mechanism string
	var namespace string
	if f.From != "" {
		src, err := findLegacySource(ctx, f.From, "")
		if err != nil {
			return err
		}
		if src.Version == (kafkaversion.Version{}) {
			return fmt.Errorf("source %s states no Kafka version (kates.io/kafka-version on its bootstrap Service)", src.Release)
		}
		opts.From = src.Version
		if opts.Name == "" {
			opts.Name = src.Lab
		}
		bootstrap, namespace = src.Bootstrap, src.Namespace
		if strings.HasSuffix(bootstrap, ":9094") {
			opts.SASL = true
			sourceUser, mechanism = migrate.LegacySASLUser, migrate.MechanismPlain
			sourceSecret = src.Release + "-" + migrate.LegacySASLUser
		}
		if f.SourceSecret != "" {
			sourceSecret = f.SourceSecret
		}
		if f.SourceUser != "" {
			sourceUser = f.SourceUser
		}
	} else {
		if f.SourceVersion == "" {
			return errors.New("--from-bootstrap needs --source-version")
		}
		v, err := kafkaversion.Parse(f.SourceVersion)
		if err != nil {
			return fmt.Errorf("--source-version: %w", err)
		}
		opts.From, bootstrap = v, f.FromBootstrap
		if f.SourceSecret != "" {
			if f.SourceUser == "" {
				return errors.New("--source-secret needs --source-user")
			}
			opts.SASL, sourceUser, sourceSecret, mechanism = true, f.SourceUser, f.SourceSecret, f.SourceMechanism
		}
	}
	if opts.SASL {
		opts.SourcePassword = "external" // the Secret already exists; the lab never writes it
	}
	l, err := migrate.New(opts)
	if err != nil {
		return err
	}
	l.SourcePassword = ""
	l.SourceBootstrap = bootstrap
	if namespace != "" {
		l.SourceNamespace = namespace
	}
	if sourceUser != "" {
		l.SourceUser, l.SourceSecret = sourceUser, sourceSecret
	}
	if f.Release != "" {
		l.MirrorRelease, l.MirrorCR, l.GroupID = f.Release, f.Release+"-mirror-maker2", f.Release
	}
	l.MirrorNamespace = f.Namespace

	inputs := l.MirrorInputs()
	inputs.TargetBrokerCount = env.Target.Brokers
	inputs.OperatorNamespace = env.OperatorNamespace
	if mechanism != "" {
		inputs.SourceMechanism = mechanism
	}
	values, err := migrate.MirrorValues(l, inputs)
	if err != nil {
		return err
	}
	path := mirrorValuesPath(l.Name)
	argv := helmMirrorArgs(l, env.IsKind, path, f.Values)
	if f.DryRun {
		output.SubHeader("helm " + strings.Join(argv, " "))
		migrateHint("# " + path)
		fmt.Fprint(output.Out, values)
		return nil
	}
	migrateHint(fmt.Sprintf("mirror %s in %s: %s → %s (%s policy)", l.MirrorRelease, l.MirrorNamespace, l.SourceBootstrap, l.TargetCluster, l.Policy))
	migrateHint("offset-syncs: " + offsetSyncsLine(l.ReadOnlySource))
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Install %s in %s?", l.MirrorRelease, l.MirrorNamespace)); err != nil {
		return err
	}
	res := &migrateResolution{Env: env, Lab: l, Opts: l.Options, SourceOperator: "external"}
	pf := &migratePairFlags{Timeout: f.Timeout, ValuesMirror: f.Values}
	r := newLabRun(res, pf)
	runErr := migrate.RunPhases(ctx, traced([]migrate.Phase{{Name: "mirror", Run: r.phaseMirror}}), r.state)
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), "the mirror "+l.MirrorRelease); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess(fmt.Sprintf("mirror %s running in %s", l.MirrorRelease, l.MirrorNamespace))
	return nil
}

// runMigrateMirrorStatus is `kates migrate mirror status`.
func runMigrateMirrorStatus(ctx context.Context, f *migrateMirrorFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	release, namespace, cr, err := resolveMirrorRelease(ctx, f)
	if err != nil {
		return err
	}
	var status migrate.MirrorStatus
	if f.Wait {
		if _, err := defaultRunner.Run(ctx, "kubectl", "-n", namespace, "wait", "kafkamirrormaker2/"+cr, "--for=condition=Ready", "--timeout="+formatSeconds(f.timeout())); err != nil {
			return fmt.Errorf("%s did not become Ready: %w", cr, err)
		}
		status, err = waitConnectorsRunning(ctx, namespace, cr, f.timeout())
		if err != nil {
			return err
		}
	} else {
		status, err = readMirrorStatus(ctx, namespace, cr)
		if err != nil {
			return err
		}
	}
	if outputMode == "json" {
		output.JSON(map[string]any{
			"release": release, "namespace": namespace, "cr": cr,
			"ready": status.Ready, "reason": status.Reason, "message": status.Message,
			"connectors": status.Connectors, "replicas": status.Replicas,
			"generation": status.Generation, "observedGeneration": status.ObservedGeneration,
		})
		return nil
	}
	output.KeyValue("mirror", release+" in "+namespace+" ("+cr+")")
	ready := "not Ready"
	if status.Ready {
		ready = "Ready"
	}
	if status.Reason != "" {
		ready += " — " + status.Reason + ": " + status.Message
	}
	output.KeyValue("state", fmt.Sprintf("%s, %d replica(s), generation %d/%d observed", ready, status.Replicas, status.ObservedGeneration, status.Generation))
	rows := make([][]string, 0, len(status.Connectors))
	for _, c := range status.Connectors {
		rows = append(rows, []string{c.Name, c.State, strings.Join(c.Tasks, ",")})
	}
	output.Table([]string{"CONNECTOR", "STATE", "TASKS"}, rows)
	if !status.AllRunning() && len(status.Connectors) > 0 {
		migrateWarn("not every connector is RUNNING")
	}
	return nil
}

// runMigrateMirrorCutover is `kates migrate mirror cutover` and, reversed,
// `rollback`.
func runMigrateMirrorCutover(ctx context.Context, f *migrateMirrorFlags, rollback bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	release, namespace, cr, err := resolveMirrorRelease(ctx, f)
	if err != nil {
		return err
	}
	var argv []string
	if rollback {
		path := rollbackValuesPath(release)
		if !f.DryRun {
			if err := writeLabFile(path, rollbackValues); err != nil {
				return err
			}
		}
		argv = helmRollbackArgs(release, namespace, path, f.timeout())
	} else {
		argv = helmCutoverArgs(release, namespace, f.timeout())
	}
	if f.DryRun {
		migrateHint("helm " + strings.Join(argv, " "))
		return nil
	}
	question := fmt.Sprintf("Cut over %s in %s: stop its source connector (the checkpoints keep running)?", release, namespace)
	if rollback {
		migrateWarn("anything produced to the target since the cutover is not replicated backwards — a rollback after producers have moved is a data merge, not a switch")
		question = fmt.Sprintf("Roll back the cutover of %s in %s: run its source connector again?", release, namespace)
	}
	if err := migrateConfirm(f.Yes, question); err != nil {
		return err
	}
	if err := ensureMirrorChartDeps(ctx); err != nil {
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", argv...); err != nil {
		return fmt.Errorf("helm upgrade: %w", err)
	}
	want := migrate.StateStopped
	if rollback {
		want = migrate.StateRunning
	}
	status, err := waitConnectorStates(ctx, namespace, cr, want, migrate.StateRunning, f.timeout())
	if err != nil {
		return err
	}
	migrateSuccess(status.Summary())
	return nil
}

// runMigrateMirrorRemove is `kates migrate mirror remove`.
func runMigrateMirrorRemove(ctx context.Context, f *migrateMirrorFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	release, namespace, cr, err := resolveMirrorRelease(ctx, f)
	if err != nil {
		return err
	}
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Remove mirror %s in %s (and its kept CR %s)?", release, namespace, cr)); err != nil {
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", "uninstall", release, "-n", namespace); err != nil && !isNotFound(err) {
		return fmt.Errorf("helm uninstall: %w", err)
	}
	if _, err := defaultRunner.Run(ctx, "kubectl", "-n", namespace, "delete", "kafkamirrormaker2", cr, "--ignore-not-found", "--timeout=60s"); err != nil {
		return fmt.Errorf("delete the kept CR: %w", err)
	}
	migrateSuccess(fmt.Sprintf("mirror %s removed from %s", release, namespace))
	return nil
}
