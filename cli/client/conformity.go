package client

import (
	"context"
)

// ConformityReport is what POST /api/conformity/check returns: every rule a
// KafkaReference declares, with its status, and a verdict.
type ConformityReport struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Reference  string                 `json:"reference"`
	Target     ConformityTarget       `json:"target"`
	CheckedAt  string                 `json:"checkedAt"`
	Cluster    ConformityClusterState `json:"cluster"`
	Verdict    string                 `json:"verdict"`
	Summary    ConformitySummary      `json:"summary"`
	Results    []ConformityResult     `json:"results"`
}

type ConformityTarget struct {
	Platform  string `json:"platform"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	ClusterID string `json:"clusterId,omitempty"`
}

type ConformityClusterState struct {
	Ready              *bool  `json:"ready,omitempty"`
	Generation         *int64 `json:"generation,omitempty"`
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`
	PodsNotReady       *int   `json:"podsNotReady,omitempty"`
}

type ConformitySummary struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Warn    int `json:"warn"`
	Waived  int `json:"waived"`
	Unknown int `json:"unknown"`
}

type ConformityResult struct {
	Rule     string            `json:"rule"`
	Status   string            `json:"status"`
	Severity string            `json:"severity"`
	Expected map[string]any    `json:"expected"`
	Observed any               `json:"observed,omitempty"`
	Subjects []string          `json:"subjects,omitempty"`
	Detail   string            `json:"detail,omitempty"`
	Source   string            `json:"source,omitempty"`
	Waiver   *ConformityWaived `json:"waiver,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// ConformityWaived is the waiver that covers a rule, and the status the rule
// had without it.
type ConformityWaived struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
	By     string `json:"by"`
	Until  string `json:"until"`
}

// ConformityCheck sends a reference, already converted to JSON values, and
// returns the report. The check reads the cluster only; it is a POST because
// the reference is a body.
func (c *Client) ConformityCheck(ctx context.Context, reference any) (*ConformityReport, error) {
	report, err := postJSON[ConformityReport](c, ctx, "/api/conformity/check", reference)
	if err != nil {
		return nil, err
	}
	return &report, nil
}
