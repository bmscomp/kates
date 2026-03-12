package client

import "encoding/json"

// HealthResponse from GET /api/health
type HealthResponse struct {
	Status string                `json:"status"`
	Engine *EngineInfo           `json:"engine,omitempty"`
	Kafka  *KafkaInfo            `json:"kafka,omitempty"`
	Tests  map[string]TestConfig `json:"tests,omitempty"`
}

type EngineInfo struct {
	ActiveBackend     string   `json:"activeBackend"`
	AvailableBackends []string `json:"availableBackends"`
}

type KafkaInfo struct {
	Status           string `json:"status"`
	BootstrapServers string `json:"bootstrapServers"`
	Message          string `json:"message"`
}

type TestConfig struct {
	NumRecords      int    `json:"numRecords"`
	Partitions      int    `json:"partitions"`
	NumProducers    int    `json:"numProducers"`
	Acks            string `json:"acks"`
	CompressionType string `json:"compressionType"`
}

// ClusterInfo from GET /api/cluster/info
type ClusterInfo struct {
	ClusterID   string       `json:"clusterId"`
	BrokerCount interface{}  `json:"brokerCount"`
	Controller  *BrokerNode  `json:"controller,omitempty"`
	Brokers     []BrokerNode `json:"brokers,omitempty"`
}

type BrokerNode struct {
	ID   interface{} `json:"id"`
	Host string      `json:"host"`
	Port interface{} `json:"port"`
	Rack string      `json:"rack"`
}

type ClusterTopology struct {
	Kubernetes     *K8sInfo                 `json:"kubernetes,omitempty"`
	Strimzi        map[string]interface{}   `json:"strimzi,omitempty"`
	Cluster        *TopoClusterInfo         `json:"cluster,omitempty"`
	KafkaConfig    map[string]interface{}   `json:"kafkaConfig,omitempty"`
	NodePools      []NodePoolInfo           `json:"nodePools,omitempty"`
	Nodes          []TopologyNode           `json:"nodes,omitempty"`
	EntityOperator map[string]interface{}   `json:"entityOperator,omitempty"`
	CruiseControl  map[string]interface{}   `json:"cruiseControl,omitempty"`
	KafkaExporter  map[string]interface{}   `json:"kafkaExporter,omitempty"`
	Certificates   map[string]interface{}   `json:"certificates,omitempty"`
	Metrics        map[string]interface{}   `json:"metrics,omitempty"`
	Topics         *TopoTopics              `json:"topics,omitempty"`
	Users          *TopoUsers               `json:"users,omitempty"`
	ConsumerGroups *TopoCountItems           `json:"consumerGroups,omitempty"`
	ACLs           *TopoCountItems           `json:"acls,omitempty"`
	LogDirs        []map[string]interface{}  `json:"logDirs,omitempty"`
	FeatureFlags   *TopoCountItems           `json:"featureFlags,omitempty"`
	Rebalances     []map[string]interface{}  `json:"rebalances,omitempty"`
	DrainCleaner   map[string]interface{}    `json:"drainCleaner,omitempty"`
	PodSets          []map[string]interface{}  `json:"podSets,omitempty"`
	NetworkPolicies  []map[string]interface{}  `json:"networkPolicies,omitempty"`
	PVCs             []map[string]interface{}  `json:"pvcs,omitempty"`
	Services         []map[string]interface{}  `json:"services,omitempty"`
	Endpoints        []map[string]interface{}  `json:"endpoints,omitempty"`
	Connect          []map[string]interface{}  `json:"connect,omitempty"`
	MirrorMaker    []map[string]interface{}  `json:"mirrorMaker2,omitempty"`
}

type TopoCountItems struct {
	Count int                      `json:"count"`
	Items []map[string]interface{} `json:"items,omitempty"`
}

