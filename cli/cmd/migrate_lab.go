package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// The lab is found on the cluster by its label, never by a state file: up
// stamps kates.io/lab=<name> and kates.io/lab-role on every release through
// the charts' extraLabels, so status, verify, cutover and down discover it
// from the resources — a KafkaMirrorMaker2, a legacy StatefulSet, a Kafka
// CR — and map them back to Helm releases through app.kubernetes.io/instance.

// labResource is one labelled resource of a lab.
type labResource struct {
	Kind, Name, Namespace string
	Release               string
	Role                  string
	Labels                map[string]string
}

// labSource is one source of a lab as the cluster describes it: the
// workload that runs it and the mirror leg that reads it.
type labSource struct {
	// Resource is the legacy broker StatefulSet or the source Kafka CR.
	Resource *labResource
	// Alias is the mirror's alias for it, from the CR (or the lab's label).
	Alias              string
	Release, Namespace string
	Provider           kafkaversion.Provider
	Version            kafkaversion.Version
	Mode               string
	Bootstrap          string
	// Topics are the topics this leg mirrors, read back out of the CR.
	Topics []string
	// Auth is how the mirror authenticates to it, from the CR's clusters.
	Auth mirrorAuth
	// NamespaceOwned says the namespace carries the lab's label, so the lab
	// created it and down may delete it.
	NamespaceOwned bool
}

// mirrorAuth is how the KafkaMirrorMaker2 CR authenticates to one cluster:
// Known when the CR lists the cluster at all, SASL when that entry carries a
// SASL authentication block, with its user and credential Secret.
type mirrorAuth struct {
	Known, SASL  bool
	User, Secret string
}

// usesSASL says the mirror reads this source over SASL: what the CR's
// authentication block says when the CR lists the cluster, else — a CR
// written before the clusters were read — the legacy chart's SASL port.
func (s *labSource) usesSASL() bool {
	if s.Auth.Known {
		return s.Auth.SASL
	}
	return strings.HasSuffix(s.Bootstrap, ":9094")
}

// labDiscovery is a lab as the cluster describes it.
type labDiscovery struct {
	Name string
	// Mirror is the KafkaMirrorMaker2 CR; MirrorRelease/MirrorNamespace
	// its Helm release.
	Mirror                         *labResource
	MirrorRelease, MirrorNamespace string
	MirrorCR                       string
	// Source is the legacy broker StatefulSet or the source Kafka CR of the
	// FIRST source; Sources is every source of the lab, in the order the
	// mirror's own spec lists them.
	Source                         *labResource
	Sources                        []*labSource
	SourceRelease, SourceNamespace string
	SourceProvider                 kafkaversion.Provider
	SourceVersion                  kafkaversion.Version
	SourceMode                     string
	SourceBootstrap                string
	SourceAlias                    string
	// Target is an additional target Kafka CR when the lab created one.
	Target *labResource
	// TargetCluster and TargetNamespace come from the mirror's spec.
	TargetCluster, TargetNamespace string
	TargetBootstrap                string
	// From the mirror's spec: the topics mirrored, the policy, the Connect
	// group.
	Topics  []string
	Policy  string
	GroupID string
	// SourceNamespaceOwned says the first source's namespace carries the
	// lab's label, so the lab created it and down may delete it.
	SourceNamespaceOwned bool
	// legs are the mirror's own entries, in CR order: what the sources are
	// matched against.
	legs []mirrorLeg
}

// mirrorLeg is one entry of the KafkaMirrorMaker2 spec: which source it
// reads, under which alias, and which topics it carries.
type mirrorLeg struct {
	Alias     string
	Bootstrap string
	Topics    []string
	Auth      mirrorAuth
	// Pattern is the leg's topicsPattern; PatternOnly says Topics does not
	// cover it (no recorded list, and the pattern holds real regex syntax),
	// so only the target's topic list can say which topics it mirrored.
	Pattern     string
	PatternOnly bool
}

// ReplicatedTopic is the lab's rule for the target-side name.
func (d *labDiscovery) ReplicatedTopic(topic string) string {
	return d.replicatedTopicOf(d.SourceAlias, topic)
}

func (d *labDiscovery) replicatedTopicOf(alias, topic string) string {
	if d.Policy == migrate.PolicyDefault && alias != "" {
		return alias + "." + topic
	}
	return topic
}

