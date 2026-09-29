// Package migrate holds the pure logic of a cross-version Kafka migration
// lab driven by MirrorMaker 2: the lab's names and labels derived from a
// pair of versions, the Helm values written for the legacy-kafka and
// mirror-maker2 charts, the parsers for what the cluster answers
// (KafkaMirrorMaker2 status, the preflight Job's verdicts, the corpus read
// back off the target), the assertion report, and the phase runner that
// `kates migrate verify` and `run` share.
//
// Nothing here talks to a cluster or runs a process. Every function returns
// data and errors; the cmd layer prints. The package replaces
// scripts/test-mm2-migration.sh, whose eleven phases and eighteen report
// rows it keeps by name.
package migrate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

const (
	// LabelLab marks every resource of a lab with its name; the lab is found
	// on the cluster by this label, not by a state file.
	LabelLab = "kates.io/lab"
	// LabelRole says which side of the lab a resource belongs to.
	LabelRole = "kates.io/lab-role"
	// LabelSourceAlias names the source a resource belongs to in a lab with
	// several of them; absent when the lab has one source, whose alias is
	// SourceAlias.
	LabelSourceAlias = "kates.io/lab-source"

	// RoleSource is the old Kafka, RoleTarget the new one (when the lab
	// creates it), RoleMirror the MirrorMaker 2 release.
	RoleSource = "source"
	RoleTarget = "target"
	RoleMirror = "mirror"

	// PolicyIdentity keeps topic names across the mirror — the migration
	// case; PolicyDefault renames them to <alias>.<topic>.
	PolicyIdentity = "identity"
	PolicyDefault  = "default"

	// ScopeCluster is one cluster-wide operator; ScopeNamespace one
	// operator per Kafka namespace (see the multi-version plan, §3.1).
	ScopeCluster   = "cluster"
	ScopeNamespace = "namespace"

	// DefaultTopic is the corpus topic when --topics is not given. A lab with
	// several sources gives each one its own — <DefaultTopic>.<alias> — so the
	// legs never write the same target topic name under the identity policy.
	DefaultTopic = "kates.orders"
	// DefaultMessages is the corpus size when --messages is not given.
	DefaultMessages = 200
	// DefaultTargetCluster and DefaultTargetNamespace are the platform's
	// primary Kafka, the target when --to is omitted.
	DefaultTargetCluster   = "krafter"
	DefaultTargetNamespace = "kafka"
	// TargetUser is the MirrorMaker 2 principal the kafka-cluster chart
	// provisions on every cluster it deploys; its Secret carries the same
	// name.
	TargetUser = "kates-mm2"
	// LegacySASLUser is the SASL/PLAIN reader the legacy-kafka chart's SASL
	// listener accepts (values-sasl.yaml).
	LegacySASLUser = "mm2reader"
	// SourceAlias is the MirrorMaker 2 alias of the source cluster in a lab
	// with one source. Several sources take SourceAliasFor(version) instead.
	SourceAlias = "source"
	// SourceAliasPrefix opens the alias of a source in a multi-source lab:
	// src282, src391.
	SourceAliasPrefix = "src"
	// ClusterDomain is the Kubernetes cluster domain used in bootstrap
	// addresses.
	ClusterDomain = "cluster.local"

	// maxNameLen keeps every derived object name — the source StatefulSet,
	// its headless Service, the MirrorMaker 2 pods and their Services — under
	// the 63-character DNS label limit.
	maxNameLen = 24
)

