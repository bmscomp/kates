package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

var (
	migratePlanFlags migratePairFlags

	migratePlanCmd = &cobra.Command{
		Use:   "plan --from <version> [--from <version>…] [--to <version>]",
		Short: "What `up` would create for a pair of versions — nothing changes",
		Long: `Resolves the pair against the cluster — the primary operator's window and
scope, the target Kafka, the provider each source gets — and prints the lab
up would create: namespaces, releases, provider, mode and image of every
source, the operator, the target, the mirror's policy, minSourceVersion,
preflight and topics, where the offset-syncs topic lives and what the source
principal therefore needs, and the values files it would write. Repeat --from
to mirror several sources into one target; a combination the chart would
refuse (two sources sharing an alias, or writing one topic name under the
identity policy) is refused here first. -o json prints the same as a
document. Nothing is created.`,
		Example: `  kates migrate plan --from 2.8.2
  kates migrate plan --from 3.9.1 --policy default
  kates migrate plan --from 2.8.2 --from 3.9.1
  kates migrate plan --from 4.2.1 --to 4.3.0 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigratePlan(cmd.Context(), &migratePlanFlags)
		},
	}

	migratePairsCmd = &cobra.Command{
		Use:   "pairs",
		Short: "Every old → new pair this cluster can stand up now",
		Long: `Lists the source versions a lab can be built from, grouped by the provider
each would get — the legacy-kafka chart (ZooKeeper below 3.3.0, the built
KRaft image below 3.7.0, the official image from there) or Strimzi — and
the targets the primary operator's window allows.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigratePairs(cmd.Context())
		},
	}

	migratePairsTargetCluster, migratePairsTargetNamespace string
)