type ClusterAlertsResponse struct {
	TotalRulesScanned int         `json:"totalRulesScanned"`
	CriticalCount     int         `json:"criticalCount"`
	WarningCount      int         `json:"warningCount"`
	Count             int         `json:"count"`
	Alerts            []AlertRule `json:"alerts"`
}

type AlertRule struct {
	Name        string `json:"name"`
	Severity    string `json:"severity"`
	Group       string `json:"group"`
	Source      string `json:"source"`
	Expr        string `json:"expr"`
	For         string `json:"for"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
}

type K8sInfo struct {
	Version    string                   `json:"version,omitempty"`
	Platform   string                   `json:"platform,omitempty"`
	GitVersion string                   `json:"gitVersion,omitempty"`
	Nodes      []map[string]interface{} `json:"nodes,omitempty"`
	NodeCount  int                      `json:"nodeCount,omitempty"`
}

type TopoClusterInfo struct {
	Name                   string                   `json:"name,omitempty"`
	Namespace              string                   `json:"namespace,omitempty"`
	KraftMode              bool                     `json:"kraftMode"`
	KafkaVersion           string                   `json:"kafkaVersion,omitempty"`
	ClusterID              string                   `json:"clusterId,omitempty"`
	ControllerQuorumLeader int                      `json:"controllerQuorumLeader"`
	BrokerCount            int                      `json:"brokerCount"`
	Ready                  bool                     `json:"ready"`
	Listeners              []map[string]interface{} `json:"listeners,omitempty"`
	Authorization          map[string]interface{}   `json:"authorization,omitempty"`
	RackAwareness          map[string]interface{}   `json:"rackAwareness,omitempty"`
	PodDisruptionBudget    map[string]interface{}   `json:"podDisruptionBudget,omitempty"`
}

type NodePoolInfo struct {
	Name         string                 `json:"name"`
	Role         string                 `json:"role"`
	Replicas     int                    `json:"replicas"`
	StorageType  string                 `json:"storageType"`
	StorageSize  string                 `json:"storageSize"`
	StorageClass string                 `json:"storageClass,omitempty"`
	Resources    map[string]interface{} `json:"resources,omitempty"`
	JVMOptions   map[string]interface{} `json:"jvmOptions,omitempty"`
	Scheduling   map[string]interface{} `json:"scheduling,omitempty"`
}

type TopologyNode struct {
	ID             int    `json:"id"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Rack           string `json:"rack"`
	Role           string `json:"role"`
	Pool           string `json:"pool"`
	Status         string `json:"status"`
	IsQuorumLeader bool   `json:"isQuorumLeader"`
	K8sNode        string `json:"k8sNode,omitempty"`
}

type TopoTopics struct {
	Count int                      `json:"count"`
	Items []map[string]interface{} `json:"items,omitempty"`
}

type TopoUsers struct {
	Count int                      `json:"count"`
	Items []map[string]interface{} `json:"items,omitempty"`
}

// TestRun from GET /api/tests/:id and POST /api/tests
type TestRun struct {
	ID           string        `json:"id"`
	TestType     string        `json:"testType"`
	Status       string        `json:"status"`
	Backend      string        `json:"backend"`
	ScenarioName string        `json:"scenarioName"`
	CreatedAt    string        `json:"createdAt"`
	Spec         *TestSpec     `json:"spec,omitempty"`
	Results      []PhaseResult `json:"results,omitempty"`
}

type PhaseResult struct {
	PhaseName               string           `json:"phaseName"`
	Status                  string           `json:"status"`
	RecordsSent             float64          `json:"recordsSent"`
	ThroughputRecordsPerSec float64          `json:"throughputRecordsPerSec"`
	ThroughputMBPerSec      float64          `json:"throughputMBPerSec"`
	AvgLatencyMs            float64          `json:"avgLatencyMs"`
	P50LatencyMs            float64          `json:"p50LatencyMs"`
	P95LatencyMs            float64          `json:"p95LatencyMs"`
	P99LatencyMs            float64          `json:"p99LatencyMs"`
	MaxLatencyMs            float64          `json:"maxLatencyMs"`
	Error                   string           `json:"error,omitempty"`
	Integrity               *IntegrityResult `json:"integrity,omitempty"`
}

