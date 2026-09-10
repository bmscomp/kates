package migrate

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

// Chart locations, relative to the repository root the CLI runs from (the
// same convention as `kates deploy`).
const (
	MirrorChartPath = "charts/mirror-maker2"
	SourceChartPath = "charts/legacy-kafka"

	// The mirror-maker2 chart's migration presets, one per source era, and
	// the overlay a cutover applies on top of whichever preset is in force.
	EraOverlay2x = "values-migrate-2x.yaml"
	EraOverlay3x = "values-migrate-3x.yaml"
	EraOverlay4x = "values-migrate-4x.yaml"
	CutoverFile  = "values-cutover.yaml"

	// DefaultTestTopic is the topic the chart's replication Helm test uses;
	// it is added to topicsPattern when the tests are enabled.
	DefaultTestTopic = "kates.mm2-e2e"
	// DefaultTestGroup and DefaultTranslationGroup are the consumer groups
	// the chart's Helm tests read and commit with.
	DefaultTestGroup        = "kates-mm2-test"
	DefaultTranslationGroup = "kates-mm2-e2e"
)

// SourceAuth is how MirrorMaker 2 authenticates to the source.
type SourceAuth string

const (
	// SourceAuthNone is an unauthenticated plaintext listener (the
	// legacy-kafka chart's default).
	SourceAuthNone SourceAuth = "none"
	// SourceAuthSASL is SASL over plaintext: PLAIN on the legacy chart's
	// 9094, SCRAM-SHA-512 on a Strimzi cluster's 9092.
	SourceAuthSASL SourceAuth = "sasl"
	// SourceAuthTLS is a TLS listener whose CA is trusted from a Secret,
	// with a SASL (SCRAM) credential on top. Mutual TLS (a client
	// certificate) is not produced here.
	SourceAuthTLS SourceAuth = "tls"
)

// SASL mechanisms in the spelling the mirror-maker2 chart's
// authentication.type takes.
const (
	MechanismPlain       = "plain"
	MechanismScramSHA512 = "scram-sha-512"
	MechanismScramSHA256 = "scram-sha-256"
)

// EraOverlay names the mirror-maker2 preset for a source version:
// EraOverlay2x below 3.0.0, EraOverlay3x below 4.0.0, EraOverlay4x from
// 4.0.0 (a Strimzi source).
func EraOverlay(from kafkaversion.Version) string {
	switch {
	case from.Less(kafkaversion.Version{Major: 3}):
		return EraOverlay2x
	case from.Less(kafkaversion.Version{Major: 4}):
		return EraOverlay3x
	default:
		return EraOverlay4x
	}
}

// EraOverlayPath is EraOverlay under MirrorChartPath.
func EraOverlayPath(from kafkaversion.Version) string {
	return MirrorChartPath + "/" + EraOverlay(from)
}

// CutoverValues is the path of the cutover overlay under MirrorChartPath.
func CutoverValues() string {
	return MirrorChartPath + "/" + CutoverFile
}

