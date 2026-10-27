package detect

// Types for raw collected data

type NodeInfo struct {
	Name     string
	Zone     string
	Roles    string
	CPU      int
	MemoryGi int
	Runtime  string
	Kubelet  string
	OS       string
	// ProviderID is the node's spec.providerID ("kind://docker/panda/…").
	ProviderID string
}

type ZoneInfo struct {
	Name             string
	Nodes            int
	CPUAllocatable   int
	MemAllocatableGi int
}

type SCInfo struct {
	Name           string
	Provisioner    string
	BindingMode    string
	ReclaimPolicy  string
	IsDefault      bool
	AllowExpansion bool
	ProbedIOPS     int
	ProbeLatencyMs float64
	// IOPSMeasured says ProbedIOPS came from the live fio bench; otherwise
	// it is an estimate from the provisioner's and the class's names.
	IOPSMeasured bool
}

type StorageAudit struct {
	PVCount         int
	PVTotalCapacity string
	PVBoundCount    int
	PVAvailable     int
	CSIDrivers      []string
}

type KafkaResources struct {
	KafkaClusters  int
	KafkaNodePools int
	KafkaTopics    int
	KafkaUsers     int
	PVCs           int
	BoundPVCs      int
	HelmRelease    string
	Health         KafkaClusterHealth
}

type KafkaClusterHealth struct {
	Name          string
	Version       string
	Replicas      int
	ReadyReplicas int
	Listeners     []KafkaListener
	Conditions    []KafkaCondition
}

type KafkaListener struct {
	Name string
	Type string
	Port int
	TLS  bool
}

type KafkaCondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

type StrimziHealth struct {
	Status      string // "Healthy", "Degraded", "Unhealthy"
	PodsReady   bool
	WarningLogs []string
	MissingCRDs []string
}

type StrimziInfo struct {
	CRDsPresent    bool
	Running        bool
	Namespace      string
	Image          string
	ReadyReplicas  int
	TotalReplicas  int
	Health         StrimziHealth
	CapacityStatus string
}

type MonitoringInfo struct {
	PodMonitorCRD     bool
	PrometheusRuleCRD bool
	GrafanaDeployed   bool
	ReleaseLabel      string
}

type EcosystemInfo struct {
	KafkaConnect   KafkaConnectStatus
	SchemaRegistry SchemaRegistryStatus
	Database       DatabaseCDCStatus
}

type KafkaConnectStatus struct {
	Installed      bool
	Name           string
	Namespace      string
	ReadyReplicas  int
	TotalReplicas  int
	Image          string
	Connectors     []ConnectorStatus
	Plugins        []string
	HasBuild       bool
	TracingEnabled bool
	RackAware      bool
	HeapXms        string
	HeapXmx        string
	MemLimitMi     int
	CPURequestM    int
	CPULimitM      int
	TLSEnabled     bool
	PDBConfigured  bool
}

type ConnectorStatus struct {
	Name     string
	Class    string
	TasksMax int
	Status   string
}

type SchemaRegistryStatus struct {
	Installed bool
	Name      string
	Namespace string
	Available bool
}

type DatabaseCDCStatus struct {
	Installed  bool
	Name       string
	Namespace  string
	Port       int
	Accessible bool
}

type LatencyResult struct {
	SourceZone string
	TargetZone string
	MinMs      float64
	MaxMs      float64
	AvgMs      float64
	JitterMs   float64
	Success    bool
}

type BandwidthResult struct {
	SourceZone    string
	TargetZone    string
	BandwidthMbps float64
	Success       bool
}

type DNSProbeResult struct {
	QueryType    string // "Internal" or "External"
	QueriesRun   int
	SuccessCount int
	SuccessRate  float64
	AvgLatencyMs float64
	MaxLatencyMs float64
}

type NetworkInfo struct {
	CNI             string
	CoreDNSRunning  int
	ClusterDomain   string
	PodCIDR         string
	ServiceCIDR     string
	LatencyMatrix   []LatencyResult
	BandwidthMatrix []BandwidthResult
	DNSResults      []DNSProbeResult
}

// ── Admission Controller Types ───────────────────────────────────────────────

type AdmissionInfo struct {
	Kyverno    KyvernoInfo
	Gatekeeper GatekeeperInfo
}