type IntegrityResult struct {
	TotalSent           int64            `json:"totalSent"`
	TotalAcked          int64            `json:"totalAcked"`
	TotalConsumed       int64            `json:"totalConsumed"`
	LostRecords         int64            `json:"lostRecords"`
	DuplicateRecords    int64            `json:"duplicateRecords"`
	DataLossPercent     float64          `json:"dataLossPercent"`
	LostRanges          []LostRange      `json:"lostRanges,omitempty"`
	ProducerRtoMs       float64          `json:"producerRtoMs,omitempty"`
	ConsumerRtoMs       float64          `json:"consumerRtoMs,omitempty"`
	MaxRtoMs            float64          `json:"maxRtoMs,omitempty"`
	RpoMs               float64          `json:"rpoMs,omitempty"`
	OutOfOrderCount     int64            `json:"outOfOrderCount"`
	CrcFailures         int64            `json:"crcFailures"`
	OrderingVerified    bool             `json:"orderingVerified"`
	CrcVerified         bool             `json:"crcVerified"`
	IdempotenceEnabled  bool             `json:"idempotenceEnabled"`
	TransactionsEnabled bool             `json:"transactionsEnabled"`
	Verdict             string           `json:"verdict,omitempty"`
	Timeline            []IntegrityEvent `json:"timeline,omitempty"`
}

type IntegrityEvent struct {
	TimestampMs int64  `json:"timestampMs"`
	Type        string `json:"type"`
	Detail      string `json:"detail"`
}

type LostRange struct {
	FromSeq int64 `json:"fromSeq"`
	ToSeq   int64 `json:"toSeq"`
	Count   int64 `json:"count"`
}

// PagedTests from GET /api/tests (paginated)
type PagedTests struct {
	Content    []TestRun `json:"content"`
	Page       int       `json:"page"`
	Size       int       `json:"size"`
	TotalItems int       `json:"totalItems"`
	TotalPages int       `json:"totalPages"`
}

// ReportSummary from GET /api/tests/:id/report/summary
type ReportSummary struct {
	TotalRecords            float64 `json:"totalRecords"`
	AvgThroughputRecPerSec  float64 `json:"avgThroughputRecPerSec"`
	PeakThroughputRecPerSec float64 `json:"peakThroughputRecPerSec"`
	AvgThroughputMBPerSec   float64 `json:"avgThroughputMBPerSec"`
	AvgLatencyMs            float64 `json:"avgLatencyMs"`
	P50LatencyMs            float64 `json:"p50LatencyMs"`
	P95LatencyMs            float64 `json:"p95LatencyMs"`
	P99LatencyMs            float64 `json:"p99LatencyMs"`
	MaxLatencyMs            float64 `json:"maxLatencyMs"`
	ErrorRate               float64 `json:"errorRate"`
}

// Report from GET /api/tests/:id/report
type Report struct {
	Summary           *ReportSummary `json:"summary,omitempty"`
	OverallSlaVerdict *SlaVerdict    `json:"overallSlaVerdict,omitempty"`
	Phases            []ReportPhase  `json:"phases,omitempty"`
}

type ReportPhase struct {
	Name                string  `json:"name"`
	Status              string  `json:"status"`
	RecordsSent         float64 `json:"recordsSent"`
	ThroughputRecPerSec float64 `json:"throughputRecordsPerSec"`
	P99LatencyMs        float64 `json:"p99LatencyMs"`
	ErrorRate           float64 `json:"errorRate"`
}

type SlaVerdict struct {
	Grade        string         `json:"grade"`
	Passed       bool           `json:"passed"`
	Violated     bool           `json:"violated"`
	Violations   []SlaViolation `json:"violations,omitempty"`
	TotalChecks  int            `json:"totalChecks"`
	PassedChecks int            `json:"passedChecks"`
}

