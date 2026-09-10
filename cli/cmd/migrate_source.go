package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// kates migrate source — the source cluster on its own: the building block
// up is composed from. Legacy (charts/legacy-kafka) only in this version; a
// Strimzi source lands with kates clusters add.

// migrateSourceFlags are the flags of source deploy|status|remove.
type migrateSourceFlags struct {
	Version        string
	Provider       string
	StrimziVersion string
	Namespace      string
	Release        string
	Image          string
	Topics         string
	SASL           bool
	Values         string
	Name           string
	SkipBuild      bool
	Yes            bool
	Timeout        int
	Registry       string
	TargetCluster  string
	TargetNS       string
}

var (
	migrateSourceDeployFlags migrateSourceFlags
	migrateSourceStatusFlags migrateSourceFlags
	migrateSourceRemoveFlags migrateSourceFlags

	migrateSourceCmd = &cobra.Command{
		Use:   "source",
		Short: "The old Kafka on its own: deploy, status, remove",
	}

	migrateSourceDeployCmd = &cobra.Command{
		Use:   "deploy --version <v>",
		Short: "Deploy a legacy Kafka source (charts/legacy-kafka) and wait for it to serve",
		Long: `Deploys the source half of a lab: the namespace (labelled kates.io/lab), the
generated values, the legacy-kafka release with the era chosen from the
version (ZooKeeper below 3.3.0, the built KRaft image below 3.7.0, the
official image from there), its Helm test, and — with --sasl — the
credential Secret the mirror will read. The image is built and loaded
first when the version needs one (--skip-build assumes it is present).`,
		Example: `  kates migrate source deploy --version 2.8.2
  kates migrate source deploy --version 3.9.1 --namespace kafka-old --release old --topic kates.orders,kates.payments`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateSourceDeploy(cmd.Context(), &migrateSourceDeployFlags)
		},
	}

	migrateSourceStatusCmd = &cobra.Command{
		Use:   "status [--name <lab> | --namespace <ns>]",
		Short: "Broker (and ZooKeeper) readiness and bootstrap of a legacy source",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateSourceStatus(cmd.Context(), &migrateSourceStatusFlags)
		},
	}

	migrateSourceRemoveCmd = &cobra.Command{
		Use:   "remove [--name <lab> | --namespace <ns>] [--yes]",
		Short: "Remove a legacy source: its release, its namespace when the lab created it, its credential",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateSourceRemove(cmd.Context(), &migrateSourceRemoveFlags)
		},
	}
)