// Options is what the front door (`kates migrate up|plan|run --from --to`)
// resolves from its flags and the cluster before a lab is built.
type Options struct {
	// Name of the lab; defaults to m<from digits>-<to digits> (m282-430).
	Name string
	// From is the source Kafka version, To the target's.
	From, To kafkaversion.Version
	// SourceProvider is who runs the source: ProviderLegacy (default) or
	// ProviderStrimzi.
	SourceProvider kafkaversion.Provider
	// SourceMode is the legacy chart's mode for a legacy source; derived
	// from From when empty.
	SourceMode kafkaversion.LegacyMode
	// SourceStrimziVersion is the operator an additional Strimzi source
	// runs under (namespace scope); empty means the primary's.
	SourceStrimziVersion string
	// AlsoFrom are the further sources of a fan-in lab — `--from` given more
	// than once. Each becomes its own entry of Sources with its own alias,
	// namespace, release, credential and corpus topics, and its own mirror in
	// the one MirrorMaker 2 release. A lab with none is named, labelled and
	// installed exactly as it was before fan-in existed.
	AlsoFrom []SourceSpec
	// ReadOnlySource keeps every MirrorMaker 2 write on the target: the
	// generated values set readOnlySource on each mirror, which the chart
	// turns into offset-syncs.topic.location=target (KIP-716) on both
	// connectors and into the matching target ACL. Off by default — the lab
	// owns both ends of its own source, so the default location (the source)
	// is the honest one there.
	ReadOnlySource bool
	// TargetIsPrimary says the target is the platform's primary cluster.
	// When false and TargetCluster is empty, the lab names an additional
	// target <name>-tgt in kafka-<name>-tgt. Set it explicitly: the zero
	// value means an additional target.
	TargetIsPrimary bool
	// TargetCluster and TargetNamespace name the target Kafka; defaulted
	// from TargetIsPrimary when empty.
	TargetCluster, TargetNamespace string
	// Policy is PolicyIdentity (default) or PolicyDefault.
	Policy string
	// Topics to seed and mirror; DefaultTopic when empty.
	Topics []string
	// Messages in the corpus; DefaultMessages when zero.
	Messages int
	// SASL puts the legacy source behind its SASL/PLAIN listener instead of
	// the plaintext one. Ignored for a Strimzi source, which always
	// authenticates.
	SASL bool
	// SourcePassword is the SASL/PLAIN password of the legacy source; a
	// random one is generated when empty. Set it only to make a lab
	// reproducible in a test.
	SourcePassword string
	// SourceTLS puts a Strimzi source behind its TLS listener (9093) with the
	// cluster CA trusted; the plain SCRAM listener (9092) otherwise.
	SourceTLS bool
	// Registry holds the built legacy images; kafkaversion's default when
	// empty.
	Registry string
	// StrimziVersion is the target's operator version, written into the
	// mirror's values.
	StrimziVersion string
	// Scope is the operator scope in force: ScopeCluster (default) or
	// ScopeNamespace.
	Scope string
}

// SourceSpec names a further source of a lab: its Kafka version and who
// runs it. Everything else — the alias, the namespace, the release, the
// credential and the corpus topics — is derived from the version exactly as
// the first source's is, so `--from 2.8.2 --from 3.9.1` needs nothing else.
type SourceSpec struct {
	// From is the source's Kafka version.
	From kafkaversion.Version
	// Provider is who runs it: ProviderLegacy (default) or ProviderStrimzi.
	Provider kafkaversion.Provider
	// Mode is the legacy chart's mode; derived from From when empty.
	Mode kafkaversion.LegacyMode
	// StrimziVersion is the operator a Strimzi source runs under.
	StrimziVersion string
}