func init() {
	addMigratePairFlags(migratePlanCmd, &migratePlanFlags)
	migratePairsCmd.Flags().StringVar(&migratePairsTargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Name of the target Kafka (the platform's primary)")
	migratePairsCmd.Flags().StringVar(&migratePairsTargetNamespace, "target-namespace", migrate.DefaultTargetNamespace, "Namespace of the target Kafka")
	migrateCmd.AddCommand(migratePlanCmd, migratePairsCmd)
}

// migratePlan is the plan document: what up creates, as data.
type migratePlan struct {
	Lab migratePlanLab `json:"lab"`
	// Source is the first source, kept beside Sources so a reader of the
	// document that knows one source still finds it where it was.
	Source      migratePlanSource   `json:"source"`
	Sources     []migratePlanSource `json:"sources"`
	Target      migratePlanTarget   `json:"target"`
	Mirror      migratePlanMirror   `json:"mirror"`
	Values      migratePlanValues   `json:"values"`
	Helm        []string            `json:"helm"`
	Notes       []string            `json:"notes"`
	Warnings    []string            `json:"warnings"`
	NotYet      []string            `json:"notImplemented"`
	Labels      map[string]string   `json:"labels"`
	Corpus      migratePlanCorpus   `json:"corpus"`
	Operator    string              `json:"operator"`
	Scope       string              `json:"scope"`
	ClientImg   string              `json:"clientImage"`
	Kind        bool                `json:"kind"`
	Describe    string              `json:"describe"`
	OffsetSyncs migratePlanSyncs    `json:"offsetSyncs"`
}

type migratePlanLab struct {
	Name            string `json:"name"`
	SourceNamespace string `json:"sourceNamespace"`
	MirrorNamespace string `json:"mirrorNamespace"`
	SourceRelease   string `json:"sourceRelease"`
	MirrorRelease   string `json:"mirrorRelease"`
	MirrorCR        string `json:"mirrorCR"`
}

type migratePlanSource struct {
	Alias     string   `json:"alias"`
	Version   string   `json:"version"`
	Provider  string   `json:"provider"`
	Mode      string   `json:"mode,omitempty"`
	Image     string   `json:"image,omitempty"`
	Operator  string   `json:"operator"`
	Namespace string   `json:"namespace"`
	Release   string   `json:"release"`
	Bootstrap string   `json:"bootstrap"`
	Auth      string   `json:"auth"`
	User      string   `json:"user,omitempty"`
	Secret    string   `json:"secret,omitempty"`
	Topics    []string `json:"topics"`
	Values    string   `json:"valuesPath,omitempty"`
	Build     bool     `json:"imageBuiltFirst"`
}

// migratePlanSyncs says where MirrorMaker 2 keeps its offset-syncs topic and
// what the source principal therefore needs — the difference between a
// source this release writes to and one it only reads.
type migratePlanSyncs struct {
	Location        string `json:"location"`
	ReadOnlySource  bool   `json:"readOnlySource"`
	SourceGrants    string `json:"sourceGrants"`
	WritesToSource  bool   `json:"writesToSource"`
	SourceGrantNote string `json:"note"`
}

type migratePlanTarget struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Version   string `json:"version"`
	Primary   bool   `json:"primary"`
	Brokers   int    `json:"brokers"`
	Ready     bool   `json:"ready"`
	Secret    string `json:"secret"`
	HasSecret bool   `json:"hasSecret"`
}

type migratePlanMirror struct {
	Policy           string   `json:"policy"`
	MinSourceVersion string   `json:"minSourceVersion"`
	Preflight        bool     `json:"preflight"`
	TopicsPattern    string   `json:"topicsPattern"`
	GroupID          string   `json:"groupId"`
	Alias            string   `json:"sourceAlias"`
	EraOverlay       string   `json:"eraOverlay"`
	Version          string   `json:"version"`
	StrimziVersion   string   `json:"strimziVersion"`
	ReplicationRF    int      `json:"replicationFactor"`
	ReplicatedTopics []string `json:"replicatedTopics"`
}

type migratePlanValues struct {
	SourcePath string `json:"sourcePath,omitempty"`
	MirrorPath string `json:"mirrorPath"`
	Source     string `json:"source,omitempty"`
	Mirror     string `json:"mirror"`
}

type migratePlanCorpus struct {
	Topics   []string `json:"topics"`
	Messages int      `json:"messages"`
}

// buildMigratePlan renders the plan for a resolution. The source values are
// rendered with a placeholder password so a plan never prints a credential.
func buildMigratePlan(res *migrateResolution, f *migratePairFlags) (*migratePlan, error) {
	l, env := res.Lab, res.Env
	p := &migratePlan{
		Lab: migratePlanLab{
			Name: l.Name, SourceNamespace: l.SourceNamespace, MirrorNamespace: l.MirrorNamespace,
			SourceRelease: l.SourceRelease, MirrorRelease: l.MirrorRelease, MirrorCR: l.MirrorCR,
		},
		Target: migratePlanTarget{
			Cluster: l.TargetCluster, Namespace: l.TargetNamespace, Version: l.To.String(),
			Primary: l.TargetIsPrimary, Brokers: env.Target.Brokers, Ready: env.Target.Ready,
			Secret: migrate.TargetUser, HasSecret: env.Target.HasSecret,
		},
		Mirror: migratePlanMirror{
			Policy: l.Policy, MinSourceVersion: kafkaversion.MinMirrorSource.String(), Preflight: true,
			TopicsPattern: migrate.TopicsPattern(l.Topics), GroupID: l.GroupID, Alias: l.SourceAlias,
			EraOverlay: migrate.EraOverlayPath(l.From), Version: l.To.String(), StrimziVersion: env.StrimziVersion,
		},
		Notes: append(append([]string{}, env.Notes...), res.Notes...), Warnings: res.Warnings, NotYet: res.Unimplemented,
		Labels:   l.RoleLabels(migrate.RoleSource),
		Corpus:   migratePlanCorpus{Topics: l.Topics, Messages: l.Messages},
		Operator: env.operatorLine(), Scope: env.Scope, ClientImg: env.clientImage(), Kind: env.IsKind,
		Describe:    l.Describe(),
		OffsetSyncs: planOffsetSyncs(l.ReadOnlySource),
	}
	if p.Notes == nil {
		p.Notes = []string{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	if p.NotYet == nil {
		p.NotYet = []string{}
	}
	p.Mirror.ReplicatedTopics = l.ReplicatedTopics()
	if l.Policy == migrate.PolicyDefault {
		p.Mirror.Policy = fmt.Sprintf("%s — %s lands as %s", l.Policy, l.Topics[0], l.ReplicatedTopic(l.Topics[0]))
	} else {
		p.Mirror.Policy = fmt.Sprintf("%s — topic names are preserved", l.Policy)
	}

	inputs := l.MirrorInputs()
	inputs.TargetBrokerCount = env.Target.Brokers
	inputs.OperatorNamespace = env.OperatorNamespace
	mirrorValues, err := migrate.MirrorValues(l, inputs)
	if err != nil {
		return nil, fmt.Errorf("render the mirror's values: %w", err)
	}
	p.Values.Mirror = mirrorValues
	p.Values.MirrorPath = mirrorValuesPath(l.Name)
	p.Mirror.ReplicationRF = replicationFactorFor(env.Target.Brokers)

	// One row per source: what it is, who runs it, where it lands and what
	// it carries — and, for a legacy source, the values file and the helm
	// command that installs it.
	for i, src := range l.Sources() {
		operator := res.SourceOperator
		if i < len(res.Sources) {
			operator = res.Sources[i].OperatorLine
		}
		row := migratePlanSource{
			Alias: src.Alias, Version: src.From.String(), Provider: string(src.Provider), Mode: string(src.Mode),
			Image: src.Image, Operator: operator, Namespace: src.Namespace, Release: src.Release,
			Bootstrap: src.Bootstrap, Auth: string(src.Auth()), User: src.User, Secret: src.Secret,
			Topics: src.Topics, Build: needsBuiltImage(src),
		}
		if src.Provider == kafkaversion.ProviderLegacy {
			// The values are rendered with a placeholder password so a plan
			// never prints a credential.
			shown := *src
			if shown.SASL {
				shown.Password = "<generated by up, never printed>"
			}
			sourceValues, err := migrate.SourceValuesFor(l, &shown)
			if err != nil {
				return nil, fmt.Errorf("render the values of source %s: %w", src.Alias, err)
			}
			row.Values = sourceValuesPathFor(l, src)
			if i == 0 {
				p.Values.Source, p.Values.SourcePath = sourceValues, row.Values
			}
			p.Helm = append(p.Helm, "helm "+strings.Join(helmSourceArgs(src, row.Values, f.ValuesSource, f.timeout()), " "))
		}
		p.Sources = append(p.Sources, row)
	}
	p.Source = p.Sources[0]
	p.Helm = append(p.Helm, "helm "+strings.Join(helmMirrorArgs(l, env.IsKind, p.Values.MirrorPath, f.ValuesMirror), " "))
	return p, nil
}

// planOffsetSyncs states the release's offset-syncs posture and the grants
// the source principal needs for it.
func planOffsetSyncs(readOnly bool) migratePlanSyncs {
	if readOnly {
		return migratePlanSyncs{
			Location: "target", ReadOnlySource: true, WritesToSource: false,
			SourceGrants:    "Read + Describe on the mirrored topics (nothing else)",
			SourceGrantNote: "--read-only-source: mm2-offset-syncs.* is kept on the target (KIP-716), so nothing is written to the source. Changing this on a running mirror restarts translation from scratch.",
		}
	}
	return migratePlanSyncs{
		Location: "source", ReadOnlySource: false, WritesToSource: true,
		SourceGrants:    "Read + Describe on the mirrored topics, and Create + Write + Describe on mm2-offset-syncs.*",
		SourceGrantNote: "Kafka's default: MirrorMaker writes its offset-syncs topic to the SOURCE. Pass --read-only-source for a cluster you may only read.",
	}
}

// replicationFactorFor is the mirror's replication factor for a broker
// count, the values writer's rule: min(3, brokers), 1 when unknown.
func replicationFactorFor(brokers int) int {
	switch {
	case brokers >= 3:
		return 3
	case brokers > 0:
		return brokers
	default:
		return 1
	}
}

// printMigratePlan prints the plan as a document.
func printMigratePlan(p *migratePlan) {
	if outputMode == "json" {
		output.JSON(p)
		return
	}
	output.Header("Migration lab " + p.Lab.Name)
	output.KeyValue("pair", p.Describe)
	output.KeyValue("operator", p.Operator)
	output.KeyValue("scope", p.Scope)
	output.KeyValue("cluster", map[bool]string{true: "kind (values-kind.yaml applied first)", false: "generic"}[p.Kind])

	for _, src := range p.Sources {
		title := "source"
		if len(p.Sources) > 1 {
			title += " " + src.Alias
		}
		output.SubHeader(title)
		output.KeyValue("version", src.Version)
		provider := src.Provider
		if src.Mode != "" {
			provider += " — " + legacyModeWords(kafkaversion.LegacyMode(src.Mode))
		}
		output.KeyValue("provider", provider)
		if src.Image != "" {
			img := src.Image
			if src.Build {
				img += "  (built and loaded first unless --skip-build)"
			}
			output.KeyValue("image", img)
		}
		output.KeyValue("operator", src.Operator)
		output.KeyValue("namespace", src.Namespace+"  (created by up, labelled "+migrate.LabelLab+"="+p.Lab.Name+")")
		output.KeyValue("release", src.Release+"  "+migrate.SourceChartPath)
		output.KeyValue("bootstrap", src.Bootstrap)
		auth := src.Auth
		if src.User != "" {
			auth += fmt.Sprintf(" as %s (secret %s in %s)", src.User, src.Secret, p.Lab.MirrorNamespace)
		}
		output.KeyValue("auth", auth)
		output.KeyValue("corpus topics", strings.Join(src.Topics, ", "))
	}

	output.SubHeader("target")
	where := "an additional cluster created by up"
	if p.Target.Primary {
		where = "the platform's primary"
	}
	output.KeyValue("cluster", fmt.Sprintf("%s in %s (%s)", p.Target.Cluster, p.Target.Namespace, where))
	output.KeyValue("version", p.Target.Version)
	ready := "not found"
	if p.Target.Ready {
		ready = "Ready"
	} else if p.Target.Brokers > 0 {
		ready = "not Ready"
	}
	output.KeyValue("state", fmt.Sprintf("%s, %d broker(s)", ready, p.Target.Brokers))
	secret := p.Target.Secret + " (present)"
	if !p.Target.HasSecret {
		secret = p.Target.Secret + " (missing — the kafka-cluster chart provisions the KafkaUser)"
	}
	output.KeyValue("credential", secret)

	output.SubHeader("mirror")
	output.KeyValue("release", p.Lab.MirrorRelease+" in "+p.Lab.MirrorNamespace+"  "+migrate.MirrorChartPath)
	output.KeyValue("CR", p.Lab.MirrorCR)
	aliases := make([]string, 0, len(p.Sources))
	for _, src := range p.Sources {
		aliases = append(aliases, src.Alias)
	}
	label := "group / alias"
	if len(aliases) > 1 {
		label = "group / aliases"
	}
	output.KeyValue(label, p.Mirror.GroupID+" / "+strings.Join(aliases, ", "))
	if len(p.Sources) > 1 {
		output.KeyValue("mirrors", fmt.Sprintf("%d in one release — one per source, each with its own topics, ACLs, egress and alerts", len(p.Sources)))
	}
	output.KeyValue("policy", p.Mirror.Policy)
	output.KeyValue("minSourceVersion", p.Mirror.MinSourceVersion+" (enforced)")
	output.KeyValue("preflight", "on, failOnError")
	output.KeyValue("topics", strings.Join(p.Corpus.Topics, ", ")+"  (the chart escapes them into topicsPattern)")
	output.KeyValue("replicated as", strings.Join(p.Mirror.ReplicatedTopics, ", "))
	output.KeyValue("era overlay", p.Mirror.EraOverlay)
	output.KeyValue("workers", fmt.Sprintf("Kafka %s (Strimzi %s), replication factor %d", p.Mirror.Version, p.Mirror.StrimziVersion, p.Mirror.ReplicationRF))
	output.KeyValue("client image", p.ClientImg)
	output.KeyValue("corpus", fmt.Sprintf("%d records to %s", p.Corpus.Messages, strings.Join(p.Corpus.Topics, ", ")))

	output.SubHeader("offset-syncs")
	output.KeyValue("location", p.OffsetSyncs.Location)
	if p.OffsetSyncs.ReadOnlySource {
		output.KeyValue("writes to the source", "none — the mirror writes nothing to any source cluster")
	} else {
		output.KeyValue("writes to the source", "mm2-offset-syncs.<target>.internal on each source")
	}
	output.KeyValue("source principal needs", p.OffsetSyncs.SourceGrants)
	output.Hint(p.OffsetSyncs.SourceGrantNote)

	output.SubHeader("values files up would write")
	for _, src := range p.Sources {
		if src.Values != "" {
			output.Hint(src.Values)
		}
	}
	output.Hint(p.Values.MirrorPath)

	output.SubHeader("helm")
	for _, h := range p.Helm {
		output.Hint(h)
	}
	if len(p.Notes) > 0 {
		output.SubHeader("notes")
		for _, n := range p.Notes {
			output.Hint("- " + n)
		}
	}
	for _, w := range p.Warnings {
		output.Warn(w)
	}
	if len(p.NotYet) > 0 {
		output.SubHeader("not in this version")
		for _, n := range p.NotYet {
			output.Warn(n)
		}
	}
	fmt.Fprintln(output.Out)
	output.Hint("Nothing was created.")
}

// runMigratePlan is `kates migrate plan`.
func runMigratePlan(ctx context.Context, f *migratePairFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.TargetNamespace, true)
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
	p, err := buildMigratePlan(res, f)
	if err != nil {
		return err
	}
	printMigratePlan(p)
	return nil
}

// migratePairRow is one line of `kates migrate pairs`.
type migratePairRow struct {
	From     string   `json:"from"`
	Provider string   `json:"provider"`
	Mode     string   `json:"mode,omitempty"`
	To       []string `json:"to"`
	Note     string   `json:"note,omitempty"`
}

// migratePairRows builds the pairs table from the primary operator's window
// and the legacy chart's ranges. The row for versions an eligible *other*
// operator could run (the dropped line, namespace scope) needs the chart
// catalogue, which this version does not consult: under cluster scope those
// versions are legacy anyway, which is what the note says.
//
// TODO(multi-version plan §3.9): add the "Strimzi <v>, own namespace" rows
// from the catalogue once the namespace-scoped source lands.
func migratePairRows(env *migrateEnv) []migratePairRow {
	to := env.Window.Strings()
	for i, v := range to {
		if v == env.Target.Version.String() {
			to[i] = "[" + v + "]"
		}
	}
	oldest, hasWindow := kafkaversion.Version{}, len(env.Window) > 0
	if hasWindow {
		oldest = env.Window[0]
	}
	rows := []migratePairRow{
		{From: kafkaversion.MinMirrorSource.String() + " – 3.2.x", Provider: "legacy — ZooKeeper, built image", Mode: string(kafkaversion.LegacyZooKeeper), To: to, Note: "image built on first use (kates migrate image build --load)"},
		{From: "3.3.0 – 3.6.x", Provider: "legacy — KRaft, built image", Mode: string(kafkaversion.LegacyKRaftBuilt), To: to, Note: "image built on first use"},
	}
	official := migratePairRow{From: "3.7.0 – ", Provider: "legacy — KRaft, official image", Mode: string(kafkaversion.LegacyKRaftOfficial), To: to}
	if hasWindow {
		official.From += justBelow(oldest)
		if env.Scope == migrate.ScopeCluster {
			official.Note = fmt.Sprintf("the %s line below %s is legacy under cluster scope: the cluster-wide Strimzi %s cannot run it", oldest.MajorMinor()[:1]+".x", oldest, env.StrimziVersion)
		} else {
			official.Note = fmt.Sprintf("the %s line below %s could run under its own operator (namespace scope) — not in this version; legacy for now", oldest.MajorMinor()[:1]+".x", oldest)
		}
		rows = append(rows, official, migratePairRow{
			From:     fmt.Sprintf("%s – %s", env.Window[0], env.Window[len(env.Window)-1]),
			Provider: fmt.Sprintf("Strimzi %s, primary's operator", env.StrimziVersion),
			To:       to,
			Note:     "in-window: both ends on the same operator version — plan only in this version (--source-provider legacy runs it under the official image)",
		})
	} else {
		official.From += "…"
		rows = append(rows, official)
	}
	return rows
}

// justBelow renders the version line just below v: 4.2.0 → "4.1.x", 5.0.0
// → "4.x".
func justBelow(v kafkaversion.Version) string {
	if v.Minor > 0 {
		return fmt.Sprintf("%d.%d.x", v.Major, v.Minor-1)
	}
	return fmt.Sprintf("%d.x", v.Major-1)
}

// runMigratePairs is `kates migrate pairs`.
func runMigratePairs(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	env, err := discoverMigrateEnv(ctx, migratePairsTargetCluster, migratePairsTargetNamespace, true)
	if err != nil {
		return err
	}
	rows := migratePairRows(env)
	if outputMode == "json" {
		output.JSON(map[string]any{
			"operator": env.operatorLine(),
			"scope":    env.Scope,
			"window":   env.Window.Strings(),
			"target":   env.Target.Version.String(),
			"pairs":    rows,
		})
		return nil
	}
	for _, n := range env.Notes {
		output.Hint(n)
	}
	output.KeyValue("operator", env.operatorLine())
	fmt.Fprintln(output.Out)
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.From, r.Provider, strings.Join(r.To, " "), r.Note})
	}
	output.Table([]string{"FROM (source)", "PROVIDER", "TO (target)", "NOTE"}, table)
	output.Hint("The target in brackets is the primary as it runs. kates migrate plan --from <version> shows what a pair creates.")
	return nil
}