func init() {
	d := migrateSourceDeployCmd.Flags()
	d.StringVar(&migrateSourceDeployFlags.Version, "version", "", "Kafka version of the source, x.y.z, at least "+kafkaversion.MinMirrorSource.String()+" (required)")
	d.StringVar(&migrateSourceDeployFlags.Provider, "provider", "auto", "Who runs the source: auto, strimzi or legacy")
	d.StringVar(&migrateSourceDeployFlags.StrimziVersion, "strimzi-version", "", "Operator version of a Strimzi source (namespace scope)")
	d.StringVar(&migrateSourceDeployFlags.Namespace, "namespace", "", "Namespace of the source (default: kafka-<lab>-src)")
	d.StringVar(&migrateSourceDeployFlags.Release, "release", "", "Helm release of the source (default: <lab>-src)")
	d.StringVar(&migrateSourceDeployFlags.Image, "image", "", "Broker image override")
	d.StringVar(&migrateSourceDeployFlags.Topics, "topic", migrate.DefaultTopic, "Topics to pre-create, comma-separated")
	d.BoolVar(&migrateSourceDeployFlags.SASL, "sasl", false, "Enable the SASL/PLAIN listener and write its credential for the mirror")
	d.StringVar(&migrateSourceDeployFlags.Values, "values", "", "Extra values file layered on the generated values")
	d.StringVar(&migrateSourceDeployFlags.Name, "name", "", "Lab name the source belongs to (default: m<version>-<target>)")
	d.BoolVar(&migrateSourceDeployFlags.SkipBuild, "skip-build", false, "Do not build/load the source image")
	d.BoolVarP(&migrateSourceDeployFlags.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	d.IntVar(&migrateSourceDeployFlags.Timeout, "timeout", migrateDefaultTimeout, "Install and test budget in seconds")
	d.StringVar(&migrateSourceDeployFlags.Registry, "registry", kafkaversion.DefaultLegacyRegistry, "Registry of the built legacy images")
	d.StringVar(&migrateSourceDeployFlags.TargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Name of the target Kafka the source will be mirrored to")
	d.StringVar(&migrateSourceDeployFlags.TargetNS, "target-namespace", migrate.DefaultTargetNamespace, "Namespace of the target Kafka")

	for _, c := range []struct {
		cmd *cobra.Command
		f   *migrateSourceFlags
	}{{migrateSourceStatusCmd, &migrateSourceStatusFlags}, {migrateSourceRemoveCmd, &migrateSourceRemoveFlags}} {
		c.cmd.Flags().StringVar(&c.f.Name, "name", "", "Lab name (default: the single lab on the cluster)")
		c.cmd.Flags().StringVar(&c.f.Namespace, "namespace", "", "Namespace of a source deployed without a lab name")
		c.cmd.Flags().StringVar(&c.f.TargetNS, "target-namespace", migrate.DefaultTargetNamespace, "Namespace where the source's credential Secret lives")
		c.cmd.Flags().IntVar(&c.f.Timeout, "timeout", migrateDefaultTimeout, "Wait budget in seconds")
	}
	migrateSourceRemoveCmd.Flags().BoolVarP(&migrateSourceRemoveFlags.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")

	migrateSourceCmd.AddCommand(migrateSourceDeployCmd, migrateSourceStatusCmd, migrateSourceRemoveCmd)
	migrateCmd.AddCommand(migrateSourceCmd)
}

// applySourceOverrides renames the source's namespace, release and image
// consistently after the lab was derived.
func applySourceOverrides(l *migrate.Lab, namespace, release, image string) {
	if release != "" {
		l.SourceRelease, l.SourceCluster = release, release
	}
	if namespace != "" {
		l.SourceNamespace = namespace
	}
	if image != "" {
		l.SourceImage = image
	}
	if l.SourceProvider == kafkaversion.ProviderLegacy {
		port := 9092
		if l.SASL {
			port = 9094
			l.SourceSecret = l.SourceRelease + "-" + migrate.LegacySASLUser
		}
		l.SourceBootstrap = fmt.Sprintf("%s-legacy-kafka-bootstrap.%s.svc.%s:%d", l.SourceRelease, l.SourceNamespace, migrate.ClusterDomain, port)
	}
}

// runMigrateSourceDeploy is `kates migrate source deploy`.
func runMigrateSourceDeploy(ctx context.Context, f *migrateSourceFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if f.Version == "" {
		return errors.New("--version is required: the source's Kafka version, x.y.z")
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.TargetNS, false)
	if err != nil {
		return err
	}
	pf := &migratePairFlags{
		From: []string{f.Version}, Name: f.Name, SourceProvider: f.Provider, SourceStrimziVersion: f.StrimziVersion,
		Policy: migrate.PolicyIdentity, Topics: f.Topics, Messages: migrate.DefaultMessages, SASL: f.SASL,
		ValuesSource: f.Values, Yes: f.Yes, TargetCluster: f.TargetCluster, TargetNamespace: f.TargetNS,
		Registry: f.Registry, Timeout: f.Timeout, SkipBuild: f.SkipBuild,
	}
	res, err := resolveMigratePair(env, pf)
	if err != nil {
		return err
	}
	if err := refuseUnimplemented(res); err != nil {
		return err
	}
	applySourceOverrides(res.Lab, f.Namespace, f.Release, f.Image)
	printNotes(res)
	if outputMode != "json" {
		output.KeyValue("source", res.Lab.Describe())
		output.KeyValue("namespace", res.Lab.SourceNamespace)
		output.KeyValue("release", res.Lab.SourceRelease+"  "+migrate.SourceChartPath)
		output.KeyValue("image", res.Lab.SourceImage)
	}
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Deploy Kafka %s as %s in %s?", res.Lab.From, res.Lab.SourceRelease, res.Lab.SourceNamespace)); err != nil {
		return err
	}
	r := newLabRun(res, pf)
	phases := []migrate.Phase{
		{Name: "image", Run: r.phaseImage},
		{Name: "source", Run: r.phaseSource},
		{Name: "credential", Run: r.phaseCredential},
	}
	runErr := migrate.RunPhases(ctx, traced(phases), r.state)
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), "the source "+r.lab.SourceRelease); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess(fmt.Sprintf("source %s serving at %s", r.lab.SourceRelease, r.lab.SourceBootstrap))
	migrateHint(fmt.Sprintf("next: kates migrate mirror deploy --from %s", r.lab.SourceRelease))
	return nil
}

// legacySource is a legacy-kafka release found on the cluster.
type legacySource struct {
	Release, Namespace string
	Lab                string
	Version            kafkaversion.Version
	Mode               string
	Bootstrap          string
	Image              string
}