// MirrorInputs are the facts MirrorValues needs beyond the lab itself. Every
// field left at its zero value is taken from the lab, so a lab's own mirror
// needs only what the cluster told the CLI (broker count, versions).
//
// The Source* fields describe the lab's FIRST source, the one a command that
// works with a single cluster (mirror deploy --from-bootstrap, verify) can
// override. Further sources of a fan-in are taken from the lab itself, where
// New derived them.
type MirrorInputs struct {
	// SourceBootstrap is the source's address from inside the cluster.
	SourceBootstrap string
	// SourceVersion is declared to the chart's KIP-896 gate.
	SourceVersion kafkaversion.Version
	// SourceAuth, SourceMechanism, SourceUser and SourceSecret describe the
	// credential: the Secret must hold the password under the key
	// "password" and live in the mirror's namespace — or in
	// SourceSecretNamespace, from where the chart's secretSync copies it
	// under the same name.
	SourceAuth            SourceAuth
	SourceMechanism       string
	SourceUser            string
	SourceSecret          string
	SourceSecretNamespace string
	// SourceCASecret (with SourceCASecretNamespace, likewise synced) holds
	// the source's CA certificate under ca.crt for SourceAuthTLS.
	SourceCASecret          string
	SourceCASecretNamespace string
	// TargetCluster and TargetNamespace name the Strimzi target.
	TargetCluster, TargetNamespace string
	// TargetSecret is the target credential's Secret (TargetUser's when
	// empty); the source Secret must not share its name.
	TargetSecret string
	// TargetBrokerCount lets the chart check every replication factor at
	// render time; 0 disables the check. TargetRF is the replication factor
	// of the mirror's topics: min(3, brokers) when zero, 1 with an unknown
	// broker count.
	TargetBrokerCount, TargetRF int
	// StrimziVersion and KafkaVersion are the target operator's version and
	// the Kafka line the workers run — the target's, because the worker
	// image is a <strimzi>-kafka-<version> pair that exists only inside the
	// operator's window. Empty leaves the chart defaults.
	StrimziVersion, KafkaVersion string
	// OperatorNamespace is where the target's Cluster Operator runs, for the
	// NetworkPolicy; empty leaves the chart default.
	OperatorNamespace string
	// HelmTests enables the chart's replication and offset-translation Helm
	// tests on TestTopic (DefaultTestTopic when empty), which is then added
	// to the first source's topics.
	HelmTests bool
	TestTopic string
	// ReadOnlySource writes readOnlySource: true on every mirror, which the
	// chart turns into offset-syncs.topic.location=target (KIP-716) on both
	// connectors and into the matching target ACL: nothing is written to the
	// source, so its principal needs Read and Describe and nothing more. Off
	// by default, because the default location — the source — is Kafka's own
	// and the lab owns both ends of the cluster it creates.
	ReadOnlySource bool
}

// MirrorInputs returns the inputs for the lab's own mirror; the caller adds
// what only the cluster knows (TargetBrokerCount, StrimziVersion,
// KafkaVersion, OperatorNamespace).
func (l *Lab) MirrorInputs() MirrorInputs {
	m := MirrorInputs{
		SourceBootstrap: l.SourceBootstrap,
		SourceVersion:   l.From,
		SourceAuth:      l.SourceAuth(),
		SourceMechanism: l.SourceMechanism(),
		SourceUser:      l.SourceUser,
		SourceSecret:    l.SourceSecret,
		SourceCASecret:  l.SourceCASecret,
		TargetCluster:   l.TargetCluster,
		TargetNamespace: l.TargetNamespace,
		StrimziVersion:  l.StrimziVersion,
		KafkaVersion:    l.To.String(),
		ReadOnlySource:  l.ReadOnlySource,
	}
	if l.SourceCASecret != "" {
		m.SourceCASecretNamespace = l.SourceNamespace
	}
	return m
}

// The values types below mirror charts/mirror-maker2/values.schema.json. A
// typed struct rendered by a marshaller is what keeps a backslash in a
// topicsPattern intact — Helm's --set parser eats it — and what lets the
// chart's schema validate what the CLI sends.
type mirrorValues struct {
	ExtraLabels       map[string]string `yaml:"extraLabels"`
	Version           string            `yaml:"version,omitempty"`
	StrimziVersion    string            `yaml:"strimziVersion,omitempty"`
	ReplicationPolicy replicationPolicy `yaml:"replicationPolicy"`
	Compatibility     compatibility     `yaml:"compatibility"`
	Preflight         preflight         `yaml:"preflight"`
	Target            mirrorTarget      `yaml:"target"`
	Mirrors           []mirror          `yaml:"mirrors"`
	SecretSync        *secretSync       `yaml:"secretSync,omitempty"`
	Tests             *chartTests       `yaml:"tests,omitempty"`
	NetworkPolicy     *networkPolicy    `yaml:"networkPolicy,omitempty"`
}

type replicationPolicy struct {
	Mode                  string `yaml:"mode"`
	ExcludeInternalTopics bool   `yaml:"excludeInternalTopics"`
}

type compatibility struct {
	Enabled                bool   `yaml:"enabled"`
	Enforce                bool   `yaml:"enforce"`
	MinSourceVersion       string `yaml:"minSourceVersion"`
	RequireDeclaredVersion bool   `yaml:"requireDeclaredVersion"`
}

type preflight struct {
	Enabled     bool `yaml:"enabled"`
	FailOnError bool `yaml:"failOnError"`
}

