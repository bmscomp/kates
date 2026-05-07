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

type StrimziInfo struct {
	CRDsPresent   bool
	Running       bool
	Namespace     string
	Image         string
	ReadyReplicas int
	TotalReplicas int
}

type MonitoringInfo struct {
	PodMonitorCRD     bool
	PrometheusRuleCRD bool
	GrafanaDeployed   bool
	ReleaseLabel      string
}

type NetworkInfo struct {
	CNI            string
	CoreDNSRunning int
	ClusterDomain  string
	PodCIDR        string
	ServiceCIDR    string
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
	Name           string
	Nodes          int
	CPUAvailable   int     // sum of available across nodes in zone
	MemAvailable   int     // GiB
	Utilization    float64 // 0.0–1.0 percentage of zone capacity used
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
	WeakestZone    string
	WeakestZoneCPU int
	WeakestZoneMem int

	// Component distribution
	ControllerCPU     int    // per-controller millicores
	ControllerMem     int    // per-controller GiB
	BrokerCPU         int    // per-broker millicores
	BrokerMem         int    // per-broker GiB
	BrokerStorage     string // per-broker storage
	ControllerStorage string // per-controller storage
	BrokerReplicas    int    // per-zone
	ControllerReplicas int

	// Metadata
	UtilizationPct float64 // current cluster utilization
	ReservePct     float64 // safety margin (default 0.30)
	Profile        string  // derived: production/staging/dev/minimal
}

type CheckResult struct {
	Description string
	Status      bool
	Detail      string
}

type Verdict struct {
	Checks     []CheckResult
	Fails      int
	Warns      int
	Compatible bool
}

type DetectReport struct {
	Context          string
	Server           string
	Provider         string
	K8sVersion       string
	K8sMinor         int
	HelmVersion      string
	HelmMajor        int
	Nodes            []NodeInfo
	Zones            []ZoneInfo
	Storage          []SCInfo
	StorageAudit     StorageAudit
	ExistingKafka    KafkaResources
	Strimzi          StrimziInfo
	Monitoring       MonitoringInfo
	Network          NetworkInfo
	Admission        AdmissionInfo
	NetPolAudit      NetworkPolicyAudit
	Workload         WorkloadPressure
	Budget           BudgetReport
	Capacity         CapacityBudget
	Verdict          Verdict
}

// ── Generated Values Types (mirrors kafka-cluster Helm chart values.yaml) ────

type GeneratedValues struct {
	ClusterName    string                `yaml:"clusterName"`
	Global         GenGlobal             `yaml:"global"`
	StrimziOp      GenStrimziOp          `yaml:"strimziOperator"`
	CRDUpgrade     GenCRDUpgrade         `yaml:"crdUpgrade"`
	ControllerPools    []GenControllerPool    `yaml:"controllerPools"`
	ControllerDefaults GenControllerDefaults  `yaml:"controllerDefaults"`
	BrokerPools        []GenBrokerPool        `yaml:"brokerPools"`
	BrokerDefaults     GenBrokerDefaults      `yaml:"brokerDefaults"`
	Kafka              GenKafka               `yaml:"kafka"`
	Dashboards         GenDashboards          `yaml:"dashboards"`
	PodMonitors        GenPodMonitors         `yaml:"podMonitors"`
	Alerts             GenAlerts              `yaml:"alerts"`
	NetPolicies        GenNetPolicies         `yaml:"networkPolicies"`
	Users              GenUsers               `yaml:"users"`
	Topics             GenFeature             `yaml:"topics"`
	CruiseControl      GenFeature             `yaml:"cruiseControl"`
	KafkaExporter      GenFeature             `yaml:"kafkaExporter"`
	DrainCleaner       GenFeature             `yaml:"drainCleaner"`
	Rebalance          GenFeature             `yaml:"rebalance"`
	KafkaConnect       GenFeature             `yaml:"kafkaConnect"`
	RBAC               GenFeature             `yaml:"rbac"`
	EntityOperator     map[string]interface{} `yaml:"entityOperator"`
	StrimziSubchart    GenStrimziSubchart     `yaml:"strimzi-kafka-operator"`
}