type SlaViolation struct {
	Metric     string  `json:"metric"`
	MetricName string  `json:"metricName"`
	Constraint string  `json:"constraint"`
	Threshold  float64 `json:"threshold"`
	Actual     float64 `json:"actual"`
	Severity   string  `json:"severity"`
}

// TrendResponse from GET /api/trends
type TrendResponse struct {
	Phase       string       `json:"phase,omitempty"`
	Baseline    float64      `json:"baseline"`
	DataPoints  []DataPoint  `json:"dataPoints,omitempty"`
	Regressions []Regression `json:"regressions,omitempty"`
}

type DataPoint struct {
	RunID     string  `json:"runId"`
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

type Regression struct {
	RunID            string  `json:"runId"`
	Value            float64 `json:"value"`
	Baseline         float64 `json:"baseline"`
	DeviationPercent float64 `json:"deviationPercent"`
}

// PhaseTrendResponse from GET /api/trends/breakdown
type PhaseTrendResponse struct {
	TestType string       `json:"testType"`
	Metric   string       `json:"metric"`
	Phases   []PhaseTrend `json:"phases,omitempty"`
}

type PhaseTrend struct {
	Phase       string       `json:"phase"`
	DataPoints  []DataPoint  `json:"dataPoints,omitempty"`
	Baseline    float64      `json:"baseline"`
	Regressions []Regression `json:"regressions,omitempty"`
}

// BrokerMetricsResponse from GET /api/tests/{id}/report/brokers
type BrokerMetricsResponse struct {
	BrokerID                  int                  `json:"brokerId"`
	Host                      string               `json:"host"`
	Rack                      string               `json:"rack,omitempty"`
	IsController              bool                 `json:"isController"`
	LeaderPartitions          int                  `json:"leaderPartitions"`
	ReplicaPartitions         int                  `json:"replicaPartitions"`
	IsrPartitions             int                  `json:"isrPartitions"`
	UnderReplicatedPartitions int                  `json:"underReplicatedPartitions"`
	TotalPartitions           int                  `json:"totalPartitions"`
	LeaderSharePct            float64              `json:"leaderSharePercent"`
	SkewPercent               float64              `json:"skewPercent"`
	Skewed                    bool                 `json:"skewed"`
	Metrics                   BrokerMetricsSummary `json:"metrics"`
}

type BrokerMetricsSummary struct {
	TotalRecords            int64   `json:"totalRecords"`
	AvgThroughputRecPerSec  float64 `json:"avgThroughputRecPerSec"`
	PeakThroughputRecPerSec float64 `json:"peakThroughputRecPerSec"`
	AvgThroughputMBPerSec   float64 `json:"avgThroughputMBPerSec"`
	AvgLatencyMs            float64 `json:"avgLatencyMs"`
	P50LatencyMs            float64 `json:"p50LatencyMs"`
	P95LatencyMs            float64 `json:"p95LatencyMs"`
	P99LatencyMs            float64 `json:"p99LatencyMs"`
	P999LatencyMs           float64 `json:"p999LatencyMs"`
	MaxLatencyMs            float64 `json:"maxLatencyMs"`
}

// BrokerTrendResponse from GET /api/trends/broker
type BrokerTrendResponse struct {
	BrokerID    int          `json:"brokerId"`
	TestType    string       `json:"testType"`
	Metric      string       `json:"metric"`
	Baseline    float64      `json:"baseline"`
	DataPoints  []DataPoint  `json:"dataPoints,omitempty"`
	Regressions []Regression `json:"regressions,omitempty"`
}

// ClusterSnapshotResponse from GET /api/tests/{id}/report/snapshot
type ClusterSnapshotResponse struct {
	ClusterID    string                `json:"clusterId"`
	BrokerCount  int                   `json:"brokerCount"`
	ControllerID int                   `json:"controllerId"`
	Brokers      []SnapshotBrokerInfo  `json:"brokers,omitempty"`
	Leaders      []PartitionAssignment `json:"leaders,omitempty"`
}

type SnapshotBrokerInfo struct {
	ID   int    `json:"id"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Rack string `json:"rack,omitempty"`
}

type PartitionAssignment struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	LeaderID  int    `json:"leaderId"`
	Replicas  []int  `json:"replicas,omitempty"`
	ISR       []int  `json:"isr,omitempty"`
}

// Schedule from GET /api/schedules and GET /api/schedules/:id
type Schedule struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	CronExpression string      `json:"cronExpression"`
	Enabled        bool        `json:"enabled"`
	LastRunID      string      `json:"lastRunId"`
	LastRunAt      string      `json:"lastRunAt"`
	CreatedAt      string      `json:"createdAt"`
	TestRequest    interface{} `json:"testRequest,omitempty"`
}

// CreateScheduleRequest for POST /api/schedules
type CreateScheduleRequest struct {
	Name           string      `json:"name"`
	CronExpression string      `json:"cronExpression"`
	Enabled        bool        `json:"enabled"`
	TestRequest    interface{} `json:"testRequest"`
}

// ResilienceResult from POST /api/resilience
type ResilienceResult struct {
	Status           string             `json:"status"`
	ChaosOutcome     *ChaosOutcome      `json:"chaosOutcome,omitempty"`
	ImpactDeltas     map[string]float64 `json:"impactDeltas,omitempty"`
	PreChaosSummary  *ReportSummary     `json:"preChaosSummary,omitempty"`
	PostChaosSummary *ReportSummary     `json:"postChaosSummary,omitempty"`
}

type ChaosOutcome struct {
	EngineName     string      `json:"engineName"`
	ExperimentName string      `json:"experimentName"`
	Verdict        string      `json:"verdict"`
	ChaosDuration  json.Number `json:"chaosDuration"`
	FailureReason  string      `json:"failureReason"`
	ProbeSuccess   string      `json:"probeSuccessPercentage,omitempty"`
	FailStep       string      `json:"failStep,omitempty"`
	Phase          string      `json:"phase,omitempty"`
}

// CreateTestRequest for POST /api/tests
type CreateTestRequest struct {
	TestType string    `json:"type"`
	Backend  string    `json:"backend,omitempty"`
	Spec     *TestSpec `json:"spec,omitempty"`
}

type TestSpec struct {
	Records            int    `json:"numRecords,omitempty"`
	ParallelProducers  int    `json:"numProducers,omitempty"`
	RecordSizeBytes    int    `json:"recordSize,omitempty"`
	DurationSeconds    int    `json:"durationMs,omitempty"`
	Topic              string `json:"topic,omitempty"`
	Acks               string `json:"acks,omitempty"`
	BatchSize          int    `json:"batchSize,omitempty"`
	LingerMs           int    `json:"lingerMs,omitempty"`
	CompressionType    string `json:"compressionType,omitempty"`
	NumConsumers       int    `json:"numConsumers,omitempty"`
	ReplicationFactor  int    `json:"replicationFactor,omitempty"`
	Partitions         int    `json:"partitions,omitempty"`
	MinInsyncReplicas  int    `json:"minInsyncReplicas,omitempty"`
	ConsumerGroup      string `json:"consumerGroup,omitempty"`
	TargetThroughput   int    `json:"targetThroughput,omitempty"`
	FetchMinBytes      int    `json:"fetchMinBytes,omitempty"`
	FetchMaxWaitMs     int    `json:"fetchMaxWaitMs,omitempty"`
	EnableIdempotence  bool   `json:"enableIdempotence,omitempty"`
	EnableTransactions bool   `json:"enableTransactions,omitempty"`
	EnableCrc          bool   `json:"enableCrc,omitempty"`
}

// TopicDetail from GET /api/cluster/topics/{name}
type TopicDetail struct {
	Name              string            `json:"name"`
	Partitions        int               `json:"partitions"`
	ReplicationFactor int               `json:"replicationFactor"`
	Internal          bool              `json:"internal"`
	Configs           map[string]string `json:"configs,omitempty"`
	PartitionInfo     []PartitionInfo   `json:"partitionInfo,omitempty"`
}

type PartitionInfo struct {
	Partition       int   `json:"partition"`
	Leader          int   `json:"leader"`
	Replicas        []int `json:"replicas"`
	ISR             []int `json:"isr"`
	UnderReplicated bool  `json:"underReplicated"`
}

// ConsumerGroupSummary from GET /api/cluster/groups
type ConsumerGroupSummary struct {
	GroupID string `json:"groupId"`
	State   string `json:"state"`
	Members int    `json:"members"`
}

// ConsumerGroupDetail from GET /api/cluster/groups/{id}
type ConsumerGroupDetail struct {
	GroupID  string               `json:"groupId"`
	State    string               `json:"state"`
	Members  int                  `json:"members"`
	Offsets  []GroupPartitionInfo `json:"offsets"`
	TotalLag int64                `json:"totalLag"`
}

type GroupPartitionInfo struct {
	Topic         string `json:"topic"`
	Partition     int    `json:"partition"`
	CurrentOffset int64  `json:"currentOffset"`
	EndOffset     int64  `json:"endOffset"`
	Lag           int64  `json:"lag"`
}

// BrokerConfig from GET /api/cluster/brokers/{id}/configs
type BrokerConfig struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Source   string `json:"source"`
	ReadOnly bool   `json:"readOnly"`
}

// ClusterHealthReport from GET /api/cluster/check
type ClusterHealthReport struct {
	ClusterID       string                `json:"clusterId"`
	Brokers         int                   `json:"brokers"`
	ControllerID    int                   `json:"controllerId"`
	Topics          int                   `json:"topics"`
	Partitions      int                   `json:"partitions"`
	ConsumerGroups  int                   `json:"consumerGroups"`
	PartitionHealth PartitionHealthReport `json:"partitionHealth"`
	Status          string                `json:"status"`
}

type PartitionHealthReport struct {
	UnderReplicated int                      `json:"underReplicated"`
	Offline         int                      `json:"offline"`
	Problems        []map[string]interface{} `json:"problems,omitempty"`
}

// DisruptionRunResponse from POST /api/disruptions
type DisruptionRunResponse struct {
	ID     string           `json:"id"`
	Report DisruptionReport `json:"report"`
}

type DisruptionReport struct {
	PlanName           string             `json:"planName"`
	Status             string             `json:"status"`
	StepReports        []StepReport       `json:"stepReports,omitempty"`
	Summary            *DisruptionSummary `json:"summary,omitempty"`
	ValidationWarnings []string           `json:"validationWarnings,omitempty"`
	SlaVerdict         *SlaVerdict        `json:"slaVerdict,omitempty"`
}

type StepReport struct {
	StepName              string             `json:"stepName"`
	DisruptionType        string             `json:"disruptionType"`
	ChaosOutcome          *ChaosOutcome      `json:"chaosOutcome,omitempty"`
	PodTimeline           []PodEvent         `json:"podTimeline,omitempty"`
	TimeToFirstReady      string             `json:"timeToFirstReady,omitempty"`
	TimeToAllReady        string             `json:"timeToAllReady,omitempty"`
	StrimziRecoveryTime   string             `json:"strimziRecoveryTime,omitempty"`
	PreDisruptionMetrics  *ReportSummary     `json:"preDisruptionMetrics,omitempty"`
	PostDisruptionMetrics *ReportSummary     `json:"postDisruptionMetrics,omitempty"`
	ImpactDeltas          map[string]float64 `json:"impactDeltas,omitempty"`
	TargetedLeaderBroker  *int               `json:"targetedLeaderBrokerId,omitempty"`
	IsrMetrics            *IsrMetrics        `json:"isrMetrics,omitempty"`
	LagMetrics            *LagMetrics        `json:"lagMetrics,omitempty"`
	RolledBack            bool               `json:"rolledBack,omitempty"`
	RollbackReason        string             `json:"rollbackReason,omitempty"`
}

type PodEvent struct {
	Timestamp string `json:"timestamp"`
	PodName   string `json:"podName"`
	EventType string `json:"eventType"`
	Phase     string `json:"phase"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
}

type DisruptionSummary struct {
	TotalSteps               int     `json:"totalSteps"`
	PassedSteps              int     `json:"passedSteps"`
	WorstRecovery            string  `json:"worstRecovery,omitempty"`
	AvgThroughputDegradation float64 `json:"avgThroughputDegradation"`
	MaxP99LatencySpike       float64 `json:"maxP99LatencySpike"`
	SlaViolated              bool    `json:"slaViolated"`
	WorstIsrRecovery         string  `json:"worstIsrRecovery,omitempty"`
	PeakConsumerLag          int64   `json:"peakConsumerLag,omitempty"`
}

type DisruptionTypeInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type DisruptionTimeline struct {
	Step             string     `json:"step"`
	Type             string     `json:"type"`
	Events           []PodEvent `json:"events"`
	TimeToFirstReady string     `json:"timeToFirstReady"`
	TimeToAllReady   string     `json:"timeToAllReady"`
}

type IsrMetrics struct {
	TimeToFullIsr       string     `json:"timeToFullIsr,omitempty"`
	MinIsrDepth         int        `json:"minIsrDepth"`
	UnderReplicatedPeak int        `json:"underReplicatedPeakCount"`
	TotalPartitions     int        `json:"totalPartitions"`
	Timeline            []IsrEntry `json:"timeline,omitempty"`
}

type IsrEntry struct {
	Timestamp         string `json:"timestamp"`
	Topic             string `json:"topic"`
	Partition         int    `json:"partition"`
	LeaderId          int    `json:"leaderId"`
	Isr               []int  `json:"isr"`
	ReplicationFactor int    `json:"replicationFactor"`
}

type LagMetrics struct {
	BaselineLag       int64      `json:"baselineLag"`
	PeakLag           int64      `json:"peakLag"`
	TimeToLagRecovery string     `json:"timeToLagRecovery,omitempty"`
	Timeline          []LagEntry `json:"timeline,omitempty"`
}

type LagEntry struct {
	Timestamp   string           `json:"timestamp"`
	GroupId     string           `json:"groupId"`
	TotalLag    int64            `json:"totalLag"`
	PerTopicLag map[string]int64 `json:"perTopicLag,omitempty"`
}

type KafkaMetricsEntry struct {
	Step                   string      `json:"step"`
	DisruptionType         string      `json:"disruptionType"`
	TargetedLeaderBrokerId *int        `json:"targetedLeaderBrokerId,omitempty"`
	Isr                    *IsrMetrics `json:"isr,omitempty"`
	Lag                    *LagMetrics `json:"lag,omitempty"`
}

type DryRunResult struct {
	WouldSucceed bool          `json:"wouldSucceed"`
	TotalBrokers int           `json:"totalBrokers"`
	Steps        []StepPreview `json:"steps,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
	Errors       []string      `json:"errors,omitempty"`
}

type StepPreview struct {
	Name             string   `json:"name"`
	DisruptionType   string   `json:"disruptionType"`
	TargetPod        string   `json:"targetPod,omitempty"`
	ResolvedLeaderId *int     `json:"resolvedLeaderId,omitempty"`
	AffectedPods     []string `json:"affectedPods,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type DisruptionListEntry struct {
	ID        string `json:"id"`
	PlanName  string `json:"planName"`
	Status    string `json:"status"`
	SlaGrade  string `json:"slaGrade"`
	CreatedAt string `json:"createdAt"`
}

type PlaybookEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Steps       int    `json:"steps"`
}

type DisruptionScheduleEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CronExpression string `json:"cronExpression"`
	Enabled        bool   `json:"enabled"`
	PlaybookName   string `json:"playbookName"`
	LastRunID      string `json:"lastRunId"`
	LastRunAt      string `json:"lastRunAt"`
	CreatedAt      string `json:"createdAt"`
}

type DisruptionSSEEvent struct {
	DisruptionID string `json:"disruptionId"`
	Type         string `json:"type"`
	StepName     string `json:"stepName"`
	Message      string `json:"message"`
	Timestamp    string `json:"timestamp"`
}

type WebhookRegistration struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Events string `json:"events"`
}

// KafkaTopic from GET /api/kafka/topics
type KafkaTopic struct {
	Name              string      `json:"name"`
	Internal          bool        `json:"internal"`
	Partitions        int         `json:"partitions"`
	ReplicationFactor int         `json:"replicationFactor"`
	UnderReplicated   interface{} `json:"underReplicated"`
}

// KafkaBrokerDetail is embedded in the cluster describe response
type KafkaBrokerDetail struct {
	ID   interface{} `json:"id"`
	Host string      `json:"host"`
	Port interface{} `json:"port"`
	Rack interface{} `json:"rack"`
}

// KafkaRecord from GET /api/kafka/consume/:topic
type KafkaRecord struct {
	Offset    interface{} `json:"offset"`
	Partition interface{} `json:"partition"`
	Timestamp interface{} `json:"timestamp"`
	Key       interface{} `json:"key"`
	Value     interface{} `json:"value"`
}