type mirrorTarget struct {
	Alias       string         `yaml:"alias"`
	ClusterName string         `yaml:"clusterName"`
	Namespace   string         `yaml:"namespace"`
	GroupID     string         `yaml:"groupId"`
	BrokerCount int            `yaml:"brokerCount"`
	Config      map[string]any `yaml:"config"`
}

type mirror struct {
	Source mirrorSource `yaml:"source"`
	// Topics is the chart's list form: it escapes the names itself, which is
	// what keeps a dot in kates.orders a dot. topicsPattern is left out
	// entirely — the chart lets a pattern win over the list, so writing both
	// would silently ignore one.
	Topics              []string  `yaml:"topics"`
	GroupsPattern       string    `yaml:"groupsPattern"`
	ReadOnlySource      bool      `yaml:"readOnlySource,omitempty"`
	SourceConnector     connector `yaml:"sourceConnector"`
	CheckpointConnector connector `yaml:"checkpointConnector"`
}

type mirrorSource struct {
	Alias            string         `yaml:"alias"`
	BootstrapServers string         `yaml:"bootstrapServers"`
	KafkaVersion     string         `yaml:"kafkaVersion"`
	TLS              *tlsValues     `yaml:"tls,omitempty"`
	Authentication   authentication `yaml:"authentication"`
}

type tlsValues struct {
	Enabled                  bool   `yaml:"enabled"`
	TrustedCertificateSecret string `yaml:"trustedCertificateSecret"`
	CertificateKey           string `yaml:"certificateKey"`
}

type authentication struct {
	Type       string `yaml:"type"`
	Username   string `yaml:"username,omitempty"`
	SecretName string `yaml:"secretName,omitempty"`
	SecretKey  string `yaml:"secretKey,omitempty"`
}

type connector struct {
	TasksMax int            `yaml:"tasksMax"`
	State    string         `yaml:"state"`
	Config   map[string]any `yaml:"config"`
}

type secretSync struct {
	Enabled bool           `yaml:"enabled"`
	Secrets []syncedSecret `yaml:"secrets"`
}

type syncedSecret struct {
	Name          string `yaml:"name"`
	FromNamespace string `yaml:"fromNamespace"`
}

type chartTests struct {
	Replication       replicationTest `yaml:"replication"`
	OffsetTranslation translationTest `yaml:"offsetTranslation"`
}

type replicationTest struct {
	Enabled           bool   `yaml:"enabled"`
	Topic             string `yaml:"topic"`
	Partitions        int    `yaml:"partitions"`
	ReplicationFactor int    `yaml:"replicationFactor"`
	Messages          int    `yaml:"messages"`
	SourceAlias       string `yaml:"sourceAlias"`
	Group             string `yaml:"group"`
}

type translationTest struct {
	Enabled bool   `yaml:"enabled"`
	Group   string `yaml:"group"`
}

type networkPolicy struct {
	StrimziOperatorNamespace string `yaml:"strimziOperatorNamespace"`
}

// sourceInputs is one mirror's source as the values writer needs it: the
// first source comes from MirrorInputs (which a caller may have overridden),
// every further one from the lab.
type sourceInputs struct {
	Alias           string
	Bootstrap       string
	Version         kafkaversion.Version
	Auth            SourceAuth
	Mechanism       string
	User            string
	Secret          string
	SecretNamespace string
	CASecret        string
	CANamespace     string
	Topics          []string
}