// Source is one source cluster of a lab, fully named: what the mirror calls
// it, where it runs, how it is reached and what corpus it carries. A lab has
// one per `--from`.
type Source struct {
	// From is the source's Kafka version, Provider who runs it, Mode the
	// legacy chart's mode and StrimziVersion the operator's when it has one.
	From           kafkaversion.Version
	Provider       kafkaversion.Provider
	Mode           kafkaversion.LegacyMode
	StrimziVersion string
	// Alias is the MirrorMaker 2 alias: "source" in a single-source lab,
	// src282 / src391 in a fan-in. It names the replicated topic prefix, the
	// offset-syncs topic and the checkpoints topic, so it is unique per lab.
	Alias string
	// Namespace, Cluster and Release name the source on the cluster.
	Namespace, Cluster, Release string
	// Image is the broker image of a legacy source, empty for a Strimzi one.
	Image string
	// Bootstrap is how the mirror and the client pods reach it.
	Bootstrap string
	// User, Secret and CASecret are the credential the mirror reads it with;
	// Password is the generated SASL password of a legacy source.
	User, Secret, CASecret, Password string
	// SASL and TLS say which listener is in use.
	SASL, TLS bool
	// Topics is this source's corpus: the lab's topics in a single-source
	// lab, one per source (<topic>.<alias>) when the lab derived them.
	Topics []string
}

// Auth is how MirrorMaker 2 authenticates to this source.
func (s *Source) Auth() SourceAuth {
	switch {
	case s.Provider == kafkaversion.ProviderStrimzi && s.TLS:
		return SourceAuthTLS
	case s.Provider == kafkaversion.ProviderStrimzi, s.SASL:
		return SourceAuthSASL
	default:
		return SourceAuthNone
	}
}

// Mechanism is the SASL mechanism of this source's listener: PLAIN for the
// legacy chart, SCRAM-SHA-512 for a Strimzi cluster, empty without
// authentication.
func (s *Source) Mechanism() string {
	switch s.Auth() {
	case SourceAuthNone:
		return ""
	case SourceAuthSASL:
		if s.Provider == kafkaversion.ProviderLegacy {
			return MechanismPlain
		}
		return MechanismScramSHA512
	default:
		return MechanismScramSHA512
	}
}

// Describe is the one-line summary of a source: "Kafka 2.8.2 (legacy,
// zookeeper)".
func (s *Source) Describe() string {
	out := fmt.Sprintf("Kafka %s (%s", s.From, s.Provider)
	if s.Provider == kafkaversion.ProviderLegacy {
		out += ", " + string(s.Mode)
	} else if s.StrimziVersion != "" {
		out += " " + s.StrimziVersion
	}
	return out + ")"
}

// Lab is a migration lab fully named: every namespace, release, cluster,
// bootstrap address, credential name and label the commands need, derived
// once from Options so that `up`, `status`, `verify`, `cutover` and `down`
// agree on all of them.
type Lab struct {
	Options

	// SourceNamespace holds the source Kafka: kafka-<name>-src.
	SourceNamespace string
	// SourceCluster is the source Kafka's name (a Kafka CR for a Strimzi
	// source, the Helm release for a legacy one): <name>-src.
	SourceCluster string
	// SourceRelease is the Helm release of the source: <name>-src.
	SourceRelease string
	// SourceImage is the broker image of a legacy source; empty for a
	// Strimzi source (the operator chooses).
	SourceImage string
	// SourceBootstrap is the address MirrorMaker 2 and the client pods use
	// to reach the source, from inside the cluster.
	SourceBootstrap string
	// SourceUser is the principal the mirror reads the source as: empty for
	// a plaintext legacy source, LegacySASLUser for a SASL one, TargetUser
	// (which the kafka-cluster chart provisions everywhere) for a Strimzi
	// source.
	SourceUser string
	// SourceSecret is the Secret in the mirror's namespace that holds the
	// source credential under the key "password". The CLI creates it (a
	// legacy SASL source: the chart's JAAS Secret has no password key) or
	// copies it under this name (a Strimzi source: its user Secret is named
	// kates-mm2, the same as the target's, so it cannot be synced as-is).
	SourceSecret string
	// SourceCASecret is the Strimzi source's cluster CA Secret when
	// SourceTLS is set.
	SourceCASecret string
	// SourceTopics is the first source's corpus: Topics itself in a
	// single-source lab, its own per-source names in a fan-in.
	SourceTopics []string
	// ExtraSources are the lab's sources after the first, one per extra
	// --from. The first source is the flat Source* fields above, because
	// every command that works with one source — verify, cutover, mirror
	// deploy against a real cluster — reads and writes them; Sources()
	// returns the whole list with the first rebuilt from them, and is what
	// anything per-source must walk.
	ExtraSources []*Source

	// MirrorRelease is the mirror-maker2 Helm release: mm2-<name>.
	MirrorRelease string
	// MirrorNamespace is where the mirror runs: the target's namespace.
	MirrorNamespace string
	// MirrorCR is the KafkaMirrorMaker2 resource the chart creates:
	// <release>-mirror-maker2.
	MirrorCR string
	// GroupID is the Connect group of the mirror: mm2-<name>.
	GroupID string
	// SourceAlias is the mirror's alias for the source.
	SourceAlias string
	// ConsumerGroup is the group verify commits on the source and expects
	// translated on the target; VerifyGroup is the group verify reads the
	// target with. Both are lab-scoped so two labs never share one.
	ConsumerGroup, VerifyGroup string

	// Labels is the lab's common label set (LabelLab); RoleLabels adds the
	// role.
	Labels map[string]string
}

