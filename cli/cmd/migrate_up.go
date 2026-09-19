package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

var (
	migrateUpFlags migratePairFlags

	migrateUpCmd = &cobra.Command{
		Use:   "up --from <version> [--from <version>…] [--to <version>]",
		Short: "Create the migration lab: the source, the mirror, ready to verify",
		Long: `Stands up the lab plan describes: the prerequisites (cluster, Strimzi CRDs,
target Kafka Ready, target credentials), the built image when a source needs
one, each source's namespace and release, each source credential, the
MirrorMaker 2 release with the era preset and the generated values, then
waits for the CR to be Ready and every connector RUNNING. Every step is a
row of the report; the exit code is 1 on any failed row.

Repeat --from to mirror several sources into the one target: each gets its
own alias (src282, src391), namespace, release, credential, corpus topic and
mirror, and its own rows in the report.

Every release carries kates.io/lab=<name>, which is how status, verify,
cutover and down find the lab again.`,
		Example: `  kates migrate up --from 2.8.2
  kates migrate up --from 3.9.1 --policy default --name m391-430-default --yes
  kates migrate up --from 2.8.2 --from 3.9.1 --yes
  kates migrate up --from 2.8.2 --sasl --topics kates.orders,kates.payments`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateUp(cmd.Context(), &migrateUpFlags)
		},
	}
)

func init() {
	addMigratePairFlags(migrateUpCmd, &migrateUpFlags)
	migrateUpCmd.Flags().BoolVar(&migrateUpFlags.SkipBuild, "skip-build", false, "Do not build/load the source image (assume it is present)")
	migrateCmd.AddCommand(migrateUpCmd)
}

// labRun is the state one lab's commands share: the lab, its environment,
// the report the phases write, the client pods once they exist, and the
// flags that shape the waits.
type labRun struct {
	lab   *migrate.Lab
	env   *migrateEnv
	res   *migrateResolution
	state *migrate.State

	timeout      time.Duration
	valuesSource string
	valuesMirror string
	skipBuild    bool
	keep         bool

	// createdNamespaces holds the source namespaces up created (and so down
	// may delete), by name.
	createdNamespaces map[string]bool
	clients           *labClients
}

// createdNamespace says up created the given source namespace.
func (r *labRun) createdNamespace(namespace string) bool {
	return r.createdNamespaces[namespace]
}

// newLabRun opens a run for a resolved lab with a fresh report.
func newLabRun(res *migrateResolution, f *migratePairFlags) *labRun {
	r := &labRun{
		lab: res.Lab, env: res.Env, res: res,
		timeout: f.timeout(), valuesSource: f.ValuesSource, valuesMirror: f.ValuesMirror,
		skipBuild: f.SkipBuild, keep: f.Keep,
	}
	r.state = migrate.NewState(res.Lab, labHeader(res))
	return r
}

// labHeader is the report header: the pair, the providers, the operators,
// the policy and — when it is on — the read-only-source posture, because
// where the offset-syncs topic lives is the difference between a report
// about a cluster the lab owns and one about a cluster it may only read.
func labHeader(res *migrateResolution) map[string]string {
	l, env := res.Lab, res.Env
	var froms, sources []string
	for _, s := range l.Sources() {
		froms = append(froms, s.From.String())
		line := fmt.Sprintf("%s in %s", s.Provider, s.Namespace)
		if s.Provider == kafkaversion.ProviderLegacy {
			line = fmt.Sprintf("legacy (%s, %s) in %s", s.Mode, s.Image, s.Namespace)
		}
		if l.FanIn() {
			line = s.Alias + ": " + line
		}
		sources = append(sources, line)
	}
	target := fmt.Sprintf("%s in %s (the platform's primary)", l.TargetCluster, l.TargetNamespace)
	if !l.TargetIsPrimary {
		target = fmt.Sprintf("%s in %s (additional)", l.TargetCluster, l.TargetNamespace)
	}
	policy := l.Policy + " — topic names preserved"
	if l.Policy == migrate.PolicyDefault {
		policy = fmt.Sprintf("%s — %s lands as %s", l.Policy, l.Topics[0], l.ReplicatedTopic(l.Topics[0]))
	}
	header := map[string]string{
		"lab":      l.Name,
		"pair":     fmt.Sprintf("Kafka %s → %s", strings.Join(froms, " + "), l.To),
		"source":   strings.Join(sources, "; "),
		"target":   target,
		"operator": env.operatorLine(),
		"policy":   policy,
		"corpus":   fmt.Sprintf("%d records → %s", l.Messages, strings.Join(l.Topics, ", ")),
		"client":   env.clientImage(),
	}
	header["offset-syncs"] = offsetSyncsLine(l.ReadOnlySource)
	return header
}