// migrateInteractivePick is the pair picker of `up -i`: the old version
// among the candidates pairs lists (each with the provider it would get),
// the new version from the window with the primary's preselected, then the
// policy, the topics and the corpus size. Flags fill the defaults; --yes
// skips only the confirmation, never this.
func migrateInteractivePick(env *migrateEnv, f *migratePairFlags) error {
	if !IsInteractive() {
		return errors.New("-i needs a terminal: pass --from (and the other flags) instead")
	}
	candidates := migratePickCandidates(env)
	from := ""
	if len(f.From) > 0 {
		from = f.From[0]
	}
	if from == "" && len(candidates) > 0 {
		from = candidates[0].Value
	}
	fromOptions := append([]huh.Option[string]{}, candidates...)
	fromOptions = append(fromOptions, huh.NewOption("another version…", "other"))
	to := env.Target.Version.String()
	if f.To != "" {
		to = f.To
	}
	var toOptions []huh.Option[string]
	for _, v := range env.Window {
		label := v.String()
		if v == env.Target.Version {
			label += " (the primary as it runs)"
		}
		toOptions = append(toOptions, huh.NewOption(label, v.String()))
	}
	policy, topics, messages, sasl := f.Policy, f.Topics, strconv.Itoa(f.Messages), f.SASL
	if topics == "" {
		topics = migrate.DefaultTopic
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Migrate from").
				Description("The old Kafka, with the provider it gets on this cluster ("+env.operatorLine()+")").
				Options(fromOptions...).
				Value(&from),
			huh.NewSelect[string]().
				Title("Migrate to").
				Description("The primary operator's window: "+env.Window.String()).
				Options(toOptions...).
				Value(&to),
		),
	).WithTheme(ThemeKates())
	if err := form.Run(); err != nil {
		return err
	}
	if from == "other" {
		from = ""
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Source Kafka version (x.y.z, at least " + kafkaversion.MinMirrorSource.String() + ")").Value(&from).
				Validate(func(s string) error { _, err := kafkaversion.Parse(s); return err }),
		)).WithTheme(ThemeKates()).Run(); err != nil {
			return err
		}
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Replication policy").
			Options(
				huh.NewOption("identity — topic names are preserved (the migration case)", migrate.PolicyIdentity),
				huh.NewOption("default — renamed to <alias>.<topic>", migrate.PolicyDefault),
			).
			Value(&policy),
		huh.NewInput().Title("Corpus topics (comma-separated)").Value(&topics),
		huh.NewInput().Title("Records per topic").Value(&messages).
			Validate(func(s string) error {
				n, err := strconv.Atoi(s)
				if err != nil || n <= 0 {
					return errors.New("want a positive count")
				}
				return nil
			}),
		huh.NewConfirm().Title("SASL/PLAIN on the legacy source?").Value(&sasl),
	)).WithTheme(ThemeKates()).Run(); err != nil {
		return err
	}
	// The picker resolves one source; a fan-in is a --from repeated on the
	// command line, so the rest of the flags' sources are left as they are.
	if len(f.From) > 1 {
		f.From = append([]string{from}, f.From[1:]...)
	} else {
		f.From = []string{from}
	}
	f.To, f.Policy, f.Topics, f.SASL = to, policy, topics, sasl
	f.Messages, _ = strconv.Atoi(messages)
	return nil
}