var (
	labName   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	topicName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)
)

// New validates o, fills its defaults and derives the lab. Errors name the
// option at fault; a source below the KIP-896 floor wraps
// kafkaversion.ErrBelowMirrorFloor. With several sources it also refuses,
// before anything touches a cluster, the two combinations the chart itself
// would refuse at render time: two sources whose aliases collide, and two
// sources that would write the same target topic under the identity policy.
func New(o Options) (*Lab, error) {
	if o.From == (kafkaversion.Version{}) {
		return nil, errors.New("source version (--from) is required")
	}
	if o.To == (kafkaversion.Version{}) {
		return nil, errors.New("target version (--to) is required")
	}
	specs, err := normaliseSources(o)
	if err != nil {
		return nil, err
	}
	o.SourceProvider, o.SourceMode = specs[0].Provider, specs[0].Mode
	o.AlsoFrom = specs[1:]
	if o.Name == "" {
		o.Name = DefaultNameFor(sourceVersions(specs), o.To)
	}
	if !labName.MatchString(o.Name) || len(o.Name) > maxNameLen {
		return nil, fmt.Errorf("lab name %q must be lowercase letters, digits and dashes, at most %d characters", o.Name, maxNameLen)
	}
	switch o.Policy {
	case "":
		o.Policy = PolicyIdentity
	case PolicyIdentity, PolicyDefault:
	default:
		return nil, fmt.Errorf("policy %q: want %s or %s", o.Policy, PolicyIdentity, PolicyDefault)
	}
	switch o.Scope {
	case "":
		o.Scope = ScopeCluster
	case ScopeCluster, ScopeNamespace:
	default:
		return nil, fmt.Errorf("operator scope %q: want %s or %s", o.Scope, ScopeCluster, ScopeNamespace)
	}
	seen := map[string]bool{}
	for _, t := range o.Topics {
		if !topicName.MatchString(t) || t == "." || t == ".." {
			return nil, fmt.Errorf("topic %q is not a valid Kafka topic name", t)
		}
		if seen[t] {
			return nil, fmt.Errorf("topic %q is listed twice", t)
		}
		seen[t] = true
	}
	switch {
	case o.Messages == 0:
		o.Messages = DefaultMessages
	case o.Messages < 0:
		return nil, fmt.Errorf("messages %d: want a positive count", o.Messages)
	}
	if o.TargetCluster == "" {
		if o.TargetIsPrimary {
			o.TargetCluster = DefaultTargetCluster
		} else {
			o.TargetCluster = o.Name + "-tgt"
		}
	}
	if o.TargetNamespace == "" {
		if o.TargetIsPrimary {
			o.TargetNamespace = DefaultTargetNamespace
		} else {
			o.TargetNamespace = "kafka-" + o.Name + "-tgt"
		}
	}
	for _, n := range []struct{ what, v string }{{"target cluster", o.TargetCluster}, {"target namespace", o.TargetNamespace}} {
		if !labName.MatchString(n.v) || len(n.v) > 63 {
			return nil, fmt.Errorf("%s %q is not a DNS label", n.what, n.v)
		}
	}

	l := &Lab{Options: o}
	l.MirrorRelease = "mm2-" + o.Name
	l.MirrorNamespace = o.TargetNamespace
	l.MirrorCR = l.MirrorRelease + "-mirror-maker2"
	l.GroupID = "mm2-" + o.Name
	l.ConsumerGroup = "kates-migration-" + o.Name
	l.VerifyGroup = "kates-migration-verify-" + o.Name
	l.Labels = map[string]string{LabelLab: o.Name}

	sources, err := deriveSources(l, specs)
	if err != nil {
		return nil, err
	}
	first := sources[0]
	l.SourceProvider, l.SourceMode, l.SourceStrimziVersion = first.Provider, first.Mode, first.StrimziVersion
	l.SourceAlias = first.Alias
	l.SourceNamespace, l.SourceCluster, l.SourceRelease = first.Namespace, first.Cluster, first.Release
	l.SourceImage, l.SourceBootstrap = first.Image, first.Bootstrap
	l.SourceUser, l.SourceSecret, l.SourceCASecret = first.User, first.Secret, first.CASecret
	l.SourcePassword, l.SASL = first.Password, first.SASL
	l.SourceTopics = first.Topics
	l.ExtraSources = sources[1:]
	// The lab's Topics is every source's corpus, in source order: what down
	// deletes on the target and what an overlapping lab is checked against.
	l.Topics = nil
	for _, s := range sources {
		l.Topics = append(l.Topics, s.Topics...)
	}
	return l, nil
}