// offsetSyncsLine states where MirrorMaker keeps its offset-syncs topic and
// what that asks of the source principal — the one sentence that says
// whether this release writes to the source at all.
func offsetSyncsLine(readOnly bool) string {
	if readOnly {
		return "target — read-only source: nothing is written to it, its principal needs Read + Describe only (KIP-716)"
	}
	return "source — Kafka's default: the source principal also needs Create + Write + Describe on mm2-offset-syncs.* (--read-only-source moves it to the target)"
}

// report is the run's report.
func (r *labRun) report() *migrate.Report { return r.state.Report }

// traced wraps phases so each announces itself and prints the rows it
// recorded as soon as it returns — the scripts' phase banner and record
// lines, so a long wait in a CI log shows where the run stands. The table
// at the end is the summary; these are the progress.
func traced(phases []migrate.Phase) []migrate.Phase {
	out := make([]migrate.Phase, len(phases))
	for i, p := range phases {
		p := p
		out[i] = migrate.Phase{Name: p.Name, Optional: p.Optional, Run: func(ctx context.Context, s *migrate.State) error {
			migrateHint("── " + p.Name)
			seen := len(s.Report.Rows)
			err := p.Run(ctx, s)
			for _, row := range s.Report.Rows[seen:] {
				printReportRow(row)
			}
			return err
		}}
	}
	return out
}

// printReportRow prints one assertion as it lands.
func printReportRow(row migrate.Row) {
	line := row.Name + " — " + row.Detail
	switch row.Status {
	case migrate.StatusPass:
		migrateSuccess(line)
	case migrate.StatusFail:
		output.Error(line)
	default:
		migrateWarn(line)
	}
}

// upPhases are the phases of `up`, in the scripts' order.
func (r *labRun) upPhases() []migrate.Phase {
	return []migrate.Phase{
		{Name: "prerequisites", Run: r.phasePrerequisites},
		{Name: "image", Run: r.phaseImage},
		{Name: "source", Run: r.phaseSource},
		{Name: "credential", Run: r.phaseCredential},
		{Name: "mirror", Run: r.phaseMirror},
	}
}