// sourceAuthValues renders one source's authentication and TLS blocks, and
// refuses the credential mistakes that only surface inside a connector:
// a mechanism the chart does not take, a missing user or Secret, and a
// source Secret that would collide with the target's in the mirror's
// namespace.
func sourceAuthValues(in sourceInputs, m MirrorInputs) (authentication, *tlsValues, error) {
	switch in.Auth {
	case SourceAuthNone:
		return authentication{Type: ""}, nil, nil
	case SourceAuthSASL, SourceAuthTLS:
	default:
		return authentication{}, nil, fmt.Errorf("source auth %q: want %s, %s or %s", in.Auth, SourceAuthNone, SourceAuthSASL, SourceAuthTLS)
	}
	switch in.Mechanism {
	case MechanismPlain, MechanismScramSHA512, MechanismScramSHA256:
	case "":
		return authentication{}, nil, fmt.Errorf("source auth %s needs a SASL mechanism", in.Auth)
	default:
		return authentication{}, nil, fmt.Errorf("source SASL mechanism %q: want %s, %s or %s", in.Mechanism, MechanismPlain, MechanismScramSHA512, MechanismScramSHA256)
	}
	if in.User == "" || in.Secret == "" {
		return authentication{}, nil, fmt.Errorf("source auth %s needs a user and a credential Secret", in.Auth)
	}
	if in.Secret == m.TargetSecret {
		return authentication{}, nil, fmt.Errorf("source credential Secret %q has the target credential's name: MirrorMaker 2 reads both from %s, and secretSync would overwrite the target's — give the source Secret its own name",
			in.Secret, m.TargetNamespace)
	}
	auth := authentication{Type: in.Mechanism, Username: in.User, SecretName: in.Secret, SecretKey: "password"}
	if in.Auth != SourceAuthTLS {
		return auth, nil, nil
	}
	if in.CASecret == "" {
		return authentication{}, nil, errors.New("source auth tls needs the source's CA Secret")
	}
	return auth, &tlsValues{Enabled: true, TrustedCertificateSecret: in.CASecret, CertificateKey: "ca.crt"}, nil
}

