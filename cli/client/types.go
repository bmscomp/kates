package client

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

// TestRun from GET /api/tests/:id and POST /api/tests
type TestRun struct {
	ID           string        `json:"id"`
	TestType     string        `json:"testType"`
	Status       string        `json:"status"`
	Backend      string        `json:"backend"`
	ScenarioName string        `json:"scenarioName"`
	CreatedAt    string        `json:"createdAt"`
	Results      []PhaseResult `json:"results,omitempty"`
}

type PhaseResult struct {
	PhaseName               string  `json:"phaseName"`
	Status                  string  `json:"status"`
	RecordsSent             float64 `json:"recordsSent"`
	ThroughputRecordsPerSec float64 `json:"throughputRecordsPerSec"`
	AvgLatencyMs            float64 `json:"avgLatencyMs"`
	P99LatencyMs            float64 `json:"p99LatencyMs"`
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
}

type SlaVerdict struct {
	Passed     bool           `json:"passed"`
	Violations []SlaViolation `json:"violations,omitempty"`
}

type SlaViolation struct {
	Metric    string  `json:"metric"`
	Threshold float64 `json:"threshold"`
	Actual    float64 `json:"actual"`
}

// TrendResponse from GET /api/trends
type TrendResponse struct {
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
	ExperimentName string `json:"experimentName"`
	Verdict        string `json:"verdict"`
	ChaosDuration  string `json:"chaosDuration"`
	FailureReason  string `json:"failureReason"`
}

// CreateTestRequest for POST /api/tests
type CreateTestRequest struct {
	TestType string    `json:"testType"`
	Backend  string    `json:"backend,omitempty"`
	Spec     *TestSpec `json:"spec,omitempty"`
}

type TestSpec struct {
	Records           int    `json:"records,omitempty"`
	ParallelProducers int    `json:"parallelProducers,omitempty"`
	RecordSizeBytes   int    `json:"recordSizeBytes,omitempty"`
	DurationSeconds   int    `json:"durationSeconds,omitempty"`
	Topic             string `json:"topic,omitempty"`
	Acks              string `json:"acks,omitempty"`
	BatchSize         int    `json:"batchSize,omitempty"`
	LingerMs          int    `json:"lingerMs,omitempty"`
	CompressionType   string `json:"compressionType,omitempty"`
	NumConsumers      int    `json:"numConsumers,omitempty"`
	ReplicationFactor int    `json:"replicationFactor,omitempty"`
	Partitions        int    `json:"partitions,omitempty"`
	MinInsyncReplicas int    `json:"minInsyncReplicas,omitempty"`
	ConsumerGroup     string `json:"consumerGroup,omitempty"`
	TargetThroughput  int    `json:"targetThroughput,omitempty"`
	FetchMinBytes     int    `json:"fetchMinBytes,omitempty"`
	FetchMaxWaitMs    int    `json:"fetchMaxWaitMs,omitempty"`
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
