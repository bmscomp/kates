package client

import (
	"context"
)

// ComplianceReport is what POST /api/conformity/check returns: every rule a
// KafkaReference declares, with its status, and a verdict.
type ComplianceReport struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Reference  string                 `json:"reference"`
	Target     ComplianceTarget       `json:"target"`
	CheckedAt  string                 `json:"checkedAt"`
	Cluster    ComplianceClusterState `json:"cluster"`
	Verdict    string                 `json:"verdict"`
	Summary    ComplianceSummary      `json:"summary"`
	Results    []ComplianceResult     `json:"results"`
}

type ComplianceTarget struct {
	Platform  string `json:"platform"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	ClusterID string `json:"clusterId,omitempty"`
}

type ComplianceClusterState struct {
	Ready              *bool  `json:"ready,omitempty"`
	Generation         *int64 `json:"generation,omitempty"`
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`
	PodsNotReady       *int   `json:"podsNotReady,omitempty"`
}

type ComplianceSummary struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Warn    int `json:"warn"`
	Waived  int `json:"waived"`
	Unknown int `json:"unknown"`
}

type ComplianceResult struct {
	Rule     string            `json:"rule"`
	Status   string            `json:"status"`
	Severity string            `json:"severity"`
	Expected map[string]any    `json:"expected"`
	Observed any               `json:"observed,omitempty"`
	Subjects []string          `json:"subjects,omitempty"`
	Detail   string            `json:"detail,omitempty"`
	Source   string            `json:"source,omitempty"`
	Waiver   *ComplianceWaived `json:"waiver,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// ComplianceWaived is the waiver that covers a rule, and the status the rule
// had without it.
type ComplianceWaived struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
	By     string `json:"by"`
	Until  string `json:"until"`
}

// ComplianceCheck sends a reference, already converted to JSON values, and
// returns the report. The check reads the cluster only; it is a POST because
// the reference is a body.
func (c *Client) ComplianceCheck(ctx context.Context, reference any) (*ComplianceReport, error) {
	report, err := postJSON[ComplianceReport](c, ctx, "/api/conformity/check", reference)
	if err != nil {
		return nil, err
	}
	return &report, nil
}