// MirrorValues renders the values overlay the lab's mirror is installed
// with, layered on the era preset (EraOverlay):
//
//	helm upgrade --install <MirrorRelease> charts/mirror-maker2 -n <MirrorNamespace> \
//	  -f charts/mirror-maker2/<EraOverlay> -f <this file>
//
// It replaces the mirrors list wholesale (Helm merges maps and replaces
// lists, so every mirror carries everything it needs) — one entry per source
// of the lab, each naming its source by its bootstrap address and declaring
// its version to the chart's KIP-896 gate — keeps the preflight probe on,
// and sizes replication factors from the target's broker count.
func MirrorValues(l *Lab, m MirrorInputs) (string, error) {
	if l == nil {
		return "", errors.New("lab is required")
	}
	d := l.MirrorInputs()
	if m.SourceBootstrap == "" {
		m.SourceBootstrap = d.SourceBootstrap
	}
	if m.SourceVersion == (kafkaversion.Version{}) {
		m.SourceVersion = d.SourceVersion
	}
	if m.SourceAuth == "" {
		m.SourceAuth = d.SourceAuth
	}
	// The lab's own credential fills whatever the caller left empty, as long
	// as the caller did not switch to another kind of authentication.
	if m.SourceAuth == d.SourceAuth {
		if m.SourceMechanism == "" {
			m.SourceMechanism = d.SourceMechanism
		}
		if m.SourceUser == "" {
			m.SourceUser = d.SourceUser
		}
		if m.SourceSecret == "" {
			m.SourceSecret = d.SourceSecret
		}
		if m.SourceCASecret == "" {
			m.SourceCASecret = d.SourceCASecret
			m.SourceCASecretNamespace = d.SourceCASecretNamespace
		}
	}
	if m.TargetCluster == "" {
		m.TargetCluster = d.TargetCluster
	}
	if m.TargetNamespace == "" {
		m.TargetNamespace = d.TargetNamespace
	}
	if m.TargetSecret == "" {
		m.TargetSecret = TargetUser
	}
	if m.KafkaVersion == "" {
		m.KafkaVersion = d.KafkaVersion
	}
	if m.StrimziVersion == "" {
		m.StrimziVersion = d.StrimziVersion
	}
	if m.TestTopic == "" {
		m.TestTopic = DefaultTestTopic
	}

	if m.TargetBrokerCount < 0 || m.TargetRF < 0 {
		return "", errors.New("target broker count and replication factor cannot be negative")
	}
	if m.TargetRF == 0 {
		switch {
		case m.TargetBrokerCount >= 3:
			m.TargetRF = 3
		case m.TargetBrokerCount > 0:
			m.TargetRF = m.TargetBrokerCount
		default:
			m.TargetRF = 1
		}
	}
	if m.TargetBrokerCount > 0 && m.TargetRF > m.TargetBrokerCount {
		return "", fmt.Errorf("replication factor %d exceeds the target's %d broker(s)", m.TargetRF, m.TargetBrokerCount)
	}
	if len(l.Topics) == 0 {
		return "", errors.New("no topics to mirror")
	}

	// One entry per source: the first from the inputs (which a caller may
	// have overridden), the rest as the lab derived them.
	sources := l.Sources()
	inputs := make([]sourceInputs, 0, len(sources))
	first := sourceInputs{
		Alias:           sources[0].Alias,
		Bootstrap:       m.SourceBootstrap,
		Version:         m.SourceVersion,
		Auth:            m.SourceAuth,
		Mechanism:       m.SourceMechanism,
		User:            m.SourceUser,
		Secret:          m.SourceSecret,
		SecretNamespace: m.SourceSecretNamespace,
		CASecret:        m.SourceCASecret,
		CANamespace:     m.SourceCASecretNamespace,
		Topics:          sources[0].Topics,
	}
	if m.HelmTests {
		first.Topics = append(append([]string(nil), first.Topics...), m.TestTopic)
	}
	inputs = append(inputs, first)
	for _, s := range sources[1:] {
		in := sourceInputs{
			Alias: s.Alias, Bootstrap: s.Bootstrap, Version: s.From,
			Auth: s.Auth(), Mechanism: s.Mechanism(),
			User: s.User, Secret: s.Secret, CASecret: s.CASecret, Topics: s.Topics,
		}
		if s.CASecret != "" {
			in.CANamespace = s.Namespace
		}
		inputs = append(inputs, in)
	}

	var mirrors []mirror
	var synced []syncedSecret
	for _, in := range inputs {
		auth, tls, err := sourceAuthValues(in, m)
		if err != nil {
			return "", err
		}
		if len(in.Topics) == 0 {
			return "", fmt.Errorf("source %s has no topics to mirror", in.Alias)
		}
		if !strings.Contains(in.Bootstrap, ":") {
			return "", fmt.Errorf("source bootstrap %q: want host:port", in.Bootstrap)
		}
		if in.Version.Less(kafkaversion.MinMirrorSource) {
			return "", fmt.Errorf("source version %s is %w", in.Version, kafkaversion.ErrBelowMirrorFloor)
		}
		mirrors = append(mirrors, mirror{
			Source: mirrorSource{
				Alias:            in.Alias,
				BootstrapServers: in.Bootstrap,
				KafkaVersion:     in.Version.String(),
				TLS:              tls,
				Authentication:   auth,
			},
			Topics:         append([]string(nil), in.Topics...),
			GroupsPattern:  ".*",
			ReadOnlySource: m.ReadOnlySource,
			SourceConnector: connector{
				TasksMax: 2,
				State:    "running",
				Config: map[string]any{
					"replication.factor":                    m.TargetRF,
					"offset-syncs.topic.replication.factor": m.TargetRF,
					"sync.topic.acls.enabled":               "false",
					"sync.topic.configs.enabled":            "true",
					"refresh.topics.interval.seconds":       20,
				},
			},
			CheckpointConnector: connector{
				TasksMax: 1,
				State:    "running",
				Config: map[string]any{
					"checkpoints.topic.replication.factor": m.TargetRF,
					"sync.group.offsets.enabled":           "true",
					"sync.group.offsets.interval.seconds":  10,
					"emit.checkpoints.interval.seconds":    10,
					"refresh.groups.interval.seconds":      20,
				},
			},
		})
		if in.SecretNamespace != "" && in.SecretNamespace != m.TargetNamespace && in.Auth != SourceAuthNone {
			synced = append(synced, syncedSecret{Name: in.Secret, FromNamespace: in.SecretNamespace})
		}
		if tls != nil && in.CANamespace != "" && in.CANamespace != m.TargetNamespace {
			synced = append(synced, syncedSecret{Name: in.CASecret, FromNamespace: in.CANamespace})
		}
	}

	v := mirrorValues{
		ExtraLabels:    l.RoleLabels(RoleMirror),
		Version:        m.KafkaVersion,
		StrimziVersion: m.StrimziVersion,
		ReplicationPolicy: replicationPolicy{
			Mode:                  l.Policy,
			ExcludeInternalTopics: true,
		},
		Compatibility: compatibility{
			Enabled:                true,
			Enforce:                true,
			MinSourceVersion:       kafkaversion.MinMirrorSource.String(),
			RequireDeclaredVersion: true,
		},
		Preflight: preflight{Enabled: true, FailOnError: true},
		Target: mirrorTarget{
			Alias:       "target",
			ClusterName: m.TargetCluster,
			Namespace:   m.TargetNamespace,
			GroupID:     l.GroupID,
			BrokerCount: m.TargetBrokerCount,
			Config: map[string]any{
				"config.storage.replication.factor": m.TargetRF,
				"offset.storage.replication.factor": m.TargetRF,
				"status.storage.replication.factor": m.TargetRF,
			},
		},
		Mirrors: mirrors,
	}

	if len(synced) > 0 {
		v.SecretSync = &secretSync{Enabled: true, Secrets: synced}
	}
	if m.HelmTests {
		v.Tests = &chartTests{
			Replication: replicationTest{
				Enabled:           true,
				Topic:             m.TestTopic,
				Partitions:        1,
				ReplicationFactor: m.TargetRF,
				Messages:          l.Messages,
				SourceAlias:       l.SourceAlias,
				Group:             DefaultTestGroup,
			},
			OffsetTranslation: translationTest{Enabled: true, Group: DefaultTranslationGroup},
		}
	}
	if m.OperatorNamespace != "" {
		v.NetworkPolicy = &networkPolicy{StrimziOperatorNamespace: m.OperatorNamespace}
	}
	return marshalValues(v)
}