// ReplicatedTopics lists the target-side names of every source's mirrored
// topics, in source order.
func (d *labDiscovery) ReplicatedTopics() []string {
	var out []string
	for _, s := range d.Sources {
		for _, t := range s.Topics {
			out = append(out, d.replicatedTopicOf(s.Alias, t))
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, t := range d.Topics {
		out = append(out, d.ReplicatedTopic(t))
	}
	return out
}

// internalTopics lists the mirror's own topics on the target: the Connect
// group's storage topics and one checkpoints topic per source.
func (d *labDiscovery) internalTopics() []string {
	var out []string
	if d.GroupID != "" {
		out = append(out, d.GroupID+"-configs", d.GroupID+"-offsets", d.GroupID+"-status")
	}
	seen := map[string]bool{}
	for _, s := range d.Sources {
		if s.Alias != "" && !seen[s.Alias] {
			seen[s.Alias] = true
			out = append(out, s.Alias+".checkpoints.internal")
		}
	}
	if len(seen) == 0 && d.SourceAlias != "" {
		out = append(out, d.SourceAlias+".checkpoints.internal")
	}
	return out
}

// discoverLab finds a lab by name, or the single lab on the cluster when
// name is empty.
func discoverLab(ctx context.Context, name string) (*labDiscovery, error) {
	selector := migrate.LabelLab
	if name != "" {
		selector += "=" + name
	}
	out, err := defaultRunner.Run(ctx, "kubectl", "get", "kafkamirrormaker2,statefulset,kafka", "-A", "-l", selector, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("discover the lab's resources: %w", err)
	}
	items, err := parseLabelledItems(out)
	if err != nil {
		return nil, err
	}
	byLab := map[string][]labItem{}
	for _, it := range items {
		lab := it.Metadata.Labels[migrate.LabelLab]
		if lab == "" {
			continue
		}
		byLab[lab] = append(byLab[lab], it)
	}
	name, err = pickLabName(byLab, name)
	if err != nil {
		return nil, err
	}
	d := &labDiscovery{Name: name}
	var found []*labSource
	for _, it := range byLab[name] {
		if src := d.addItem(it); src != nil {
			found = append(found, src)
		}
	}
	d.Sources = orderLabSources(found, d.legs)
	for _, src := range d.Sources {
		if src.Namespace != "" {
			src.NamespaceOwned = namespaceHasLabel(ctx, src.Namespace, migrate.LabelLab, name)
		}
	}
	d.adoptFirstSource()
	return d, nil
}

// pickLabName settles which lab a discovery describes: the one named, which
// must exist, or the single lab on the cluster when name is empty.
func pickLabName(byLab map[string][]labItem, name string) (string, error) {
	if name == "" {
		switch len(byLab) {
		case 0:
			return "", errors.New("no migration lab found (no resource labelled " + migrate.LabelLab + ") — kates migrate up creates one")
		case 1:
			for n := range byLab {
				name = n
			}
		default:
			names := make([]string, 0, len(byLab))
			for n := range byLab {
				names = append(names, n)
			}
			sort.Strings(names)
			return "", fmt.Errorf("%d migration labs found (%s) — pass --name", len(byLab), strings.Join(names, ", "))
		}
	}
	if len(byLab[name]) == 0 {
		return "", fmt.Errorf("no migration lab named %q (no resource labelled %s=%s)", name, migrate.LabelLab, name)
	}
	return name, nil
}

// addItem records one labelled resource of the lab on d: the mirror and the
// target directly, a source workload as the returned labSource (nil for
// anything that is not one).
func (d *labDiscovery) addItem(it labItem) *labSource {
	res := &labResource{
		Kind: it.Kind, Name: it.Metadata.Name, Namespace: it.Metadata.Namespace,
		Release: it.Metadata.Labels["app.kubernetes.io/instance"],
		Role:    it.Metadata.Labels[migrate.LabelRole],
		Labels:  it.Metadata.Labels,
	}
	if res.Release == "" {
		res.Release = it.Metadata.Annotations["meta.helm.sh/release-name"]
	}
	switch strings.ToLower(it.Kind) {
	case "kafkamirrormaker2":
		d.Mirror = res
		d.MirrorRelease, d.MirrorNamespace, d.MirrorCR = res.Release, res.Namespace, res.Name
		d.readMirrorSpec(it.Spec, it.Metadata.Annotations[migrate.AnnotationLabTopics])
	case "statefulset":
		if it.Metadata.Labels["app.kubernetes.io/component"] != "broker" {
			return nil // the ZooKeeper StatefulSet carries the same labels
		}
		src := &labSource{
			Resource: res, Release: res.Release, Namespace: res.Namespace,
			Provider: kafkaversion.ProviderLegacy,
			Mode:     it.Metadata.Labels["kates.io/kafka-mode"],
			Alias:    it.Metadata.Labels[migrate.LabelSourceAlias],
		}
		if v, err := kafkaversion.Parse(it.Metadata.Labels["app.kubernetes.io/version"]); err == nil {
			src.Version = v
		}
		return src
	case "kafka":
		if res.Role == migrate.RoleTarget {
			d.Target = res
			return nil
		}
		src := &labSource{
			Resource: res, Release: res.Release, Namespace: res.Namespace,
			Provider: kafkaversion.ProviderStrimzi,
			Alias:    it.Metadata.Labels[migrate.LabelSourceAlias],
		}
		if v, err := kafkaversion.Parse(specKafkaVersion(it.Spec)); err == nil {
			src.Version = v
		}
		return src
	}
	return nil
}

// adoptFirstSource copies the first ordered source onto the discovery's
// single-source fields, which the one-source commands read.
func (d *labDiscovery) adoptFirstSource() {
	if len(d.Sources) == 0 {
		return
	}
	first := d.Sources[0]
	d.Source = first.Resource
	d.SourceRelease, d.SourceNamespace = first.Release, first.Namespace
	d.SourceProvider, d.SourceVersion, d.SourceMode = first.Provider, first.Version, first.Mode
	if first.Bootstrap != "" {
		d.SourceBootstrap = first.Bootstrap
	}
	if first.Alias != "" {
		d.SourceAlias = first.Alias
	}
	d.SourceNamespaceOwned = first.NamespaceOwned
}

// orderLabSources matches the source workloads to the mirror's legs — by
// alias when the resources carry one, else by the bootstrap address the leg
// names — and returns them in the CR's own order, which is the order the
// lab was created with. A workload no leg claims keeps its place at the end,
// so a half-removed lab is still fully described.
func orderLabSources(found []*labSource, legs []mirrorLeg) []*labSource {
	if len(found) == 0 {
		return nil
	}
	claimed := map[*labSource]bool{}
	var out []*labSource
	for _, leg := range legs {
		var match *labSource
		for _, src := range found {
			if claimed[src] {
				continue
			}
			if (src.Alias != "" && src.Alias == leg.Alias) || legNamesSource(leg.Bootstrap, src) {
				match = src
				break
			}
		}
		if match == nil {
			continue
		}
		claimed[match] = true
		match.Alias, match.Bootstrap, match.Topics, match.Auth = leg.Alias, leg.Bootstrap, leg.Topics, leg.Auth
		out = append(out, match)
	}
	for _, src := range found {
		if !claimed[src] {
			out = append(out, src)
		}
	}
	// One source and one leg that did not match by name still belong
	// together: the leg is what verify and down work from.
	if len(out) == 1 && len(legs) == 1 && out[0].Bootstrap == "" {
		out[0].Alias, out[0].Bootstrap, out[0].Topics, out[0].Auth = legs[0].Alias, legs[0].Bootstrap, legs[0].Topics, legs[0].Auth
	}
	return out
}

// legNamesSource says a mirror leg's bootstrap address points at a source's
// release in its namespace (<release>-…-bootstrap.<namespace>.svc…).
func legNamesSource(bootstrap string, src *labSource) bool {
	if bootstrap == "" || src.Release == "" || src.Namespace == "" {
		return false
	}
	host := bootstrap
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	parts := strings.SplitN(host, ".", 3)
	if len(parts) < 2 || parts[1] != src.Namespace {
		return false
	}
	return strings.HasPrefix(parts[0], src.Release+"-")
}

// labItem is a labelled resource as kubectl lists it.
type labItem struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec json.RawMessage `json:"spec"`
}

func parseLabelledItems(listJSON string) ([]labItem, error) {
	if strings.TrimSpace(listJSON) == "" {
		return nil, nil
	}
	var list struct {
		Items []labItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		return nil, fmt.Errorf("parse the lab's resources: %w", err)
	}
	return list.Items, nil
}

// readMirrorSpec reads what down and status need from the
// KafkaMirrorMaker2 spec: the target, the source, the topics, the policy,
// and how the CR authenticates to each cluster. labTopics is the CR's
// AnnotationLabTopics value — the topic list up recorded — and wins over the
// names read out of topicsPattern, which a real regular expression hides.
func (d *labDiscovery) readMirrorSpec(spec json.RawMessage, labTopics string) {
	var s struct {
		Target struct {
			Alias            string `json:"alias"`
			BootstrapServers string `json:"bootstrapServers"`
			GroupID          string `json:"groupId"`
		} `json:"target"`
		Clusters []struct {
			Alias          string `json:"alias"`
			Authentication *struct {
				Type           string `json:"type"`
				Username       string `json:"username"`
				PasswordSecret struct {
					SecretName string `json:"secretName"`
				} `json:"passwordSecret"`
			} `json:"authentication"`
		} `json:"clusters"`
		Mirrors []struct {
			Source struct {
				Alias            string `json:"alias"`
				BootstrapServers string `json:"bootstrapServers"`
			} `json:"source"`
			TopicsPattern   string `json:"topicsPattern"`
			SourceConnector struct {
				Config map[string]any `json:"config"`
			} `json:"sourceConnector"`
		} `json:"mirrors"`
	}
	if err := json.Unmarshal(spec, &s); err != nil {
		return
	}
	d.GroupID = s.Target.GroupID
	d.TargetBootstrap = s.Target.BootstrapServers
	d.TargetCluster, d.TargetNamespace = clusterFromBootstrap(s.Target.BootstrapServers)
	if len(s.Mirrors) == 0 {
		return
	}
	auth := map[string]mirrorAuth{}
	for _, c := range s.Clusters {
		a := mirrorAuth{Known: true}
		if c.Authentication != nil && c.Authentication.Type != "" && c.Authentication.Type != "tls" {
			a.SASL, a.User, a.Secret = true, c.Authentication.Username, c.Authentication.PasswordSecret.SecretName
		}
		auth[c.Alias] = a
	}
	recorded := splitTopics(labTopics)
	for _, m := range s.Mirrors {
		topics, patternOnly := legTopics(recorded, m.TopicsPattern, len(s.Mirrors))
		leg := mirrorLeg{
			Alias:       m.Source.Alias,
			Bootstrap:   m.Source.BootstrapServers,
			Topics:      topics,
			Auth:        auth[m.Source.Alias],
			Pattern:     m.TopicsPattern,
			PatternOnly: patternOnly,
		}
		d.legs = append(d.legs, leg)
		d.Topics = append(d.Topics, leg.Topics...)
	}
	if len(recorded) > 0 {
		d.Topics = recorded
	}
	first := s.Mirrors[0]
	d.SourceAlias = first.Source.Alias
	d.SourceBootstrap = first.Source.BootstrapServers
	d.Policy = migrate.PolicyDefault
	if policy, _ := first.SourceConnector.Config["replication.policy.class"].(string); strings.Contains(policy, "IdentityReplicationPolicy") {
		d.Policy = migrate.PolicyIdentity
	}
}

// legTopics is one mirror leg's topics: the recorded list when the lab has
// one leg; in a fan-in, the recorded names the leg's pattern matches, since
// each leg carries its own. Without a recorded list, or when the pattern
// claims none of it, the names are read out of the pattern itself, and
// patternOnly says whether the pattern holds more than those names.
func legTopics(recorded []string, pattern string, legs int) (topics []string, patternOnly bool) {
	fromPattern := func() ([]string, bool) { return topicsFromPattern(pattern), !literalPattern(pattern) }
	if len(recorded) == 0 {
		return fromPattern()
	}
	if legs == 1 {
		return recorded, false
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return fromPattern()
	}
	var out []string
	for _, t := range recorded {
		if re.MatchString(t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return fromPattern()
	}
	return out, false
}

// clusterFromBootstrap reads a Strimzi bootstrap address back into the
// cluster name and namespace: <cluster>-kafka-bootstrap.<ns>.svc…:port.
func clusterFromBootstrap(bootstrap string) (cluster, namespace string) {
	host := bootstrap
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 || !strings.HasSuffix(parts[0], "-kafka-bootstrap") {
		return "", ""
	}
	return strings.TrimSuffix(parts[0], "-kafka-bootstrap"), parts[1]
}

func specKafkaVersion(spec json.RawMessage) string {
	var s struct {
		Kafka struct {
			Version string `json:"version"`
		} `json:"kafka"`
	}
	_ = json.Unmarshal(spec, &s)
	return s.Kafka.Version
}

// namespaceHasLabel reports whether a namespace carries label=value.
func namespaceHasLabel(ctx context.Context, namespace, label, value string) bool {
	out, err := defaultRunner.Run(ctx, "kubectl", "get", "namespace", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.labels."+jsonpathKey(label)+"}")
	return err == nil && strings.TrimSpace(out) == value
}

// jsonpathKey escapes the dots of a label key for a jsonpath expression.
func jsonpathKey(k string) string {
	return strings.ReplaceAll(k, ".", `\.`)
}

// discoveryFromLab builds the discovery of a lab the CLI created itself
// (run knows its lab without asking the cluster). owned says, per source
// namespace, whether the lab created it and down may delete it.
func discoveryFromLab(l *migrate.Lab, owned func(namespace string) bool) *labDiscovery {
	d := &labDiscovery{
		Name:            l.Name,
		MirrorRelease:   l.MirrorRelease,
		MirrorNamespace: l.MirrorNamespace,
		MirrorCR:        l.MirrorCR,
		SourceRelease:   l.SourceRelease,
		SourceNamespace: l.SourceNamespace,
		SourceProvider:  l.SourceProvider,
		SourceVersion:   l.From,
		SourceMode:      string(l.SourceMode),
		SourceBootstrap: l.SourceBootstrap,
		SourceAlias:     l.SourceAlias,
		TargetCluster:   l.TargetCluster,
		TargetNamespace: l.TargetNamespace,
		Topics:          l.Topics,
		Policy:          l.Policy,
		GroupID:         l.GroupID,
	}
	d.Mirror = &labResource{Kind: "KafkaMirrorMaker2", Name: l.MirrorCR, Namespace: l.MirrorNamespace, Release: l.MirrorRelease, Role: migrate.RoleMirror}
	for _, src := range l.Sources() {
		res := &labResource{Kind: "Kafka", Name: src.Cluster, Namespace: src.Namespace, Release: src.Release, Role: migrate.RoleSource}
		if src.Provider == kafkaversion.ProviderLegacy {
			res = &labResource{Kind: "StatefulSet", Name: src.Release + "-legacy-kafka", Namespace: src.Namespace, Release: src.Release, Role: migrate.RoleSource}
		}
		d.Sources = append(d.Sources, &labSource{
			Resource: res, Alias: src.Alias, Release: src.Release, Namespace: src.Namespace,
			Provider: src.Provider, Version: src.From, Mode: string(src.Mode), Bootstrap: src.Bootstrap,
			Topics: src.Topics, NamespaceOwned: owned != nil && owned(src.Namespace),
		})
	}
	d.Source = d.Sources[0].Resource
	d.SourceNamespaceOwned = d.Sources[0].NamespaceOwned
	return d
}

// labFromDiscovery rebuilds the lab a discovery describes, so verify,
// cutover and status can reuse the same client pods and tools as run. The
// target version comes from the environment.
func labFromDiscovery(d *labDiscovery, env *migrateEnv) (*migrate.Lab, error) {
	if d.SourceVersion == (kafkaversion.Version{}) {
		return nil, fmt.Errorf("lab %s: the source's Kafka version is unknown (no app.kubernetes.io/version on its StatefulSet)", d.Name)
	}
	provider := d.SourceProvider
	if provider == "" {
		provider = kafkaversion.ProviderLegacy
	}
	opts, err := labOptionsFromDiscovery(d, env, provider)
	if err != nil {
		return nil, err
	}
	l, err := migrate.New(opts)
	if err != nil {
		return nil, fmt.Errorf("lab %s: %w", d.Name, err)
	}
	l.SourcePassword = ""
	applyDiscoveredSource(l, d, provider)
	applyDiscoveredExtras(l, d)
	l.Topics = nil
	for _, s := range l.Sources() {
		l.Topics = append(l.Topics, s.Topics...)
	}
	return l, nil
}

// firstLabSource is the discovery's first ordered source, nil when it has
// none.
func (d *labDiscovery) firstLabSource() *labSource {
	if len(d.Sources) > 0 {
		return d.Sources[0]
	}
	return nil
}

// labOptionsFromDiscovery is the migrate.Options New rebuilds a discovered
// lab from.
func labOptionsFromDiscovery(d *labDiscovery, env *migrateEnv, provider kafkaversion.Provider) (migrate.Options, error) {
	opts := migrate.Options{
		Name:            d.Name,
		From:            d.SourceVersion,
		To:              env.Target.Version,
		SourceProvider:  provider,
		TargetIsPrimary: true,
		TargetCluster:   env.Target.Cluster,
		TargetNamespace: env.Target.Namespace,
		Policy:          d.Policy,
		Topics:          d.Topics,
		StrimziVersion:  env.StrimziVersion,
		Scope:           env.Scope,
	}
	// A fan-in: every further source is its own spec, and the corpus is not
	// the lab's to derive — each leg's topics come back off the CR below.
	for _, src := range d.Sources[1:] {
		if src.Version == (kafkaversion.Version{}) {
			return migrate.Options{}, fmt.Errorf("lab %s: the Kafka version of source %s is unknown (no app.kubernetes.io/version on its StatefulSet)", d.Name, src.Alias)
		}
		spec := migrate.SourceSpec{From: src.Version, Provider: src.Provider}
		if spec.Provider == "" {
			spec.Provider = kafkaversion.ProviderLegacy
		}
		opts.AlsoFrom = append(opts.AlsoFrom, spec)
	}
	if len(opts.AlsoFrom) > 0 {
		opts.Topics = nil
	}
	if d.TargetCluster != "" && d.TargetNamespace != "" {
		opts.TargetCluster, opts.TargetNamespace = d.TargetCluster, d.TargetNamespace
	}
	// The CR's authentication block for the source says whether the mirror
	// reads it over SASL; a CR that lists no clusters is judged by the legacy
	// chart's SASL port, 9094.
	first := d.firstLabSource()
	sourceSASL := strings.HasSuffix(d.SourceBootstrap, ":9094")
	if first != nil && first.Auth.Known {
		sourceSASL = first.Auth.SASL
	}
	if provider == kafkaversion.ProviderLegacy && sourceSASL {
		opts.SASL = true
		opts.SourcePassword = "existing" // the Secret exists; the lab never rewrites it
	}
	opts.ImageMirror = imageRegistry()
	return opts, nil
}

// applyDiscoveredSource overwrites what New derived for the first source
// and the mirror with what the cluster says they are.
func applyDiscoveredSource(l *migrate.Lab, d *labDiscovery, provider kafkaversion.Provider) {
	if d.SourceRelease != "" {
		l.SourceRelease = d.SourceRelease
		l.SourceCluster = d.SourceRelease
		if l.SASL && provider == kafkaversion.ProviderLegacy {
			l.SourceSecret = l.SourceRelease + "-" + migrate.LegacySASLUser
		}
	}
	// The credential the CR really uses, when it names one.
	first := d.firstLabSource()
	if first != nil && first.Auth.Secret != "" {
		l.SourceSecret = first.Auth.Secret
		if first.Auth.User != "" {
			l.SourceUser = first.Auth.User
		}
	}
	if d.SourceNamespace != "" {
		l.SourceNamespace = d.SourceNamespace
	}
	if d.SourceBootstrap != "" {
		l.SourceBootstrap = d.SourceBootstrap
	}
	if d.MirrorRelease != "" {
		l.MirrorRelease, l.MirrorCR = d.MirrorRelease, d.MirrorCR
		l.MirrorNamespace = d.MirrorNamespace
	}
	if d.GroupID != "" {
		l.GroupID = d.GroupID
	}
	if d.SourceAlias != "" {
		l.SourceAlias = d.SourceAlias
	}
	if first != nil && len(first.Topics) > 0 {
		l.SourceTopics = first.Topics
	}
}

// applyDiscoveredExtras makes the lab's further sources what the cluster
// says they are, not what New derived: the cluster is the record.
func applyDiscoveredExtras(l *migrate.Lab, d *labDiscovery) {
	for i, extra := range l.ExtraSources {
		if i+1 >= len(d.Sources) {
			break
		}
		applyDiscoveredExtra(extra, d.Sources[i+1])
	}
}

func applyDiscoveredExtra(extra *migrate.Source, src *labSource) {
	if src.Alias != "" {
		extra.Alias = src.Alias
	}
	if src.Release != "" {
		extra.Release, extra.Cluster = src.Release, src.Release
		if extra.SASL && extra.Provider == kafkaversion.ProviderLegacy {
			extra.Secret = extra.Release + "-" + migrate.LegacySASLUser
		}
	}
	if src.Namespace != "" {
		extra.Namespace = src.Namespace
	}
	if src.Bootstrap != "" {
		extra.Bootstrap = src.Bootstrap
		extra.SASL = src.usesSASL()
		if extra.SASL && extra.Provider == kafkaversion.ProviderLegacy {
			extra.User = migrate.LegacySASLUser
			extra.Secret = extra.Release + "-" + migrate.LegacySASLUser
		}
		if src.Auth.Secret != "" {
			extra.User, extra.Secret = src.Auth.User, src.Auth.Secret
		}
	}
	if len(src.Topics) > 0 {
		extra.Topics = src.Topics
	}
	extra.Password = ""
}

// ── status ───────────────────────────────────────────────────────────────

var (
	migrateStatusName    string
	migrateStatusWatch   bool
	migrateStatusOffsets bool
	migrateStatusTarget  migratePairFlags

	migrateStatusCmd = &cobra.Command{
		Use:   "status [--name <lab>] [--watch]",
		Short: "The lab as the cluster sees it: source, target, mirror CR, connectors, end offsets",
		Long: `Finds the lab by its kates.io/lab label and prints its source, its target,
the KafkaMirrorMaker2 CR's readiness and every connector's state, and —
best effort, through a client pod on each side — the end offsets of the
mirrored topics on both ends. --watch repeats every 10 seconds without the
end offsets, and says so on every frame.`,
		Example: `  kates migrate status
  kates migrate status --name m282-430 --watch
  kates migrate status -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateStatus(cmd.Context())
		},
	}
)

func init() {
	migrateStatusCmd.Flags().StringVar(&migrateStatusName, "name", "", "Lab name (default: the single lab on the cluster)")
	migrateStatusCmd.Flags().BoolVarP(&migrateStatusWatch, "watch", "w", false, "Repeat every 10 seconds")
	migrateStatusCmd.Flags().BoolVar(&migrateStatusOffsets, "offsets", true, "Read the end offsets on both ends through client pods (never under --watch)")
	migrateStatusCmd.Flags().StringVar(&migrateStatusTarget.TargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Name of the target Kafka")
	migrateStatusCmd.Flags().StringVar(&migrateStatusTarget.TargetNamespace, "target-namespace", migrate.DefaultTargetNamespace, "Namespace of the target Kafka")
	migrateStatusCmd.Flags().IntVar(&migrateStatusTarget.Timeout, "timeout", migrateDefaultTimeout, "Client pod readiness budget in seconds")
	migrateCmd.AddCommand(migrateStatusCmd)
}

// labStatus is the status document.
type labStatus struct {
	Lab string `json:"lab"`
	// Source is the first source, Sources every one of them.
	Source  labStatusSide      `json:"source"`
	Sources []labStatusSide    `json:"sources"`
	Target  labStatusSide      `json:"target"`
	Mirror  labStatusMirror    `json:"mirror"`
	Offsets []labStatusOffsets `json:"offsets,omitempty"`
	Notes   []string           `json:"notes,omitempty"`
}

type labStatusSide struct {
	Alias     string `json:"alias,omitempty"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Version   string `json:"version,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Bootstrap string `json:"bootstrap,omitempty"`
	Ready     string `json:"ready,omitempty"`
}

type labStatusMirror struct {
	Release    string                    `json:"release"`
	Namespace  string                    `json:"namespace"`
	CR         string                    `json:"cr"`
	Ready      bool                      `json:"ready"`
	Reason     string                    `json:"reason,omitempty"`
	Message    string                    `json:"message,omitempty"`
	Policy     string                    `json:"policy"`
	Topics     []string                  `json:"topics"`
	Connectors []migrate.ConnectorStatus `json:"connectors"`
	Generation string                    `json:"generation"`
}

type labStatusOffsets struct {
	Topic      string `json:"topic"`
	Replicated string `json:"replicated"`
	Source     string `json:"sourceEndOffsets"`
	Target     string `json:"targetEndOffsets"`
}

// watchOffsetsNote is what status --watch says in place of the end offsets
// it does not read.
const watchOffsetsNote = "end offsets are off under --watch (each reading starts a client pod on every end); run kates migrate status without --watch to read them"

// runMigrateStatus is `kates migrate status`.
func runMigrateStatus(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		st, err := collectLabStatus(ctx, migrateStatusName, migrateStatusOffsets && !migrateStatusWatch)
		if err != nil {
			return err
		}
		if migrateStatusWatch && migrateStatusOffsets {
			// Each reading starts a client pod on every end; every ten
			// seconds that is churn, not status. Say so on every frame.
			st.Notes = append(st.Notes, watchOffsetsNote)
		}
		if migrateStatusWatch && outputMode != "json" {
			output.ClearFrame(IsInteractive())
		}
		printLabStatus(st)
		if !migrateStatusWatch {
			return nil
		}
		if err := migrateSleep(ctx, 10*time.Second); err != nil {
			return nil
		}
	}
}

// collectLabStatus gathers the status document.
func collectLabStatus(ctx context.Context, name string, offsets bool) (*labStatus, error) {
	d, err := discoverLab(ctx, name)
	if err != nil {
		return nil, err
	}
	st := &labStatus{
		Lab:    d.Name,
		Target: labStatusSide{Cluster: d.TargetCluster, Namespace: d.TargetNamespace, Bootstrap: d.TargetBootstrap, Provider: string(kafkaversion.ProviderStrimzi)},
		Mirror: labStatusMirror{Release: d.MirrorRelease, Namespace: d.MirrorNamespace, CR: d.MirrorCR, Policy: d.Policy, Topics: d.Topics},
	}
	for _, src := range d.Sources {
		side := labStatusSide{
			Alias: src.Alias, Cluster: src.Release, Namespace: src.Namespace,
			Provider: string(src.Provider), Mode: src.Mode, Bootstrap: src.Bootstrap,
		}
		if src.Version != (kafkaversion.Version{}) {
			side.Version = src.Version.String()
		}
		if src.Resource != nil && src.Provider == kafkaversion.ProviderLegacy {
			if ready, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "get", "statefulset", src.Resource.Name, "-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}"); err == nil {
				side.Ready = strings.TrimSpace(ready) + " ready"
			}
		}
		st.Sources = append(st.Sources, side)
	}
	if len(st.Sources) > 0 {
		st.Source = st.Sources[0]
	}
	if d.TargetCluster != "" {
		if out, err := defaultRunner.Run(ctx, "kubectl", "-n", d.TargetNamespace, "get", "kafka", d.TargetCluster, "-o", "jsonpath={.spec.kafka.version} {.status.conditions[?(@.type==\"Ready\")].status}"); err == nil {
			fields := strings.Fields(out)
			if len(fields) > 0 {
				st.Target.Version = fields[0]
			}
			if len(fields) > 1 {
				st.Target.Ready = "Ready=" + fields[1]
			}
		}
	}
	if d.Mirror != nil {
		ms, err := readMirrorStatus(ctx, d.MirrorNamespace, d.MirrorCR)
		if err != nil {
			st.Notes = append(st.Notes, "mirror status: "+err.Error())
		} else {
			st.Mirror.Ready, st.Mirror.Reason, st.Mirror.Message = ms.Ready, ms.Reason, ms.Message
			st.Mirror.Connectors = ms.Connectors
			st.Mirror.Generation = fmt.Sprintf("%d/%d observed", ms.ObservedGeneration, ms.Generation)
		}
	} else {
		st.Notes = append(st.Notes, "no KafkaMirrorMaker2 carries the lab's label")
	}
	if st.Mirror.Connectors == nil {
		st.Mirror.Connectors = []migrate.ConnectorStatus{}
	}
	if offsets && d.Mirror != nil && len(d.Topics) > 0 {
		st.Offsets, err = readLabOffsets(ctx, d)
		if err != nil {
			st.Notes = append(st.Notes, "end offsets: "+err.Error())
		}
	}
	return st, nil
}

// readLabOffsets reads the end offsets of every mirrored topic on both ends
// through the lab's client pods, best effort.
func readLabOffsets(ctx context.Context, d *labDiscovery) ([]labStatusOffsets, error) {
	env, err := discoverMigrateEnv(ctx, d.TargetCluster, d.TargetNamespace, false)
	if err != nil {
		return nil, err
	}
	l, err := labFromDiscovery(d, env)
	if err != nil {
		return nil, err
	}
	r := &labRun{lab: l, env: env, state: migrate.NewState(l, nil), timeout: time.Duration(migrateStatusTarget.Timeout) * time.Second}
	if r.timeout <= 0 {
		r.timeout = migrateDefaultTimeout * time.Second
	}
	if err := r.ensureClients(ctx); err != nil {
		return nil, err
	}
	defer r.deleteClients(context.Background())
	var out []labStatusOffsets
	for _, sc := range r.clients.sources {
		for _, t := range sc.src.Topics {
			replicated := l.ReplicatedTopicOf(sc.src, t)
			out = append(out, labStatusOffsets{
				Topic:      t,
				Replicated: replicated,
				Source:     r.sourceEndOffsets(ctx, sc, t).String(),
				Target:     r.targetEndOffsets(ctx, replicated).String(),
			})
		}
	}
	return out, nil
}

// printLabStatus prints the status document.
func printLabStatus(st *labStatus) {
	if outputMode == "json" {
		output.JSON(st)
		return
	}
	output.Header("Migration lab " + st.Lab)
	for _, src := range st.Sources {
		title := "source"
		if len(st.Sources) > 1 && src.Alias != "" {
			title += " " + src.Alias
		}
		output.SubHeader(title)
		output.KeyValue("cluster", src.Cluster+" in "+src.Namespace)
		if src.Version != "" {
			output.KeyValue("version", fmt.Sprintf("%s (%s %s)", src.Version, src.Provider, src.Mode))
		}
		if src.Bootstrap != "" {
			output.KeyValue("bootstrap", src.Bootstrap)
		}
		if src.Ready != "" {
			output.KeyValue("brokers", src.Ready)
		}
	}
	output.SubHeader("target")
	output.KeyValue("cluster", st.Target.Cluster+" in "+st.Target.Namespace)
	if st.Target.Version != "" {
		output.KeyValue("version", st.Target.Version+"  "+st.Target.Ready)
	}
	output.SubHeader("mirror")
	output.KeyValue("release", st.Mirror.Release+" in "+st.Mirror.Namespace)
	output.KeyValue("CR", st.Mirror.CR)
	ready := "not Ready"
	if st.Mirror.Ready {
		ready = "Ready"
	}
	if st.Mirror.Reason != "" {
		ready += " — " + st.Mirror.Reason + ": " + st.Mirror.Message
	}
	output.KeyValue("state", ready+"  (generation "+st.Mirror.Generation+")")
	output.KeyValue("policy", st.Mirror.Policy)
	output.KeyValue("topics", strings.Join(st.Mirror.Topics, ", "))
	if len(st.Mirror.Connectors) > 0 {
		rows := make([][]string, 0, len(st.Mirror.Connectors))
		for _, c := range st.Mirror.Connectors {
			rows = append(rows, []string{c.Name, c.State, strings.Join(c.Tasks, ",")})
		}
		fmt.Fprintln(output.Out)
		output.Table([]string{"CONNECTOR", "STATE", "TASKS"}, rows)
	}
	if len(st.Offsets) > 0 {
		rows := make([][]string, 0, len(st.Offsets))
		for _, o := range st.Offsets {
			rows = append(rows, []string{o.Topic, o.Source, o.Replicated, o.Target})
		}
		output.Table([]string{"SOURCE TOPIC", "END OFFSETS", "TARGET TOPIC", "END OFFSETS"}, rows)
	}
	for _, n := range st.Notes {
		migrateHint(n)
	}
}

// ── down ─────────────────────────────────────────────────────────────────

var (
	migrateDownName       string
	migrateDownYes        bool
	migrateDownTimeout    int
	migrateDownKeepTopics bool
	migrateDownDeleteData bool

	migrateDownCmd = &cobra.Command{
		Use:   "down [--name <lab>] [--yes] [--keep-topics | --delete-data]",
		Short: "Remove the lab: the mirror, the source, the lab's topics on the target — found by label",
		Long: `Finds the lab by its kates.io/lab label and removes exactly what up created:
the mirror release and its kept CR, the source release, the source namespace
when the lab created it (it carries the label), the lab's topics on the
target (the mirrored topics and the mirror's own), the credential Secrets and
the client pods. Nothing without the label is touched, and a topic of the
mirror's own that another mirror on the target also writes, such as the
source.checkpoints.internal every single-source lab shares, is kept.

The mirrored topics are data. Before deleting them, down reads their end
offsets on the target as kates-mm2, from a client pod it removes afterwards.
A topic that holds records, whose offsets cannot be read, or that only the
mirror's topicsPattern names (a regular expression, resolved against the
target's topic list) needs a second confirmation naming it; without a
terminal, or with --yes or -o json, down refuses unless --delete-data is
also given. --keep-topics removes the lab and leaves its mirrored topics
in place (the mirror's own internal topics still go).`,
		Example: `  kates migrate down --name m282-430 --yes
  kates migrate down --name m282-430 --yes --keep-topics
  kates migrate down --name m282-430 --yes --delete-data
  kates migrate down`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateDown(cmd.Context())
		},
	}
)

func init() {
	migrateDownCmd.Flags().StringVar(&migrateDownName, "name", "", "Lab name (default: the single lab on the cluster)")
	migrateDownCmd.Flags().BoolVarP(&migrateDownYes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	migrateDownCmd.Flags().IntVar(&migrateDownTimeout, "timeout", migrateDefaultTimeout, "Deletion budget in seconds")
	migrateDownCmd.Flags().BoolVar(&migrateDownKeepTopics, "keep-topics", false, "Remove the lab but leave its mirrored topics on the target")
	migrateDownCmd.Flags().BoolVar(&migrateDownDeleteData, "delete-data", false, "Delete mirrored topics that hold records (or cannot be checked) without a second prompt")
	migrateDownCmd.MarkFlagsMutuallyExclusive("keep-topics", "delete-data")
	migrateCmd.AddCommand(migrateDownCmd)
}

// runMigrateDown is `kates migrate down`.
func runMigrateDown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if migrateDownKeepTopics && migrateDownDeleteData {
		return errors.New("--keep-topics and --delete-data contradict each other: pass one")
	}
	d, err := discoverLab(ctx, migrateDownName)
	if err != nil {
		return err
	}
	output.SubHeader("Lab " + d.Name + " — to be removed")
	for _, line := range describeLabTeardown(d, migrateDownKeepTopics) {
		migrateHint(line)
	}
	if err := migrateConfirm(migrateDownYes, fmt.Sprintf("Remove lab %s?", d.Name)); err != nil {
		return err
	}
	if !migrateDownKeepTopics {
		if err := guardLabTopics(ctx, d, time.Duration(migrateDownTimeout)*time.Second, migrateDownYes, migrateDownDeleteData); err != nil {
			return err
		}
	}
	if err := labDown(ctx, d, time.Duration(migrateDownTimeout)*time.Second, migrateDownKeepTopics); err != nil {
		return err
	}
	if migrateDownKeepTopics {
		if kept := d.ReplicatedTopics(); len(kept) > 0 {
			migrateHint(fmt.Sprintf("--keep-topics: %s left on %s in %s", strings.Join(kept, ", "), d.TargetCluster, d.TargetNamespace))
		}
	}
	migrateSuccess("lab " + d.Name + " removed")
	return nil
}

// describeLabTeardown lists what down will remove.
func describeLabTeardown(d *labDiscovery, keepTopics bool) []string {
	var lines []string
	if d.MirrorRelease != "" {
		lines = append(lines, fmt.Sprintf("mirror release %s in %s (and the kept CR %s)", d.MirrorRelease, d.MirrorNamespace, d.MirrorCR))
	}
	for _, src := range d.Sources {
		if src.Release == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("source release %s in %s", src.Release, src.Namespace))
		if src.NamespaceOwned {
			lines = append(lines, fmt.Sprintf("namespace %s (created by the lab)", src.Namespace))
		} else if src.Namespace != "" {
			lines = append(lines, fmt.Sprintf("namespace %s is left in place (not created by the lab)", src.Namespace))
		}
	}
	if d.Target != nil {
		lines = append(lines, fmt.Sprintf("additional target %s in %s", d.Target.Name, d.Target.Namespace))
	}
	if d.TargetNamespace != "" {
		mirrored := d.ReplicatedTopics()
		switch {
		case keepTopics && len(mirrored) > 0:
			lines = append(lines, fmt.Sprintf("mirrored topics on %s are kept (--keep-topics): %s", d.TargetCluster, strings.Join(mirrored, ", ")))
		case len(mirrored) > 0:
			lines = append(lines, fmt.Sprintf("mirrored topics on %s: %s (a topic that holds records needs a second confirmation)", d.TargetCluster, strings.Join(mirrored, ", ")))
		}
		if !keepTopics {
			for _, p := range d.patternLegs() {
				lines = append(lines, fmt.Sprintf("the topics on %s that topicsPattern %q matches (no recorded topic list: resolved against the target's topics, then confirmed)", d.TargetCluster, p.Pattern))
			}
		}
		if internal := d.internalTopics(); len(internal) > 0 {
			lines = append(lines, fmt.Sprintf("the mirror's own topics on %s: %s (one another mirror there also writes is kept)", d.TargetCluster, strings.Join(internal, ", ")))
		}
	}
	if d.MirrorNamespace != "" {
		lines = append(lines, fmt.Sprintf("secrets and client pods labelled %s=%s", migrate.LabelLab, d.Name))
	}
	return lines
}

// labDown removes a lab: the mirror (release, then the kept CR), the source
// release, the source namespace when the lab created it, the lab's topics on
// the target through KafkaTopic resources, the lab's Secrets and client pods.
// Every step is by name or by label; nothing else is touched. keepTopics
// leaves the mirrored topics in place; the mirror's own still go.
func labDown(ctx context.Context, d *labDiscovery, timeout time.Duration, keepTopics bool) error {
	var problems []string
	note := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	labelled := d.Name != "" // an unnamed source has no label to select by

	// Client pods first: they hold nothing and would block a namespace.
	if labelled {
		if _, err := defaultRunner.Run(ctx, "kubectl", "delete", "pod", "-A", "-l", migrate.LabelLab+"="+d.Name, "--ignore-not-found", "--wait=false"); err != nil {
			note("client pods: %v", err)
		}
	}

	if d.MirrorRelease != "" {
		if _, err := defaultRunner.Run(ctx, "helm", "uninstall", d.MirrorRelease, "-n", d.MirrorNamespace); err != nil && !isNotFound(err) {
			note("uninstall %s: %v", d.MirrorRelease, err)
		}
		// keepOnDelete annotates the CR resource-policy: keep, so uninstall
		// alone leaves the mirror running — right in production, wrong for a
		// lab that must not leak into the next run.
		if _, err := defaultRunner.Run(ctx, "kubectl", "-n", d.MirrorNamespace, "delete", "kafkamirrormaker2", d.MirrorCR, "--ignore-not-found", "--timeout=60s"); err != nil {
			note("delete %s: %v", d.MirrorCR, err)
		}
	}
	for _, src := range d.Sources {
		if src.Release == "" || src.Namespace == "" {
			continue
		}
		if _, err := defaultRunner.Run(ctx, "helm", "uninstall", src.Release, "-n", src.Namespace); err != nil && !isNotFound(err) {
			note("uninstall %s: %v", src.Release, err)
		}
	}
	if d.Target != nil {
		if _, err := defaultRunner.Run(ctx, "helm", "uninstall", d.Target.Release, "-n", d.Target.Namespace); err != nil && !isNotFound(err) {
			note("uninstall %s: %v", d.Target.Release, err)
		}
	}
	if labelled && d.TargetNamespace != "" && d.TargetCluster != "" {
		if err := deleteLabTopics(ctx, d, keepTopics); err != nil {
			note("lab topics on %s: %v", d.TargetCluster, err)
		}
	}
	if labelled && d.MirrorNamespace != "" {
		if _, err := defaultRunner.Run(ctx, "kubectl", "-n", d.MirrorNamespace, "delete", "secret", "-l", migrate.LabelLab+"="+d.Name, "--ignore-not-found"); err != nil {
			note("secrets: %v", err)
		}
	}
	for _, src := range d.Sources {
		if !src.NamespaceOwned || src.Namespace == "" {
			continue
		}
		if _, err := defaultRunner.Run(ctx, "kubectl", "delete", "namespace", src.Namespace, "--ignore-not-found", "--timeout="+formatSeconds(timeout)); err != nil {
			note("delete namespace %s: %v", src.Namespace, err)
		}
	}
	if labelled {
		removeLabFiles(d.Name)
	}
	if len(problems) > 0 {
		return fmt.Errorf("lab %s was not fully removed:\n  %s", d.Name, strings.Join(problems, "\n  "))
	}
	return nil
}

// isNotFound recognises Helm's "release: not found".
func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

// deleteLabTopics removes the lab's topics on the target through the Topic
// Operator: a KafkaTopic carrying the lab's labels is created for each (the
// operator adopts the existing topic) and deleted, which deletes the topic.
// No credential is needed and nothing but the named topics is touched. An
// internal topic another mirror on the target also writes is kept, and all
// of them are when the other mirrors cannot be listed. keepTopics leaves
// the mirrored topics out.
func deleteLabTopics(ctx context.Context, d *labDiscovery, keepTopics bool) error {
	shared, sharedErr := sharedInternalTopics(ctx, d)
	var topics []string
	if !keepTopics {
		topics = d.ReplicatedTopics()
	}
	for _, t := range d.internalTopics() {
		if sharedErr == nil && !shared[t] {
			topics = append(topics, t)
		}
	}
	if len(topics) > 0 {
		manifest, err := kafkaTopicManifests(d, topics)
		if err != nil {
			return err
		}
		if _, err := defaultRunner.RunInput(ctx, manifest, "kubectl", "-n", d.TargetNamespace, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("adopt the topics as KafkaTopics: %w", err)
		}
		if _, err := defaultRunner.Run(ctx, "kubectl", "-n", d.TargetNamespace, "delete", "kafkatopic", "-l", migrate.LabelLab+"="+d.Name, "--ignore-not-found", "--timeout=120s"); err != nil {
			return fmt.Errorf("delete the KafkaTopics: %w", err)
		}
	}
	if sharedErr != nil {
		return fmt.Errorf("kept the mirror's own topics (%s), since the other mirrors could not be listed: %w", strings.Join(d.internalTopics(), ", "), sharedErr)
	}
	return nil
}

// sharedInternalTopics lists the internal topics another KafkaMirrorMaker2
// on the lab's target also writes: every single-source lab, and the
// loopback mirror kates deploy installs, checkpoint under the alias source.
// A mirror whose target cannot be read is taken to write there.
func sharedInternalTopics(ctx context.Context, d *labDiscovery) (map[string]bool, error) {
	out, err := defaultRunner.Run(ctx, "kubectl", "get", "kafkamirrormaker2", "-A", "-o", "json")
	if err != nil {
		return nil, err
	}
	items, err := parseLabelledItems(out)
	if err != nil {
		return nil, err
	}
	shared := map[string]bool{}
	for _, it := range items {
		if it.Metadata.Namespace == d.MirrorNamespace && it.Metadata.Name == d.MirrorCR {
			continue
		}
		other := &labDiscovery{}
		other.readMirrorSpec(it.Spec, it.Metadata.Annotations[migrate.AnnotationLabTopics])
		if other.TargetCluster != "" && (other.TargetCluster != d.TargetCluster || other.TargetNamespace != d.TargetNamespace) {
			continue
		}
		if other.GroupID != "" {
			shared[other.GroupID+"-configs"], shared[other.GroupID+"-offsets"], shared[other.GroupID+"-status"] = true, true, true
		}
		for _, leg := range other.legs {
			shared[leg.Alias+".checkpoints.internal"] = true
		}
	}
	return shared, nil
}

// kafkaTopicManifests renders one KafkaTopic per topic, for kubectl apply
// on stdin.
func kafkaTopicManifests(d *labDiscovery, topics []string) (string, error) {
	type metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	}
	type spec struct {
		TopicName string `yaml:"topicName"`
	}
	type kafkaTopic struct {
		APIVersion string   `yaml:"apiVersion"`
		Kind       string   `yaml:"kind"`
		Metadata   metadata `yaml:"metadata"`
		Spec       spec     `yaml:"spec"`
	}
	var b strings.Builder
	for _, t := range topics {
		kt := kafkaTopic{
			APIVersion: "kafka.strimzi.io/v1",
			Kind:       "KafkaTopic",
			Metadata: metadata{
				Name:      kafkaTopicResourceName(d.Name, t),
				Namespace: d.TargetNamespace,
				Labels: map[string]string{
					"strimzi.io/cluster": d.TargetCluster,
					migrate.LabelLab:     d.Name,
					migrate.LabelRole:    migrate.RoleMirror,
				},
			},
			Spec: spec{TopicName: t},
		}
		out, err := yaml.Marshal(kt)
		if err != nil {
			return "", fmt.Errorf("render KafkaTopic for %s: %w", t, err)
		}
		b.WriteString("---\n")
		b.Write(out)
	}
	return b.String(), nil
}

// kafkaTopicResourceName is a DNS-1123 name for the KafkaTopic that adopts
// a topic: the lab name, then the topic lowercased with every other
// character turned into a dash. Whenever that changed the name — or the
// 63-character limit cut it — the first six hex digits of the topic's
// SHA-256 follow, so a.b, a-b and A_B are three KafkaTopics, not one
// overwriting the other two.
func kafkaTopicResourceName(lab, topic string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(topic) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	name := strings.Trim(lab+"-"+sanitized, "-")
	if sanitized == topic && len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(topic))
	suffix := "-" + hex.EncodeToString(sum[:3])
	if len(name)+len(suffix) > 63 {
		name = strings.TrimRight(name[:63-len(suffix)], "-")
	}
	return name + suffix
}