// normaliseSources validates and defaults every source of o — the one From
// names and each of AlsoFrom — and returns them in order.
func normaliseSources(o Options) ([]SourceSpec, error) {
	specs := make([]SourceSpec, 0, 1+len(o.AlsoFrom))
	specs = append(specs, SourceSpec{From: o.From, Provider: o.SourceProvider, Mode: o.SourceMode, StrimziVersion: o.SourceStrimziVersion})
	specs = append(specs, o.AlsoFrom...)
	for i := range specs {
		s := &specs[i]
		if s.From == (kafkaversion.Version{}) {
			return nil, errors.New("source version (--from) is required")
		}
		if s.From.Less(kafkaversion.MinMirrorSource) {
			return nil, fmt.Errorf("source version %s is %w: %s is the oldest broker a Kafka 4.x MirrorMaker 2 can read (KIP-896)",
				s.From, kafkaversion.ErrBelowMirrorFloor, kafkaversion.MinMirrorSource)
		}
		switch s.Provider {
		case "":
			s.Provider = kafkaversion.ProviderLegacy
		case kafkaversion.ProviderLegacy, kafkaversion.ProviderStrimzi:
		default:
			return nil, fmt.Errorf("source provider %q: want %s or %s", s.Provider, kafkaversion.ProviderLegacy, kafkaversion.ProviderStrimzi)
		}
		if s.Provider == kafkaversion.ProviderLegacy {
			if s.Mode == "" {
				s.Mode = kafkaversion.LegacyModeFor(s.From)
			}
			switch s.Mode {
			case kafkaversion.LegacyZooKeeper, kafkaversion.LegacyKRaftBuilt, kafkaversion.LegacyKRaftOfficial:
			default:
				return nil, fmt.Errorf("source mode %q is not a legacy-kafka mode", s.Mode)
			}
			if s.StrimziVersion != "" {
				return nil, errors.New("a legacy source runs under no operator: drop --source-strimzi-version or use --source-provider strimzi")
			}
		} else {
			s.Mode = ""
		}
	}
	return specs, nil
}