// ProduceMeta from POST /api/kafka/produce/:topic
type ProduceMeta struct {
	Topic     string      `json:"topic"`
	Partition interface{} `json:"partition"`
	Offset    interface{} `json:"offset"`
	Timestamp interface{} `json:"timestamp"`
}

type CreateTopicRequest struct {
	Name              string            `json:"name"`
	Partitions        int               `json:"partitions"`
	ReplicationFactor int               `json:"replicationFactor"`
	Configs           map[string]string `json:"configs,omitempty"`
}

type AlterTopicRequest struct {
	Configs map[string]string `json:"configs"`
}

type SetBaselineRequest struct {
	RunID string `json:"runId"`
}

type BaselineEntry struct {
	TestType string `json:"testType"`
	RunID    string `json:"runId"`
	SetAt    string `json:"setAt"`
}

type RegressionReport struct {
	RunID              string                 `json:"runId"`
	BaselineID         string                 `json:"baselineId"`
	TestType           string                 `json:"testType"`
	RegressionDetected bool                   `json:"regressionDetected"`
	Deltas             map[string]MetricDelta `json:"deltas"`
	Warnings           []string               `json:"warnings,omitempty"`
}

type MetricDelta struct {
	Baseline float64  `json:"baseline"`
	Current  float64  `json:"current"`
	Delta    *float64 `json:"delta,omitempty"`
}

type TuningReport struct {
	TestType       string       `json:"testType"`
	ParameterName  string       `json:"parameterName"`
	Steps          []TuningStep `json:"steps"`
	BestStepIndex  int          `json:"bestStepIndex"`
	Recommendation string       `json:"recommendation"`
}

type TuningStep struct {
	StepIndex      int                `json:"stepIndex"`
	Label          string             `json:"label"`
	Config         map[string]any     `json:"config"`
	Metrics        map[string]float64 `json:"metrics,omitempty"`
	TopicCleanupMs int64              `json:"topicCleanupMs"`
}

type TuningTypeInfo struct {
	Type        string `json:"type"`
	Parameter   string `json:"parameter"`
	Steps       int    `json:"steps"`
	Description string `json:"description"`
}

type AuditEntry struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	EventType string `json:"eventType"`
	Target    string `json:"target"`
	Details   string `json:"details"`
	Timestamp string `json:"timestamp"`
}
