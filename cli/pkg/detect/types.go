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
	Context       string
	Server        string
	Provider      string
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
	Monitoring    MonitoringInfo
	Network       NetworkInfo
	Admission     AdmissionInfo
	NetPolAudit   NetworkPolicyAudit
	Budget        BudgetReport
	Verdict       Verdict
}

// ── Generated Values Types (mirrors kafka-cluster Helm chart values.yaml) ────

type GeneratedValues struct {
	ClusterName    string                `yaml:"clusterName"`
	StrimziOp      GenStrimziOp          `yaml:"strimziOperator"`
	CRDUpgrade     GenCRDUpgrade         `yaml:"crdUpgrade"`
	Controllers    GenControllers        `yaml:"controllers"`
	BrokerPools    []GenBrokerPool       `yaml:"brokerPools"`
	BrokerDefaults GenBrokerDefaults     `yaml:"brokerDefaults"`
	Kafka          GenKafka              `yaml:"kafka"`
	Dashboards     GenDashboards         `yaml:"dashboards"`
	PodMonitors    GenPodMonitors        `yaml:"podMonitors"`
	Alerts         GenAlerts             `yaml:"alerts"`
	NetPolicies    GenNetPolicies        `yaml:"networkPolicies"`
}

type GenStrimziOp struct {
	Enabled bool `yaml:"enabled"`
}

type GenCRDUpgrade struct {
	Enabled bool `yaml:"enabled"`
}

type GenControllers struct {
	Replicas    int                    `yaml:"replicas"`
	Storage     GenStorage             `yaml:"storage"`
	TopologyTSC GenTopologyConstraints `yaml:"topologySpreadConstraints"`
	AntiAffinity GenAntiAffinity       `yaml:"podAntiAffinity"`
}

type GenStorage struct {
	Size  string `yaml:"size"`
	Class string `yaml:"class"`
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
}

type GenResourceValues struct {
	Memory string `yaml:"memory"`
	CPU    string `yaml:"cpu"`
}

type GenTopologyConstraints struct {
	Enabled     bool   `yaml:"enabled"`
	TopologyKey string `yaml:"topologyKey"`
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
