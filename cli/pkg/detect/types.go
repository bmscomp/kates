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
	Name          string
	Provisioner   string
	BindingMode   string
	ReclaimPolicy string
	IsDefault     bool
}

type KafkaResources struct {
	KafkaClusters  int
	KafkaNodePools int
	KafkaTopics    int
	KafkaUsers     int
	PVCs           int
	BoundPVCs      int
	HelmRelease    string
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
	ExistingKafka KafkaResources
	Strimzi       StrimziInfo
	Monitoring    MonitoringInfo
	Network       NetworkInfo
	Admission     AdmissionInfo
	NetPolAudit   NetworkPolicyAudit
	Budget        BudgetReport
	Verdict       Verdict
}