type KyvernoInfo struct {
	Installed       bool
	Namespace       string
	Version         string
	ClusterPolicies []KyvernoPolicyInfo
	KafkaRelevant   []KyvernoPolicyInfo
	Constraints     PolicyConstraints
}

type KyvernoPolicyInfo struct {
	Name         string
	Action       string // "enforce" or "audit"
	Category     string
	AffectsKafka bool
	Rules        []string
	Description  string
}

type PolicyConstraints struct {
	EmptyPodSelectorBlocked bool
	HostNetworkBlocked      bool
	PrivilegedBlocked       bool
	RunAsRootBlocked        bool
	LatestTagBlocked        bool
	ResourceLimitsRequired  bool
	CustomConstraints       []string
}

type GatekeeperInfo struct {
	Installed   bool
	Namespace   string
	Constraints []GatekeeperConstraint
}

type GatekeeperConstraint struct {
	Name   string
	Kind   string
	Action string
}

// ── NetworkPolicy Audit Types ────────────────────────────────────────────────

type NetworkPolicyAudit struct {
	Existing       []ExistingNetPol
	TotalCount     int
	HasDefaultDeny bool
	HasDNSAllow    bool
}

type ExistingNetPol struct {
	Name         string
	Namespace    string
	PodSelector  string
	PolicyTypes  []string
	IngressRules int
	EgressRules  int
	ManagedBy    string
}

// ── Workload Pressure Types ──────────────────────────────────────────────────

type WorkloadPressure struct {
	TotalPods          int
	TotalCPURequests   int // millicores consumed by all running pods
	TotalMemRequests   int // GiB consumed by all running pods
	KafkaNamespacePods int
	PerNode            []NodePressure
	PerZone            []ZonePressure
}

type NodePressure struct {
	Name           string
	Zone           string
	CPUAllocatable int // total allocatable millicores
	CPURequested   int // consumed by existing pods
	CPUAvailable   int // allocatable - requested
	MemAllocatable int // GiB
	MemRequested   int // GiB
	MemAvailable   int // GiB
}

type ZonePressure struct {
	Name         string
	Nodes        int
	CPUAvailable int     // sum of available across nodes in zone
	MemAvailable int     // GiB
	Utilization  float64 // 0.0–1.0 percentage of zone capacity used
}

// ── Budget & Verdict Types ───────────────────────────────────────────────────

type ParsedReqs struct {
	BrokerCPU     int
	BrokerMem     int
	ControllerCPU int
	ControllerMem int
	OtherCPU      int
	OtherMem      int
}

type BudgetReport struct {
	CtrlCPU, CtrlMem     int
	BrokerCPU, BrokerMem int
	OtherCPU, OtherMem   int
	NeedCPU, NeedMem     int
	TotalCPU, TotalMem   int
	Sufficient           bool
	RecommendedProfile   string
}

type CapacityBudget struct {
	// Cluster-wide totals
	TotalCPU     int // total allocatable millicores
	TotalMem     int // total allocatable GiB
	UsedCPU      int // consumed by existing workloads
	UsedMem      int // consumed by existing workloads
	AvailableCPU int // total - used
	AvailableMem int // total - used

	// Kafka allocation (after reserve)
	KafkaCPU int // available × (1 - reserve)
	KafkaMem int // available × (1 - reserve)

	// Per-zone bottleneck
	WeakestZone      string
	WeakestZoneCPU   int
	WeakestZoneMem   int
	StrongestZoneCPU int
	StrongestZoneMem int

	// Component distribution
	ControllerCPU      int    // per-controller millicores
	ControllerMem      int    // per-controller GiB
	BrokerCPU          int    // per-broker millicores
	BrokerMem          int    // per-broker GiB
	BrokerStorage      string // per-broker storage
	ControllerStorage  string // per-controller storage
	BrokerReplicas     int    // per-zone
	ControllerReplicas int

	// Metadata
	UtilizationPct float64 // current cluster utilization
	ReservePct     float64 // safety margin (default 0.30)
	Profile        string  // derived: production/staging/dev/minimal
}

type CheckResult struct {
	Description string
	Status      bool
	// Warn marks a passing check that counts as a warning.
	Warn   bool `json:",omitempty"`
	Detail string
}

type Verdict struct {
	Checks     []CheckResult
	Fails      int
	Warns      int
	Compatible bool
}