// The values types below mirror charts/legacy-kafka/values.schema.json.
type sourceValues struct {
	ExtraLabels   map[string]string `yaml:"extraLabels"`
	Mode          string            `yaml:"mode"`
	Kafka         sourceKafka       `yaml:"kafka"`
	ZooKeeper     *sourceZooKeeper  `yaml:"zookeeper,omitempty"`
	Listeners     *listeners        `yaml:"listeners,omitempty"`
	Persistence   persistence       `yaml:"persistence"`
	Topics        []sourceTopic     `yaml:"topics"`
	NetworkPolicy sourcePolicy      `yaml:"networkPolicy"`
}

type sourceKafka struct {
	Version           string          `yaml:"version"`
	Image             string          `yaml:"image"`
	Replicas          int             `yaml:"replicas"`
	ReplicationFactor int             `yaml:"replicationFactor"`
	HeapOpts          string          `yaml:"heapOpts"`
	Config            map[string]any  `yaml:"config"`
	Resources         sourceResources `yaml:"resources"`
}

type sourceZooKeeper struct {
	Replicas  int             `yaml:"replicas"`
	HeapOpts  string          `yaml:"heapOpts"`
	Resources sourceResources `yaml:"resources"`
}

type sourceResources struct {
	Requests map[string]string `yaml:"requests"`
	Limits   map[string]string `yaml:"limits"`
}

type listeners struct {
	Plaintext listener     `yaml:"plaintext"`
	SASL      saslListener `yaml:"sasl"`
}

type listener struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type saslListener struct {
	Enabled bool       `yaml:"enabled"`
	Port    int        `yaml:"port"`
	Users   []saslUser `yaml:"users"`
}

type saslUser struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type persistence struct {
	Enabled bool `yaml:"enabled"`
}

type sourceTopic struct {
	Name              string `yaml:"name"`
	Partitions        int    `yaml:"partitions"`
	ReplicationFactor int    `yaml:"replicationFactor"`
}

type sourcePolicy struct {
	Enabled bool `yaml:"enabled"`
}

// SourceTopicPartitions is the partition count of every corpus topic on the
// source — three, so a per-partition offset sum is a real sum.
const SourceTopicPartitions = 3

// SourceValues renders the values file a legacy source is installed with:
//
//	helm upgrade --install <SourceRelease> charts/legacy-kafka -n <SourceNamespace> -f <this file>
//
// It carries the era (mode, version, image, the protocol versions a 2.x
// broker should state), the corpus topics, the SASL listener when the lab
// asks for one — with the lab's password, which is why the file must be
// written with restrictive permissions and removed after the install — and
// the single-broker sizing of the chart's kind overlay. A Strimzi source is
// not this chart's business and is refused.
func SourceValues(l *Lab) (string, error) {
	if l == nil {
		return "", errors.New("lab is required")
	}
	return SourceValuesFor(l, l.Source())
}

