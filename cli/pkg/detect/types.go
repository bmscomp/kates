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

type ParsedReqs struct {
	BrokerCPU     int
	BrokerMem     int
	ControllerCPU int
	ControllerMem int
	OtherCPU      int
	OtherMem      int
}

// Types for the final analyzed report

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
	Budget        BudgetReport
	Verdict       Verdict
}