type SecretCreationAudit struct {
	NamespaceCreated bool
	SecretCreated    bool
	ErrorMsg         string
	BlockedByPolicy  bool
	PolicyName       string
}

type CertExpirationInfo struct {
	SecretName string
	Subject    string
	ExpiryDate string
	DaysLeft   int
}

type SecurityAudit struct {
	PSALabelEnforced       string // "restricted", "baseline", "privileged" or "none"
	KyvernoEnforced        bool
	PermissionsOk          bool
	ExpiringCerts          []CertExpirationInfo
	HasExcessivePrivileges bool
}

type DetectReport struct {
	Context       string
	Server        string
	Provider      string
	ProviderBasis string // what Provider was read from: "server version v1.30.4-eks-…"
	K8sVersion    string
	K8sMinor      int
	HelmVersion   string
	HelmMajor     int
	Nodes         []NodeInfo
	Zones         []ZoneInfo
	Storage       []SCInfo
	StorageAudit  StorageAudit
	ExistingKafka KafkaResources
	Strimzi       StrimziInfo
	Ecosystem     EcosystemInfo
	Monitoring    MonitoringInfo
	Network       NetworkInfo
	Admission     AdmissionInfo
	NetPolAudit   NetworkPolicyAudit
	Workload      WorkloadPressure
	Budget        BudgetReport
	Capacity      CapacityBudget
	Verdict       Verdict
	SecretAudit   SecretCreationAudit
	Security      SecurityAudit
}

// ── Generated Values Types (mirrors kafka-cluster 1.x values.yaml) ───────────
//
// Only keys the chart reads are emitted. The 0.4 shape (controllerPools,
// brokerPools, *Defaults, networkPolicies, strimziOperator, crdUpgrade,
// kafkaConnect, drainCleaner) is still translated by kafka-cluster 1.x, but
// three of those blocks are ignored there and drainCleaner.enabled fails the
// render, so none of them belongs in a file this CLI writes.

type GeneratedValues struct {
	Global      GenGlobal `yaml:"global"`
	ClusterName string    `yaml:"clusterName"`

	NodePools      GenNodePools           `yaml:"nodePools"`
	Kafka          GenKafka               `yaml:"kafka"`
	Monitoring     GenMonitoring          `yaml:"monitoring"`
	Alerts         GenAlerts              `yaml:"alerts"`
	NetworkPolicy  GenNetworkPolicy       `yaml:"networkPolicy"`
	Users          *GenUsers              `yaml:"users,omitempty"`
	Topics         GenFeature             `yaml:"topics"`
	CruiseControl  GenFeature             `yaml:"cruiseControl"`
	KafkaExporter  GenFeature             `yaml:"kafkaExporter"`
	Rebalance      GenFeature             `yaml:"rebalance"`
	RBAC           GenRBAC                `yaml:"rbac"`
	EntityOperator map[string]interface{} `yaml:"entityOperator"`
}

type GenGlobal struct {
	ClusterDomain string `yaml:"clusterDomain"`
	StorageClass  string `yaml:"storageClass,omitempty"`
}

// GenNodePools is nodePools: every pool is rendered from Pools, with
// precedence pool > roleDefaults[role] > defaults (maps merged, lists
// replaced), so a pool's storage volume carries its size and class in full.
type GenNodePools struct {
	Defaults     GenPoolDefaults `yaml:"defaults"`
	RoleDefaults GenRoleDefaults `yaml:"roleDefaults"`
	Pools        []GenPool       `yaml:"pools"`
}

type GenPoolDefaults struct {
	Scheduling GenScheduling `yaml:"scheduling"`
}

type GenScheduling struct {
	// ZoneKey is the node label a pool's zone pins to.
	ZoneKey      string          `yaml:"zoneKey,omitempty"`
	Spread       GenSpread       `yaml:"spread"`
	AntiAffinity GenAntiAffinity `yaml:"antiAffinity"`
}

type GenSpread struct {
	Enabled           bool   `yaml:"enabled"`
	TopologyKey       string `yaml:"topologyKey"`
	WhenUnsatisfiable string `yaml:"whenUnsatisfiable,omitempty"`
}

type GenRoleDefaults struct {
	Controller GenRoleDefault `yaml:"controller"`
	Broker     GenRoleDefault `yaml:"broker"`
}