// findLegacySources lists the legacy-kafka releases on the cluster through
// their bootstrap Services (which carry the bootstrap address and the
// version as annotations) and their broker StatefulSets (the image).
func findLegacySources(ctx context.Context, namespace string) ([]legacySource, error) {
	args := []string{"get", "service", "-l", "app.kubernetes.io/name=legacy-kafka,app.kubernetes.io/component=broker", "-o", "json"}
	if namespace != "" {
		args = append([]string{"-n", namespace}, args...)
	} else {
		args = append(args, "-A")
	}
	out, err := defaultRunner.Run(ctx, "kubectl", args...)
	if err != nil {
		return nil, fmt.Errorf("list legacy sources: %w", err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Namespace   string            `json:"namespace"`
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return nil, fmt.Errorf("parse legacy sources: %w", err)
		}
	}
	var sources []legacySource
	for _, it := range list.Items {
		bootstrap := it.Metadata.Annotations["kates.io/bootstrap-address"]
		if bootstrap == "" {
			continue // the headless Service carries no annotation
		}
		s := legacySource{
			Release:   it.Metadata.Labels["app.kubernetes.io/instance"],
			Namespace: it.Metadata.Namespace,
			Lab:       it.Metadata.Labels[migrate.LabelLab],
			Mode:      it.Metadata.Labels["kates.io/kafka-mode"],
			Bootstrap: bootstrap,
		}
		if v, err := kafkaversion.Parse(it.Metadata.Annotations["kates.io/kafka-version"]); err == nil {
			s.Version = v
		}
		if img, err := defaultRunner.Run(ctx, "kubectl", "-n", s.Namespace, "get", "statefulset", s.Release+"-legacy-kafka", "-o", "jsonpath={.spec.template.spec.containers[0].image}"); err == nil {
			s.Image = strings.TrimSpace(img)
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// findLegacySource resolves a source by lab name or release name, or the
// single one in a namespace.
func findLegacySource(ctx context.Context, name, namespace string) (*legacySource, error) {
	sources, err := findLegacySources(ctx, namespace)
	if err != nil {
		return nil, err
	}
	var matches []legacySource
	for _, s := range sources {
		if name == "" || s.Lab == name || s.Release == name || s.Release == name+"-src" {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		if name != "" {
			return nil, fmt.Errorf("no legacy source named %q (by lab or release) — kates migrate source deploy creates one", name)
		}
		return nil, errors.New("no legacy source found — kates migrate source deploy creates one")
	default:
		var names []string
		for _, m := range matches {
			names = append(names, m.Release+" in "+m.Namespace)
		}
		return nil, fmt.Errorf("%d legacy sources match (%s) — pass --name or --namespace", len(matches), strings.Join(names, ", "))
	}
}

// runMigrateSourceStatus is `kates migrate source status`.
func runMigrateSourceStatus(ctx context.Context, f *migrateSourceFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s, err := findLegacySource(ctx, f.Name, f.Namespace)
	if err != nil {
		return err
	}
	type sts struct {
		Name  string `json:"name"`
		Ready string `json:"ready"`
	}
	var pods []sts
	for _, suffix := range []string{"-legacy-kafka", "-legacy-kafka-zookeeper"} {
		out, err := defaultRunner.Run(ctx, "kubectl", "-n", s.Namespace, "get", "statefulset", s.Release+suffix, "--ignore-not-found", "-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}")
		if err != nil || strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "/" {
			continue
		}
		pods = append(pods, sts{Name: s.Release + suffix, Ready: strings.TrimSpace(out)})
	}
	if outputMode == "json" {
		output.JSON(map[string]any{
			"release": s.Release, "namespace": s.Namespace, "lab": s.Lab, "version": s.Version.String(),
			"mode": s.Mode, "bootstrap": s.Bootstrap, "image": s.Image, "statefulsets": pods,
		})
		return nil
	}
	output.Header("Legacy source " + s.Release)
	output.KeyValue("namespace", s.Namespace)
	if s.Lab != "" {
		output.KeyValue("lab", s.Lab)
	}
	output.KeyValue("version", fmt.Sprintf("%s (%s)", s.Version, s.Mode))
	output.KeyValue("image", s.Image)
	output.KeyValue("bootstrap", s.Bootstrap)
	for _, p := range pods {
		output.KeyValue(p.Name, p.Ready+" ready")
	}
	return nil
}

// runMigrateSourceRemove is `kates migrate source remove`.
func runMigrateSourceRemove(ctx context.Context, f *migrateSourceFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s, err := findLegacySource(ctx, f.Name, f.Namespace)
	if err != nil {
		return err
	}
	owned := s.Lab != "" && namespaceHasLabel(ctx, s.Namespace, migrate.LabelLab, s.Lab)
	migrateHint("release " + s.Release + " in " + s.Namespace)
	if owned {
		migrateHint("namespace " + s.Namespace + " (created by the lab — removed)")
	} else {
		migrateHint("namespace " + s.Namespace + " (left in place)")
	}
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Remove source %s in %s?", s.Release, s.Namespace)); err != nil {
		return err
	}
	d := &labDiscovery{
		Name: s.Lab, SourceRelease: s.Release, SourceNamespace: s.Namespace, SourceNamespaceOwned: owned,
		Sources: []*labSource{{
			Release: s.Release, Namespace: s.Namespace, NamespaceOwned: owned,
			Provider: kafkaversion.ProviderLegacy, Version: s.Version, Bootstrap: s.Bootstrap,
		}},
	}
	if err := labDown(ctx, d, time.Duration(f.Timeout)*time.Second); err != nil {
		return err
	}
	if s.Lab != "" {
		if _, err := defaultRunner.Run(ctx, "kubectl", "-n", f.TargetNS, "delete", "secret", "-l", migrate.LabelLab+"="+s.Lab+","+migrate.LabelRole+"="+migrate.RoleMirror, "--ignore-not-found"); err != nil {
			migrateWarn(err.Error())
		}
	}
	migrateSuccess("source " + s.Release + " removed")
	return nil
}