type GenGlobal struct {
	ClusterDomain string `yaml:"clusterDomain"`
}

type GenStrimziOp struct {
	Enabled bool `yaml:"enabled"`
}

type GenStrimziSubchart struct {
	KubernetesServiceDnsDomain string `yaml:"kubernetesServiceDnsDomain"`
}

type GenCRDUpgrade struct {
	Enabled bool `yaml:"enabled"`
}

type GenControllerPool struct {
	Name         string `yaml:"name"`
	Zone         string `yaml:"zone"`
	Replicas     int    `yaml:"replicas"`
	StorageSize  string `yaml:"storageSize"`
	StorageClass string `yaml:"storageClass"`
}

type GenControllerDefaults struct {
	Resources    GenResources           `yaml:"resources"`
	TopologyTSC  GenTopologyConstraints `yaml:"topologySpreadConstraints"`
	AntiAffinity GenAntiAffinity       `yaml:"podAntiAffinity"`
}

type GenStorage struct {
	Size  string `yaml:"size"`
	Class string `yaml:"class,omitempty"`
}

type GenBrokerPool struct {
	Name         string `yaml:"name"`
	Zone         string `yaml:"zone"`
	Replicas     int    `yaml:"replicas"`
	StorageSize  string `yaml:"storageSize"`
	StorageClass string `yaml:"storageClass"`
}

type GenBrokerDefaults struct {
	Resources    GenResources           `yaml:"resources"`
	TopologyTSC  GenTopologyConstraints `yaml:"topologySpreadConstraints"`
	AntiAffinity GenAntiAffinity       `yaml:"podAntiAffinity"`
}

type GenResources struct {
	Requests GenResourceValues `yaml:"requests"`
	Limits   GenResourceValues `yaml:"limits"`
}

type GenResourceValues struct {
	Memory string `yaml:"memory"`
	CPU    string `yaml:"cpu"`
}

type GenTopologyConstraints struct {
	Enabled           bool   `yaml:"enabled"`
	TopologyKey       string `yaml:"topologyKey"`
	WhenUnsatisfiable string `yaml:"whenUnsatisfiable,omitempty"`
}

type GenAntiAffinity struct {
	Enabled     bool   `yaml:"enabled"`
	TopologyKey string `yaml:"topologyKey"`
}

type GenKafka struct {
	Listeners []GenListener `yaml:"listeners"`
	Rack      GenRack       `yaml:"rack"`
}

type GenListener struct {
	Name           string               `yaml:"name"`
	Port           int                  `yaml:"port"`
	Type           string               `yaml:"type"`
	TLS            bool                 `yaml:"tls"`
	Authentication *GenAuth             `yaml:"authentication,omitempty"`
	Configuration  *GenListenerConfig   `yaml:"configuration,omitempty"`
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

type GenDashboards struct {
	Enabled   bool   `yaml:"enabled"`
	Namespace string `yaml:"namespace,omitempty"`
}

type GenPodMonitors struct {
	Enabled bool              `yaml:"enabled"`
	Labels  map[string]string `yaml:"labels,omitempty"`
}

type GenAlerts struct {
	Enabled bool              `yaml:"enabled"`
	Labels  map[string]string `yaml:"labels,omitempty"`
}

type GenNetPolicies struct {
	Enabled          bool              `yaml:"enabled"`
	DefaultDeny      bool              `yaml:"defaultDeny"`
	DefaultSelector  map[string]string `yaml:"defaultDenySelector,omitempty"`
	AllowDNS         bool              `yaml:"allowDNS"`
	AllowDNSSelector map[string]string `yaml:"allowDNSSelector,omitempty"`
}

type GenUsers struct {
	Enabled bool      `yaml:"enabled"`
	Items   []GenUser `yaml:"items"`
}

type GenUser struct {
	Name           string       `yaml:"name"`
	Authentication GenUserAuth  `yaml:"authentication"`
	Authorization  *GenUserAuthz `yaml:"authorization,omitempty"`
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
	Create  bool `yaml:"create,omitempty"`
}