// phasePrerequisites is the scripts' phase 1: cluster reachable, the
// KafkaMirrorMaker2 CRD, the target Kafka Ready, the target credential.
func (r *labRun) phasePrerequisites(ctx context.Context, s *migrate.State) error {
	rep := s.Report
	l := r.lab
	if _, err := defaultRunner.Run(ctx, "kubectl", "cluster-info"); err != nil {
		rep.Fail(migrate.RowClusterReachable, "no reachable Kubernetes cluster")
		return errors.New("no reachable Kubernetes cluster. Run 'make cluster' first")
	}
	current, _ := defaultRunner.Run(ctx, "kubectl", "config", "current-context")
	rep.Pass(migrate.RowClusterReachable, strings.TrimSpace(current))

	if _, err := defaultRunner.Run(ctx, "kubectl", "get", "crd", migrateCRDName, "-o", "name"); err != nil {
		rep.Fail(migrate.RowStrimziCRDs, migrateCRDName+" not installed")
		return errors.New("the KafkaMirrorMaker2 CRD is not installed. Run: kates deploy (or make deploy-strimzi)")
	}
	rep.Pass(migrate.RowStrimziCRDs, migrateCRDName+" present")

	if _, err := defaultRunner.Run(ctx, "kubectl", "-n", l.TargetNamespace, "wait", "kafka/"+l.TargetCluster,
		"--for=condition=Ready", "--timeout="+formatSeconds(migrateTargetReadyTimeout)); err != nil {
		rep.Fail(migrate.RowTargetKafkaReady, fmt.Sprintf("%s in %s is not Ready", l.TargetCluster, l.TargetNamespace))
		return fmt.Errorf("target cluster %s in %s is not Ready. Run: kates deploy (or make deploy-kafka)", l.TargetCluster, l.TargetNamespace)
	}
	rep.Pass(migrate.RowTargetKafkaReady, fmt.Sprintf("%s in %s", l.TargetCluster, l.TargetNamespace))

	name, err := defaultRunner.Run(ctx, "kubectl", "-n", l.TargetNamespace, "get", "secret", migrate.TargetUser, "--ignore-not-found", "-o", "jsonpath={.metadata.name}")
	if err != nil || strings.TrimSpace(name) == "" {
		rep.Fail(migrate.RowTargetCredentials, fmt.Sprintf("secret %s not found in %s", migrate.TargetUser, l.TargetNamespace))
		return fmt.Errorf("Secret %s not found in %s. The kafka-cluster chart provisions the KafkaUser %q", migrate.TargetUser, l.TargetNamespace, migrate.TargetUser)
	}
	rep.Pass(migrate.RowTargetCredentials, "secret "+migrate.TargetUser)
	return r.refuseOverlappingLab(ctx)
}