// sourceVersions lists the versions of the specs, in order.
func sourceVersions(specs []SourceSpec) []kafkaversion.Version {
	out := make([]kafkaversion.Version, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.From)
	}
	return out
}

// deriveSources names every source of the lab: its alias, its namespace, its
// release, its image, its bootstrap address, its credential and its corpus.
// A lab with one source keeps the names it has always had (<name>-src,
// alias "source", the lab's own topics); a lab with several gives each
// source the alias of its version and derives the rest from it — including
// its own corpus topics, so the legs do not collide on the target.
func deriveSources(l *Lab, specs []SourceSpec) ([]*Source, error) {
	o := l.Options
	multi := len(specs) > 1
	aliases := map[string]kafkaversion.Version{}
	sources := make([]*Source, 0, len(specs))
	for _, spec := range specs {
		alias := SourceAlias
		suffix := "src"
		if multi {
			alias = SourceAliasFor(spec.From)
			suffix = alias
		}
		if !labName.MatchString(alias) {
			return nil, fmt.Errorf("source alias %q derived from --from %s is not a Kafka alias: lowercase letters, digits and dashes, no dots (a dot is MirrorMaker's own separator)", alias, spec.From)
		}
		if other, ok := aliases[alias]; ok {
			return nil, fmt.Errorf("--from %s and --from %s both derive the source alias %q: an alias names the replicated topic prefix, the offset-syncs topic and the checkpoints topic, so two sources sharing one interleave into a single set of target topics without any error. Name each source version once, and run a second lab (--name) for a repeat.",
				other, spec.From, alias)
		}
		aliases[alias] = spec.From

		s := &Source{
			From: spec.From, Provider: spec.Provider, Mode: spec.Mode, StrimziVersion: spec.StrimziVersion,
			Alias:     alias,
			Namespace: "kafka-" + o.Name + "-" + suffix,
			Cluster:   o.Name + "-" + suffix,
			Release:   o.Name + "-" + suffix,
			Topics:    corpusTopics(o.Topics, alias, multi),
		}
		for _, n := range []struct{ what, v string }{{"source namespace", s.Namespace}, {"source release", s.Release}} {
			if !labName.MatchString(n.v) || len(n.v) > 63 {
				return nil, fmt.Errorf("%s %q is not a DNS label", n.what, n.v)
			}
		}
		if spec.Provider == kafkaversion.ProviderLegacy {
			s.Image = kafkaversion.LegacyImageFor(spec.From, o.Registry)
			// The chart's fullname is <release>-legacy-kafka; its bootstrap
			// Service is <fullname>-bootstrap (templates/services.yaml).
			port := 9092
			if o.SASL {
				port = 9094
				s.SASL = true
				s.User = LegacySASLUser
				s.Secret = s.Release + "-" + LegacySASLUser
				s.Password = o.SourcePassword
				if s.Password == "" {
					pw, err := randomPassword()
					if err != nil {
						return nil, err
					}
					s.Password = pw
				}
			}
			s.Bootstrap = fmt.Sprintf("%s-legacy-kafka-bootstrap.%s.svc.%s:%d", s.Release, s.Namespace, ClusterDomain, port)
		} else {
			s.SASL = true // a Strimzi listener always authenticates
			s.User = TargetUser
			s.Secret = l.MirrorRelease + "-" + alias
			port := 9092
			if o.SourceTLS {
				port = 9093
				s.TLS = true
				s.CASecret = s.Cluster + "-cluster-ca-cert"
			}
			s.Bootstrap = fmt.Sprintf("%s-kafka-bootstrap.%s.svc.%s:%d", s.Cluster, s.Namespace, ClusterDomain, port)
		}
		sources = append(sources, s)
	}
	if err := refuseIdentityFanIn(o.Policy, sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// corpusTopics is one source's corpus: the topics the caller named, or the
// lab's own — which, in a fan-in, is one topic per source
// (kates.orders.src282) so that each leg is provable and no two legs write
// the same name onto the target.
func corpusTopics(topics []string, alias string, multi bool) []string {
	if len(topics) > 0 {
		return append([]string(nil), topics...)
	}
	if multi {
		return []string{DefaultTopic + "." + alias}
	}
	return []string{DefaultTopic}
}

// refuseIdentityFanIn refuses two sources that would write the same topic
// name onto the target — the chart's own rail, applied before anything
// touches a cluster and stated in the caller's own words (--from), because a
// Helm template error is a worse way to learn this.
func refuseIdentityFanIn(policy string, sources []*Source) error {
	if policy != PolicyIdentity || len(sources) < 2 {
		return nil
	}
	for i, a := range sources {
		for _, b := range sources[i+1:] {
			for _, t := range a.Topics {
				for _, u := range b.Topics {
					if t != u {
						continue
					}
					return fmt.Errorf("--from %s and --from %s would both mirror topic %q into the target under the identity policy, which keeps topic names: two mirrors writing one target topic interleave into one offset space, which is corruption rather than a migration.\n"+
						"  - keep the names apart:  --policy default  (each source's topics land as <alias>.<topic>)\n"+
						"  - or let the lab name them: drop --topics, and each source gets its own corpus (%s.%s, %s.%s)\n"+
						"  - or mirror one source per lab (--name tells them apart)",
						a.From, b.From, t, DefaultTopic, a.Alias, DefaultTopic, b.Alias)
				}
			}
		}
	}
	return nil
}

// DefaultName is the lab name for a pair: m<from digits>-<to digits>, so
// 2.8.2 → 4.3.0 is m282-430.
func DefaultName(from, to kafkaversion.Version) string {
	return DefaultNameFor([]kafkaversion.Version{from}, to)
}

// DefaultNameFor is the lab name for one target and every source of it:
// m<from digits>[-<from digits>…]-<to digits>, so 2.8.2 and 3.9.1 → 4.3.0
// is m282-391-430 and a single 2.8.2 → 4.3.0 stays m282-430.
func DefaultNameFor(froms []kafkaversion.Version, to kafkaversion.Version) string {
	strip := func(v kafkaversion.Version) string { return strings.ReplaceAll(v.String(), ".", "") }
	parts := make([]string, 0, len(froms)+1)
	for _, f := range froms {
		parts = append(parts, strip(f))
	}
	parts = append(parts, strip(to))
	return "m" + strings.Join(parts, "-")
}

// SourceAliasFor is the MirrorMaker 2 alias of a source in a lab with
// several of them: src plus the version's digits (2.8.2 → src282). A dot is
// MirrorMaker's own separator between alias and topic, so the version's dots
// go; the result is a DNS label, which is what Strimzi and the chart's
// per-source ACLs, NetworkPolicy, alerts and tests key on.
func SourceAliasFor(v kafkaversion.Version) string {
	return SourceAliasPrefix + strings.ReplaceAll(v.String(), ".", "")
}

// RoleLabels returns the lab's labels plus LabelRole=role, the label set a
// release of that role is installed with.
func (l *Lab) RoleLabels(role string) map[string]string {
	out := map[string]string{}
	for k, v := range l.Labels {
		out[k] = v
	}
	out[LabelRole] = role
	return out
}

// SourceLabels is the label set one source's release is installed with: the
// lab's labels, the source role, and — in a fan-in — the source's alias, so
// down and status can tell one leg's resources from another's.
func (l *Lab) SourceLabels(s *Source) map[string]string {
	out := l.RoleLabels(RoleSource)
	if l.FanIn() && s != nil && s.Alias != "" {
		out[LabelSourceAlias] = s.Alias
	}
	return out
}

// Selector is the label selector that finds every resource of the lab
// (`kubectl get … -l`, `helm list -l`).
func (l *Lab) Selector() string {
	return LabelLab + "=" + l.Name
}

// Sources returns every source of the lab in --from order. The first is
// rebuilt from the flat Source* fields each time, so a command that
// reassigned one of them — mirror deploy against a bootstrap address, verify
// against a real cluster — is described by what it set rather than by what
// New derived. Anything per-source walks this; nothing walks ExtraSources.
func (l *Lab) Sources() []*Source {
	topics := l.SourceTopics
	if len(topics) == 0 {
		topics = l.Topics
	}
	first := &Source{
		From: l.From, Provider: l.SourceProvider, Mode: l.SourceMode, StrimziVersion: l.SourceStrimziVersion,
		Alias: l.SourceAlias, Namespace: l.SourceNamespace, Cluster: l.SourceCluster, Release: l.SourceRelease,
		Image: l.SourceImage, Bootstrap: l.SourceBootstrap,
		User: l.SourceUser, Secret: l.SourceSecret, CASecret: l.SourceCASecret, Password: l.SourcePassword,
		SASL: l.SASL, TLS: l.SourceTLS, Topics: topics,
	}
	if first.Alias == "" {
		first.Alias = SourceAlias
	}
	out := make([]*Source, 0, 1+len(l.ExtraSources))
	out = append(out, first)
	return append(out, l.ExtraSources...)
}

// Source returns the lab's first source — the one every single-source
// command works with.
func (l *Lab) Source() *Source { return l.Sources()[0] }

// FanIn says the lab mirrors more than one source into its target.
func (l *Lab) FanIn() bool { return len(l.ExtraSources) > 0 }

// RowFor names a per-source assertion of the report: the row's own name in a
// single-source lab, and the name with the source's alias in a fan-in, so a
// stuck leg is named rather than averaged away.
func (l *Lab) RowFor(name string, s *Source) string {
	if !l.FanIn() || s == nil {
		return name
	}
	return name + " [" + s.Alias + "]"
}

// ReplicatedTopic is the name a source topic takes on the target under the
// lab's policy: unchanged under identity, <alias>.<topic> under default.
func (l *Lab) ReplicatedTopic(topic string) string {
	return l.ReplicatedTopicOf(l.Source(), topic)
}

// ReplicatedTopicOf is ReplicatedTopic for one source of the lab.
func (l *Lab) ReplicatedTopicOf(s *Source, topic string) string {
	if l.Policy == PolicyDefault {
		alias := SourceAlias
		if s != nil && s.Alias != "" {
			alias = s.Alias
		}
		return alias + "." + topic
	}
	return topic
}

// ReplicatedTopics lists the target-side name of every topic of every
// source, in source order.
func (l *Lab) ReplicatedTopics() []string {
	var out []string
	for _, s := range l.Sources() {
		for _, t := range s.Topics {
			out = append(out, l.ReplicatedTopicOf(s, t))
		}
	}
	return out
}

// SourceAuth is how the mirror authenticates to the first source, see the
// SourceAuth constants.
func (l *Lab) SourceAuth() SourceAuth { return l.Source().Auth() }

// SourceMechanism is the SASL mechanism of the first source's listener:
// PLAIN for the legacy chart, SCRAM-SHA-512 for a Strimzi cluster, empty
// without authentication.
func (l *Lab) SourceMechanism() string { return l.Source().Mechanism() }

// Describe is the one-line summary of the pair a report header or plan
// carries: "Kafka 2.8.2 (legacy, zookeeper) → 4.3.0 (krafter in kafka)".
// A fan-in names every source, joined by "+".
func (l *Lab) Describe() string {
	var sources []string
	for _, s := range l.Sources() {
		sources = append(sources, s.Describe())
	}
	return fmt.Sprintf("%s → %s (%s in %s)", strings.Join(sources, " + "), l.To, l.TargetCluster, l.TargetNamespace)
}

func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate a source password: %w", err)
	}
	return hex.EncodeToString(b), nil
}
