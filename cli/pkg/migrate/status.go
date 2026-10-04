package migrate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Connector and task states as Kafka Connect reports them.
const (
	StateRunning    = "RUNNING"
	StatePaused     = "PAUSED"
	StateStopped    = "STOPPED"
	StateFailed     = "FAILED"
	StateUnassigned = "UNASSIGNED"

	// The suffixes of the two connectors a mirror runs, named
	// <source>-><target>.<suffix> by MirrorMaker 2.
	SourceConnectorSuffix     = "MirrorSourceConnector"
	CheckpointConnectorSuffix = "MirrorCheckpointConnector"
)

// ConnectorStatus is one entry of a KafkaMirrorMaker2's status.connectors,
// which carries Kafka Connect's REST status verbatim.
type ConnectorStatus struct {
	// Name is source->target.MirrorSourceConnector or …CheckpointConnector.
	Name string
	// State is the connector's own state.
	State string
	// Tasks are the task states in task order.
	Tasks []string
	// Trace is the first stack trace the connector or a task reported, empty
	// when none did.
	Trace string
}

// Running reports whether the connector and every task of it is RUNNING. A
// connector with no tasks yet counts as running: Connect assigns tasks
// after the connector starts.
func (c ConnectorStatus) Running() bool {
	if c.State != StateRunning {
		return false
	}
	for _, t := range c.Tasks {
		if t != StateRunning {
			return false
		}
	}
	return true
}

// FailedTasks counts the tasks in FAILED.
func (c ConnectorStatus) FailedTasks() int {
	n := 0
	for _, t := range c.Tasks {
		if t == StateFailed {
			n++
		}
	}
	return n
}

// MirrorStatus is what a KafkaMirrorMaker2 object says about itself.
type MirrorStatus struct {
	// Ready is the Ready condition being True. It means the Connect workers
	// are up — nothing more; Connectors say whether the mirror runs.
	Ready bool
	// Reason and Message come from the NotReady or Warning condition when
	// there is one, else from Ready.
	Reason, Message string
	Connectors      []ConnectorStatus
	// Replicas is status.replicas, the worker count the operator reports.
	Replicas int
	// ObservedGeneration and Generation tell whether the status describes
	// the current spec (a just-applied cutover is not reflected until they
	// match).
	ObservedGeneration, Generation int64
}

// kmm2 is the subset of a KafkaMirrorMaker2 object the status needs.
type kmm2 struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Status *struct {
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
		Connectors []struct {
			Name      string `json:"name"`
			Connector struct {
				State string `json:"state"`
				Trace string `json:"trace"`
			} `json:"connector"`
			Tasks []struct {
				ID    int    `json:"id"`
				State string `json:"state"`
				Trace string `json:"trace"`
			} `json:"tasks"`
		} `json:"connectors"`
		Replicas           int   `json:"replicas"`
		ObservedGeneration int64 `json:"observedGeneration"`
	} `json:"status"`
}

// ParseMirrorStatus reads a KafkaMirrorMaker2 object as `kubectl get
// kafkamirrormaker2 <name> -o json` prints it. An object without a status
// yet (just created) parses as not Ready with no connectors, not as an
// error; malformed JSON is an error.
func ParseMirrorStatus(kmm2JSON []byte) (MirrorStatus, error) {
	var obj kmm2
	if err := json.Unmarshal(kmm2JSON, &obj); err != nil {
		return MirrorStatus{}, fmt.Errorf("parse KafkaMirrorMaker2 JSON: %w", err)
	}
	s := MirrorStatus{Generation: obj.Metadata.Generation}
	if obj.Status == nil {
		return s, nil
	}
	s.Replicas = obj.Status.Replicas
	s.ObservedGeneration = obj.Status.ObservedGeneration
	// Strimzi reports trouble as a NotReady (or Warning) condition rather
	// than Ready=False, so the reason is looked for there first.
	var readyReason, readyMessage string
	for _, c := range obj.Status.Conditions {
		switch c.Type {
		case "Ready":
			if c.Status == "True" {
				s.Ready = true
			}
			readyReason, readyMessage = c.Reason, c.Message
		case "NotReady", "Warning":
			if s.Reason == "" && (c.Reason != "" || c.Message != "") {
				s.Reason, s.Message = c.Reason, c.Message
			}
		}
	}
	if s.Reason == "" && s.Message == "" {
		s.Reason, s.Message = readyReason, readyMessage
	}
	for _, c := range obj.Status.Connectors {
		cs := ConnectorStatus{Name: c.Name, State: c.Connector.State, Trace: c.Connector.Trace}
		for _, t := range c.Tasks {
			cs.Tasks = append(cs.Tasks, t.State)
			if cs.Trace == "" && t.Trace != "" {
				cs.Trace = t.Trace
			}
		}
		s.Connectors = append(s.Connectors, cs)
	}
	return s, nil
}

// AllRunning reports whether the operator lists at least one connector and
// every connector, with every task, is RUNNING. PAUSED and STOPPED are not
// running: a cutover is deliberate, and Stopped is how to check for it.
func (s MirrorStatus) AllRunning() bool {
	if len(s.Connectors) == 0 {
		return false
	}
	for _, c := range s.Connectors {
		if !c.Running() {
			return false
		}
	}
	return true
}

// RunningCount is the number of connectors in RUNNING (the scripts waited
// for two).
func (s MirrorStatus) RunningCount() int {
	n := 0
	for _, c := range s.Connectors {
		if c.State == StateRunning {
			n++
		}
	}
	return n
}

// Connector finds the connector whose name ends with suffix (a
// SourceConnectorSuffix or CheckpointConnectorSuffix).
func (s MirrorStatus) Connector(suffix string) (ConnectorStatus, bool) {
	for _, c := range s.Connectors {
		if strings.HasSuffix(c.Name, suffix) {
			return c, true
		}
	}
	return ConnectorStatus{}, false
}

// Stopped reports whether the connector with the given suffix is listed and
// STOPPED — the state a cutover puts the source connector in.
func (s MirrorStatus) Stopped(connectorSuffix string) bool {
	c, ok := s.Connector(connectorSuffix)
	return ok && c.State == StateStopped
}

// FailedTasks counts the FAILED tasks across every connector.
func (s MirrorStatus) FailedTasks() int {
	n := 0
	for _, c := range s.Connectors {
		n += c.FailedTasks()
	}
	return n
}

// Summary is a one-line rendering of the connector states,
// "source->target.MirrorSourceConnector=RUNNING …", in the order the
// operator lists them — the scripts' report detail for the connectors row.
func (s MirrorStatus) Summary() string {
	parts := make([]string, 0, len(s.Connectors))
	for _, c := range s.Connectors {
		parts = append(parts, c.Name+"="+c.State)
	}
	return strings.Join(parts, " ")
}