// refuseOverlappingLab refuses a second lab against the same target whose
// topic set overlaps this one's: under the identity policy two mirrors
// writing one topic name is data corruption, not a test.
func (r *labRun) refuseOverlappingLab(ctx context.Context) error {
	l := r.lab
	out, err := defaultRunner.Run(ctx, "kubectl", "-n", l.MirrorNamespace, "get", "kafkamirrormaker2", "-l", migrate.LabelLab, "-o", "json")
	if err != nil {
		return nil // best effort: the mirror's own install still fails on a real collision
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Mirrors []struct {
					TopicsPattern string `json:"topicsPattern"`
				} `json:"mirrors"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil
	}
	mine := map[string]bool{}
	for _, t := range l.Topics {
		mine[t] = true
	}
	for _, item := range list.Items {
		other := item.Metadata.Labels[migrate.LabelLab]
		if other == "" || other == l.Name {
			continue
		}
		for _, m := range item.Spec.Mirrors {
			for _, t := range topicsFromPattern(m.TopicsPattern) {
				if mine[t] {
					return fmt.Errorf("lab %s already mirrors topic %s onto %s in %s — two mirrors writing one topic name is corruption, not a test; pass --topics with names of your own, or kates migrate down --name %s",
						other, t, l.TargetCluster, l.MirrorNamespace, other)
				}
			}
		}
	}
	return nil
}

// topicsFromPattern reads the topic names back out of a topicsPattern the
// values writer produced (quoted names joined by "|"); a pattern with real
// regex syntax yields nothing.
func topicsFromPattern(pattern string) []string {
	var out []string
	for _, part := range strings.Split(pattern, "|") {
		name := javaRegexUnquote(part)
		if name == "" || strings.ContainsAny(name, `*+?()[]{}^$`) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func javaRegexUnquote(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if r == '\\' && !esc {
			esc = true
			continue
		}
		esc = false
		b.WriteRune(r)
	}
	return b.String()
}

// phaseImage builds and loads the image of every source that needs one
// Dockerfile.legacy-kafka produces — the scripts' step for the 2.x line,
// generalised to every built-image mode. --skip-build assumes it is present.
func (r *labRun) phaseImage(ctx context.Context, s *migrate.State) error {
	if r.skipBuild {
		return nil
	}
	for _, src := range r.lab.Sources() {
		if !needsBuiltImage(src) {
			continue
		}
		migrateHint(fmt.Sprintf("Building %s (no upstream image exists for the %s line)...", src.Image, src.From.MajorMinor()))
		if _, err := defaultRunner.Run(ctx, "docker", "--version"); err != nil {
			return errors.New("docker not found and --skip-build not given. Build elsewhere and load it (kates migrate image build --load), or install docker")
		}
		opts := migrateImageOptions{
			Version: src.From.String(), Scala: defaultScalaVersion, OSRelease: defaultOSRelease,
			Registry: r.lab.Registry, Load: r.env.IsKind, KindCluster: defaultKindClusterName(),
		}
		if !r.env.IsKind {
			migrateWarn("not a kind cluster: the image is built locally but not loaded anywhere — push it where the nodes can pull it (kates migrate image build --push)")
		}
		if err := runMigrateImageBuild(ctx, opts); err != nil {
			return err
		}
	}
	return nil
}

// phaseSource is the scripts' phase 2, once per source: the source
// namespace, the generated values, the legacy-kafka release, its Helm test.
// In a fan-in each source is its own pair of rows, named by its alias.
func (r *labRun) phaseSource(ctx context.Context, s *migrate.State) error {
	for _, src := range r.lab.Sources() {
		if err := r.deploySource(ctx, s, src); err != nil {
			return err
		}
	}
	return nil
}

// deploySource installs one source of the lab.
func (r *labRun) deploySource(ctx context.Context, s *migrate.State, src *migrate.Source) error {
	rep := s.Report
	l := r.lab
	deployed, serving := l.RowFor(migrate.RowSourceDeployed, src), l.RowFor(migrate.RowSourceServing, src)
	if src.Provider != kafkaversion.ProviderLegacy {
		return errors.New("a Strimzi source is not implemented in this version — use --source-provider legacy")
	}
	created, err := ensureLabNamespace(ctx, src.Namespace, l.SourceLabels(src))
	if err != nil {
		rep.Fail(deployed, err.Error())
		return err
	}
	if r.createdNamespaces == nil {
		r.createdNamespaces = map[string]bool{}
	}
	r.createdNamespaces[src.Namespace] = created
	if !created {
		migrateWarn(fmt.Sprintf("namespace %s already exists — reusing it, and leaving it behind on down", src.Namespace))
	}
	values, err := migrate.SourceValuesFor(l, src)
	if err != nil {
		rep.Fail(deployed, err.Error())
		return err
	}
	path := sourceValuesPathFor(l, src)
	if err := writeLabFile(path, values); err != nil {
		rep.Fail(deployed, err.Error())
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", helmSourceArgs(src, path, r.valuesSource, r.timeout)...); err != nil {
		rep.Fail(deployed, "helm install failed")
		r.sourceDiagnostics(ctx, src)
		return fmt.Errorf("install the source: %w", err)
	}
	rep.Pass(deployed, fmt.Sprintf("Kafka %s (%s)", src.From, src.Mode))

	if _, err := defaultRunner.Run(ctx, "helm", "test", src.Release, "-n", src.Namespace, "--timeout", formatSeconds(r.timeout)); err != nil {
		rep.Fail(serving, "helm test failed — the broker is not answering")
		if logs, lerr := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "logs", src.Release+"-legacy-kafka-test-broker"); lerr == nil && logs != "" {
			migrateHint(logs)
		}
		return fmt.Errorf("the source is not serving: %w", err)
	}
	rep.Pass(serving, src.Bootstrap)
	return nil
}

// ensureLabNamespace creates a namespace labelled as the lab's when it does
// not exist, and reports whether it did. An existing namespace is somebody's
// and is left as it is.
func ensureLabNamespace(ctx context.Context, namespace string, labels map[string]string) (created bool, err error) {
	out, err := defaultRunner.Run(ctx, "kubectl", "get", "namespace", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.name}")
	if err != nil {
		return false, fmt.Errorf("look up namespace %s: %w", namespace, err)
	}
	if strings.TrimSpace(out) != "" {
		return false, nil
	}
	if _, err := defaultRunner.Run(ctx, "kubectl", "create", "namespace", namespace); err != nil {
		return false, fmt.Errorf("create namespace %s: %w", namespace, err)
	}
	args := []string{"label", "namespace", namespace, "--overwrite"}
	args = append(args, labelPairs(labels)...)
	if _, err := defaultRunner.Run(ctx, "kubectl", args...); err != nil {
		return true, fmt.Errorf("label namespace %s: %w", namespace, err)
	}
	return true, nil
}

// labelPairs renders labels as key=value arguments, sorted.
func labelPairs(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+labels[k])
	}
	return out
}

// sourceDiagnostics prints a source's pods and broker log after a failed
// install.
func (r *labRun) sourceDiagnostics(ctx context.Context, src *migrate.Source) {
	if pods, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "get", "pods"); err == nil && pods != "" {
		migrateHint(pods)
	}
	if logs, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "logs", "sts/"+src.Release+"-legacy-kafka", "--tail=50"); err == nil && logs != "" {
		migrateHint(logs)
	}
}

// phaseCredential creates each source's credential Secret in the mirror's
// namespace for a SASL source — through a manifest on stdin, never an
// argument. A plaintext source needs none.
func (r *labRun) phaseCredential(ctx context.Context, s *migrate.State) error {
	l := r.lab
	for _, src := range l.Sources() {
		if src.Auth() == migrate.SourceAuthNone {
			continue
		}
		if src.Password == "" {
			return fmt.Errorf("the credential of source %s is unknown", src.Alias)
		}
		// The Secret lives in the mirror's namespace and is the mirror's to
		// remove, so it carries the mirror role — with the source's alias
		// beside it in a fan-in.
		labels := l.RoleLabels(migrate.RoleMirror)
		if l.FanIn() {
			labels[migrate.LabelSourceAlias] = src.Alias
		}
		manifest, err := migrate.SecretManifest(l.MirrorNamespace, src.Secret, labels, map[string]string{"password": src.Password})
		if err != nil {
			return err
		}
		if _, err := defaultRunner.RunInput(ctx, manifest, "kubectl", "-n", l.MirrorNamespace, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("create the source credential Secret %s/%s: %w", l.MirrorNamespace, src.Secret, err)
		}
		migrateHint(fmt.Sprintf("source credential %s written to secret %s/%s", src.User, l.MirrorNamespace, src.Secret))
	}
	return nil
}

// phaseMirror is the scripts' phases 5 and 6: the mirror release from the
// era preset and the generated values, the CR Ready, every connector
// RUNNING.
func (r *labRun) phaseMirror(ctx context.Context, s *migrate.State) error {
	rep := s.Report
	l := r.lab
	inputs := l.MirrorInputs()
	inputs.TargetBrokerCount = r.env.Target.Brokers
	inputs.OperatorNamespace = r.env.OperatorNamespace
	values, err := migrate.MirrorValues(l, inputs)
	if err != nil {
		rep.Fail(migrate.RowMirrorInstalled, err.Error())
		return err
	}
	path := mirrorValuesPath(l.Name)
	if err := writeLabFile(path, values); err != nil {
		rep.Fail(migrate.RowMirrorInstalled, err.Error())
		return err
	}
	if err := ensureMirrorChartDeps(ctx); err != nil {
		rep.Fail(migrate.RowMirrorInstalled, err.Error())
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", helmMirrorArgs(l, r.env.IsKind, path, r.valuesMirror)...); err != nil {
		rep.Fail(migrate.RowMirrorInstalled, "helm install failed (preflight, or the CR itself)")
		if logs, lerr := defaultRunner.Run(ctx, "kubectl", "-n", l.MirrorNamespace, "logs", "job/"+l.MirrorCR+"-preflight", "--tail=80"); lerr == nil && logs != "" {
			migrateHint(logs)
			for _, v := range migrate.ParseVerdicts(logs) {
				if v.Failed() {
					output.Error(fmt.Sprintf("preflight %s: %s", v.Code, v.Detail))
				}
			}
		}
		r.mirrorDiagnostics(ctx)
		return fmt.Errorf("install the mirror: %w", err)
	}
	rep.Pass(migrate.RowMirrorInstalled, "preflight passed — the source answered a 4.x client")

	if _, err := defaultRunner.Run(ctx, "kubectl", "-n", l.MirrorNamespace, "wait", "kafkamirrormaker2/"+l.MirrorCR,
		"--for=condition=Ready", "--timeout="+formatSeconds(r.timeout)); err != nil {
		rep.Fail(migrate.RowCRReady, fmt.Sprintf("did not become Ready in %s", formatSeconds(r.timeout)))
		r.mirrorDiagnostics(ctx)
		return fmt.Errorf("the mirror did not become Ready: %w", err)
	}
	rep.Pass(migrate.RowCRReady, "Connect workers are up")

	status, err := waitConnectorsRunning(ctx, l.MirrorNamespace, l.MirrorCR, r.timeout)
	if err != nil {
		rep.Fail(migrate.RowConnectorsRunning, err.Error())
		if status.Summary() != "" {
			migrateHint(status.Summary())
		}
		r.mirrorDiagnostics(ctx)
		return err
	}
	rep.Pass(migrate.RowConnectorsRunning, status.Summary())
	return nil
}

// readMirrorStatus reads and parses the KafkaMirrorMaker2 object.
func readMirrorStatus(ctx context.Context, namespace, cr string) (migrate.MirrorStatus, error) {
	out, err := defaultRunner.Run(ctx, "kubectl", "-n", namespace, "get", "kafkamirrormaker2", cr, "-o", "json")
	if err != nil {
		return migrate.MirrorStatus{}, err
	}
	return migrate.ParseMirrorStatus([]byte(out))
}

// waitConnectorsRunning polls the CR until the mirror's two connectors —
// and every task of theirs — report RUNNING (Ready says nothing about the
// mirror; the connector status does), every 10 seconds within the budget.
func waitConnectorsRunning(ctx context.Context, namespace, cr string, timeout time.Duration) (migrate.MirrorStatus, error) {
	deadline := migrateNow().Add(timeout)
	var last migrate.MirrorStatus
	for {
		status, err := readMirrorStatus(ctx, namespace, cr)
		if err == nil {
			last = status
			if status.RunningCount() >= 2 && status.AllRunning() {
				return status, nil
			}
		}
		if !migrateNow().Before(deadline) {
			detail := fmt.Sprintf("only %d of 2 connectors running after %s", last.RunningCount(), formatSeconds(timeout))
			if failed := last.FailedTasks(); failed > 0 {
				detail = fmt.Sprintf("%d task(s) FAILED after %s (%s)", failed, formatSeconds(timeout), last.Summary())
			}
			return last, errors.New(detail)
		}
		if err := migrateSleep(ctx, 10*time.Second); err != nil {
			return last, err
		}
	}
}

// waitConnectorStates polls the CR until the source connector is in
// sourceState and the checkpoint connector in checkpointState.
func waitConnectorStates(ctx context.Context, namespace, cr, sourceState, checkpointState string, timeout time.Duration) (migrate.MirrorStatus, error) {
	deadline := migrateNow().Add(timeout)
	var last migrate.MirrorStatus
	for {
		status, err := readMirrorStatus(ctx, namespace, cr)
		if err == nil {
			last = status
			src, okSrc := status.Connector(migrate.SourceConnectorSuffix)
			chk, okChk := status.Connector(migrate.CheckpointConnectorSuffix)
			if okSrc && okChk && src.State == sourceState && chk.State == checkpointState && status.ObservedGeneration >= status.Generation {
				return status, nil
			}
		}
		if !migrateNow().Before(deadline) {
			return last, fmt.Errorf("connectors did not reach source=%s checkpoint=%s within %s (last: %s)", sourceState, checkpointState, formatSeconds(timeout), last.Summary())
		}
		if err := migrateSleep(ctx, 10*time.Second); err != nil {
			return last, err
		}
	}
}

// mirrorDiagnostics is the scripts' dump_diagnostics: the CR's status, the
// workers' log, the source's pods.
func (r *labRun) mirrorDiagnostics(ctx context.Context) {
	l := r.lab
	migrateHint("─── diagnostics ───")
	if status, err := readMirrorStatus(ctx, l.MirrorNamespace, l.MirrorCR); err == nil {
		if status.Reason != "" || status.Message != "" {
			migrateHint(fmt.Sprintf("%s: %s %s", l.MirrorCR, status.Reason, status.Message))
		}
		for _, c := range status.Connectors {
			line := fmt.Sprintf("%s=%s tasks=%s", c.Name, c.State, strings.Join(c.Tasks, ","))
			if c.Trace != "" {
				line += " " + firstLine(c.Trace)
			}
			migrateHint(line)
		}
	}
	if logs, err := defaultRunner.Run(ctx, "kubectl", "-n", l.MirrorNamespace, "logs", "-l", "strimzi.io/name="+l.MirrorCR+"-mirrormaker2", "--tail=80"); err == nil && logs != "" {
		migrateHint(logs)
	}
	if pods, err := defaultRunner.Run(ctx, "kubectl", "-n", l.SourceNamespace, "get", "pods"); err == nil && pods != "" {
		migrateHint(pods)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// runMigrateUp is `kates migrate up`.
func runMigrateUp(ctx context.Context, f *migratePairFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.TargetNamespace, false)
	if err != nil {
		return err
	}
	if f.Interactive {
		if err := migrateInteractivePick(env, f); err != nil {
			return err
		}
	}
	res, err := resolveMigratePair(env, f)
	if err != nil {
		return err
	}
	if err := refuseUnimplemented(res); err != nil {
		return err
	}
	printNotes(res)
	if !f.Yes || f.Interactive {
		p, err := buildMigratePlan(res, f)
		if err != nil {
			return err
		}
		printMigratePlan(p)
	}
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Create lab %s (%s)?", res.Lab.Name, res.Lab.Describe())); err != nil {
		return err
	}
	migrateHint(fmt.Sprintf("creating lab %s — %s", res.Lab.Name, res.Lab.Describe()))
	r := newLabRun(res, f)
	runErr := migrate.RunPhases(ctx, traced(r.upPhases()), r.state)
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), fmt.Sprintf("lab %s", r.lab.Name)); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess(fmt.Sprintf("lab %s is up: %s", r.lab.Name, r.lab.Describe()))
	migrateHint(fmt.Sprintf("next: kates migrate verify --name %s · kates migrate status --name %s · kates migrate down --name %s", r.lab.Name, r.lab.Name, r.lab.Name))
	return nil
}

// refuseUnimplemented turns what plan can only describe into up's error.
func refuseUnimplemented(res *migrateResolution) error {
	if len(res.Unimplemented) == 0 {
		return nil
	}
	return errors.New(strings.Join(res.Unimplemented, "\n"))
}

// removeLabFiles deletes the lab's generated files (a source values file
// may hold a password).
func removeLabFiles(name string) {
	_ = os.RemoveAll(labBuildDir(name))
}