// migratePickCandidates lists the picker's source candidates: the
// documented examples of the legacy chart's two shapes (the versions its
// values-kafka-2x.yaml and values-kafka-3x.yaml carry — the charts are the
// source of truth for them, not a table in Go) and every version of the
// primary operator's window.
func migratePickCandidates(env *migrateEnv) []huh.Option[string] {
	var opts []huh.Option[string]
	for _, overlay := range []string{"values-kafka-2x.yaml", "values-kafka-3x.yaml"} {
		v, ok := legacyOverlayVersion(migrate.SourceChartPath + "/" + overlay)
		if !ok {
			continue
		}
		mode := kafkaversion.LegacyModeFor(v)
		label := fmt.Sprintf("%s — legacy (%s)", v, legacyModeWords(mode))
		if env.Scope == migrate.ScopeCluster && env.Window.Supports(v) {
			label = fmt.Sprintf("%s — Strimzi (primary's operator)", v)
		}
		opts = append(opts, huh.NewOption(label, v.String()))
	}
	for _, v := range env.Window {
		if v == env.Target.Version {
			continue
		}
		opts = append(opts, huh.NewOption(fmt.Sprintf("%s — Strimzi %s (primary's operator; plan only in this version)", v, env.StrimziVersion), v.String()))
	}
	return opts
}

// legacyOverlayVersion reads kafka.version from a legacy-kafka overlay.
func legacyOverlayVersion(path string) (kafkaversion.Version, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return kafkaversion.Version{}, false
	}
	var doc struct {
		Kafka struct {
			Version string `yaml:"version"`
		} `yaml:"kafka"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Kafka.Version == "" {
		return kafkaversion.Version{}, false
	}
	v, err := kafkaversion.Parse(doc.Kafka.Version)
	if err != nil {
		return kafkaversion.Version{}, false
	}
	return v, true
}