// SourceValuesFor is SourceValues for one source of the lab — what a fan-in
// installs once per `--from`, each release with its own era, image, corpus
// and credential.
func SourceValuesFor(l *Lab, s *Source) (string, error) {
	if l == nil || s == nil {
		return "", errors.New("lab and source are required")
	}
	if s.Provider != kafkaversion.ProviderLegacy {
		return "", fmt.Errorf("source provider %s is not deployed by the legacy-kafka chart", s.Provider)
	}
	mode := "kraft"
	if s.Mode == kafkaversion.LegacyZooKeeper {
		mode = "zookeeper"
	}
	config := map[string]any{"log.retention.hours": 168}
	if s.From.Major < 3 {
		// A 2.x broker states the protocol it speaks, as values-kafka-2x.yaml
		// does, so a migration reader sees it in the effective config.
		config["inter.broker.protocol.version"] = s.From.MajorMinor()
		config["log.message.format.version"] = s.From.MajorMinor()
	}
	v := sourceValues{
		ExtraLabels: l.SourceLabels(s),
		Mode:        mode,
		Kafka: sourceKafka{
			Version:           s.From.String(),
			Image:             s.Image,
			Replicas:          1,
			ReplicationFactor: 1,
			HeapOpts:          "-Xms256m -Xmx384m",
			Config:            config,
			Resources: sourceResources{
				Requests: map[string]string{"memory": "640Mi", "cpu": "150m"},
				Limits:   map[string]string{"memory": "1Gi", "cpu": "1000m"},
			},
		},
		Persistence:   persistence{Enabled: false},
		NetworkPolicy: sourcePolicy{Enabled: true},
	}
	if mode == "zookeeper" {
		v.ZooKeeper = &sourceZooKeeper{
			Replicas: 1,
			HeapOpts: "-Xms128m -Xmx192m",
			Resources: sourceResources{
				Requests: map[string]string{"memory": "256Mi", "cpu": "50m"},
				Limits:   map[string]string{"memory": "512Mi", "cpu": "500m"},
			},
		}
	}
	for _, t := range s.Topics {
		v.Topics = append(v.Topics, sourceTopic{Name: t, Partitions: SourceTopicPartitions, ReplicationFactor: 1})
	}
	if s.SASL {
		if s.Password == "" {
			return "", errors.New("a SASL source needs a password")
		}
		v.Listeners = &listeners{
			Plaintext: listener{Enabled: true, Port: 9092},
			SASL: saslListener{
				Enabled: true,
				Port:    9094,
				Users:   []saslUser{{Username: s.User, Password: s.Password}},
			},
		}
	}
	return marshalValues(v)
}

// TopicsPattern is the Java regular expression that matches exactly the
// given topic names and nothing else: each name quoted (a dot in a topic
// name is a dot, not "any character"), joined with "|".
func TopicsPattern(topics []string) string {
	quoted := make([]string, 0, len(topics))
	for _, t := range topics {
		quoted = append(quoted, JavaRegexQuote(t))
	}
	return strings.Join(quoted, "|")
}

// JavaRegexQuote escapes every java.util.regex metacharacter in s with a
// backslash. Only "." and "-" can occur in a Kafka topic name, but quoting
// the full set costs nothing and guards a future caller.
func JavaRegexQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.[]{}()<>*+-=!?^$|`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SecretManifest renders an Opaque Secret with the given string data, for
// `kubectl apply -f -` on stdin — the way a credential reaches the cluster
// without ever being an argument. Keys are sorted so the output is stable.
func SecretManifest(namespace, name string, labels map[string]string, data map[string]string) (string, error) {
	if namespace == "" || name == "" {
		return "", errors.New("secret namespace and name are required")
	}
	if len(data) == 0 {
		return "", errors.New("secret has no data")
	}
	type metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels,omitempty"`
	}
	type secret struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   metadata          `yaml:"metadata"`
		Type       string            `yaml:"type"`
		StringData map[string]string `yaml:"stringData"`
	}
	s := secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Metadata:   metadata{Name: name, Namespace: namespace, Labels: labels},
		Type:       "Opaque",
		StringData: data,
	}
	out, err := yaml.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("render Secret %s/%s: %w", namespace, name, err)
	}
	return string(out), nil
}

func marshalValues(v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("render values: %w", err)
	}
	return string(out), nil
}