type GenRoleDefault struct {
	Resources GenResources `yaml:"resources"`
}

type GenPool struct {
	Name     string         `yaml:"name"`
	Roles    []string       `yaml:"roles"`
	Zone     string         `yaml:"zone,omitempty"`
	Replicas int            `yaml:"replicas"`
	Storage  GenPoolStorage `yaml:"storage"`
}

type GenPoolStorage struct {
	Volumes []GenVolume `yaml:"volumes"`
}

type GenVolume struct {
	ID    int    `yaml:"id"`
	Size  string `yaml:"size"`
	Class string `yaml:"class,omitempty"`
}

type GenResources struct {
	Requests GenResourceValues `yaml:"requests"`
	Limits   GenResourceValues `yaml:"limits"`
}

type GenResourceValues struct {
	Memory string `yaml:"memory"`
	CPU    string `yaml:"cpu"`
}

type GenAntiAffinity struct {
	Enabled     bool   `yaml:"enabled"`
	TopologyKey string `yaml:"topologyKey"`
}

type GenRBAC struct {
	Create bool `yaml:"create"`
}

type GenKafka struct {
	Listeners []GenListener `yaml:"listeners"`
	Rack      GenRack       `yaml:"rack"`
}

type GenListener struct {
	Name           string             `yaml:"name"`
	Port           int                `yaml:"port"`
	Type           string             `yaml:"type"`
	TLS            bool               `yaml:"tls"`
	Authentication *GenAuth           `yaml:"authentication,omitempty"`
	Configuration  *GenListenerConfig `yaml:"configuration,omitempty"`
}

type GenAuth struct {
	Type string `yaml:"type"`
}

type GenListenerConfig struct {
	Bootstrap GenBootstrapConfig `yaml:"bootstrap"`
}

type GenBootstrapConfig struct {
	Annotations map[string]string `yaml:"annotations"`
}

type GenRack struct {
	TopologyKey string `yaml:"topologyKey"`
}

// GenMonitoring is kafka-cluster 1.x's monitoring block. The 0.4 key
// podMonitors is still accepted there, but it wins over this one whenever
// this one carries the chart default, so emitting it would make an explicit
// monitoring.podMonitor.enabled=true from the caller a no-op.
type GenMonitoring struct {
	PodMonitor GenPodMonitor `yaml:"podMonitor"`
}

type GenPodMonitor struct {
	Enabled bool `yaml:"enabled"`
}

type GenAlerts struct {
	Enabled bool `yaml:"enabled"`
}

// GenNetworkPolicy is kafka-cluster 1.x's networkPolicy block (the 0.4
// networkPolicies block is translated over it, so emitting that one would
// shadow a base file's).
type GenNetworkPolicy struct {
	Enabled     bool              `yaml:"enabled"`
	DefaultDeny GenSelectorToggle `yaml:"defaultDeny"`
	DNS         GenSelectorToggle `yaml:"dns"`
}

type GenSelectorToggle struct {
	Enabled  bool              `yaml:"enabled"`
	Selector map[string]string `yaml:"selector,omitempty"`
}

type GenUsers struct {
	Enabled bool      `yaml:"enabled"`
	Items   []GenUser `yaml:"items"`
}

type GenUser struct {
	Name           string         `yaml:"name"`
	Authentication GenUserAuth    `yaml:"authentication"`
	Quotas         *GenUserQuotas `yaml:"quotas,omitempty"`
	Authorization  *GenUserAuthz  `yaml:"authorization,omitempty"`
}

type GenUserQuotas struct {
	ProducerByteRate  int `yaml:"producerByteRate,omitempty"`
	ConsumerByteRate  int `yaml:"consumerByteRate,omitempty"`
	RequestPercentage int `yaml:"requestPercentage,omitempty"`
}

type GenUserAuth struct {
	Type string `yaml:"type"`
}

type GenUserAuthz struct {
	Type string   `yaml:"type"`
	Acls []GenAcl `yaml:"acls"`
}

type GenAcl struct {
	Resource   GenAclResource `yaml:"resource"`
	Operations []string       `yaml:"operations"`
}

type GenAclResource struct {
	Type        string `yaml:"type"`
	Name        string `yaml:"name,omitempty"`
	PatternType string `yaml:"patternType,omitempty"`
}

type GenFeature struct {
	Enabled bool `yaml:"enabled"`
}
