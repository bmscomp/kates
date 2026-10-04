package migrate

import (
	"reflect"
	"strings"
	"testing"
)

// kmm2Running is `kubectl get kafkamirrormaker2 mm2-m282-430-mirror-maker2
// -o json` on a healthy mirror, trimmed to the fields that matter plus the
// neighbours the operator writes around them.
const kmm2Running = `{
  "apiVersion": "kafka.strimzi.io/v1",
  "kind": "KafkaMirrorMaker2",
  "metadata": {
    "name": "mm2-m282-430-mirror-maker2",
    "namespace": "kafka",
    "generation": 2,
    "labels": {"kates.io/lab": "m282-430", "kates.io/lab-role": "mirror"}
  },
  "spec": {"version": "4.3.0", "replicas": 1},
  "status": {
    "conditions": [
      {"lastTransitionTime": "2026-09-08T10:14:02.117653251Z", "status": "True", "type": "Ready"}
    ],
    "connectorPlugins": [
      {"class": "org.apache.kafka.connect.mirror.MirrorCheckpointConnector", "type": "source", "version": "4.3.0"},
      {"class": "org.apache.kafka.connect.mirror.MirrorSourceConnector", "type": "source", "version": "4.3.0"}
    ],
    "connectors": [
      {
        "connector": {"state": "RUNNING", "worker_id": "mm2-m282-430-mirror-maker2-mirrormaker2-0.mm2-m282-430-mirror-maker2-mirrormaker2.kafka.svc:8083"},
        "name": "source->target.MirrorCheckpointConnector",
        "tasks": [{"id": 0, "state": "RUNNING", "worker_id": "mm2-m282-430-mirror-maker2-mirrormaker2-0.mm2-m282-430-mirror-maker2-mirrormaker2.kafka.svc:8083"}],
        "type": "source"
      },
      {
        "connector": {"state": "RUNNING", "worker_id": "mm2-m282-430-mirror-maker2-mirrormaker2-0.mm2-m282-430-mirror-maker2-mirrormaker2.kafka.svc:8083"},
        "name": "source->target.MirrorSourceConnector",
        "tasks": [
          {"id": 0, "state": "RUNNING", "worker_id": "mm2-m282-430-mirror-maker2-mirrormaker2-0.mm2-m282-430-mirror-maker2-mirrormaker2.kafka.svc:8083"},
          {"id": 1, "state": "RUNNING", "worker_id": "mm2-m282-430-mirror-maker2-mirrormaker2-0.mm2-m282-430-mirror-maker2-mirrormaker2.kafka.svc:8083"}
        ],
        "type": "source"
      }
    ],
    "labelSelector": "strimzi.io/cluster=mm2-m282-430-mirror-maker2,strimzi.io/name=mm2-m282-430-mirror-maker2-mirrormaker2,strimzi.io/kind=KafkaMirrorMaker2",
    "observedGeneration": 2,
    "replicas": 1,
    "url": "http://mm2-m282-430-mirror-maker2-mirrormaker2-api.kafka.svc:8083"
  }
}`

// kmm2Cutover is the same mirror after values-cutover.yaml: the source
// connector STOPPED with its tasks released, the checkpoint connector still
// running, and the operator one generation behind the spec.
const kmm2Cutover = `{
  "metadata": {"name": "mm2-m282-430-mirror-maker2", "generation": 3},
  "status": {
    "conditions": [{"status": "True", "type": "Ready"}],
    "connectors": [
      {"connector": {"state": "STOPPED"}, "name": "source->target.MirrorSourceConnector", "tasks": [], "type": "source"},
      {"connector": {"state": "RUNNING"}, "name": "source->target.MirrorCheckpointConnector", "tasks": [{"id": 0, "state": "RUNNING"}], "type": "source"}
    ],
    "observedGeneration": 2,
    "replicas": 1
  }
}`

// kmm2NotReady is a mirror whose workers cannot start: Strimzi reports a
// NotReady condition with the reason, not Ready=False.
const kmm2NotReady = `{
  "metadata": {"name": "mm2-m282-430-mirror-maker2", "generation": 1},
  "status": {
    "conditions": [
      {"lastTransitionTime": "2026-09-08T10:02:41.5Z", "message": "Secret kates-mm2 not found in namespace kafka", "reason": "InvalidResourceException", "status": "True", "type": "NotReady"}
    ],
    "observedGeneration": 1,
    "replicas": 0
  }
}`

// kmm2FailedTask is Ready with the source connector RUNNING and one task
// FAILED on a source-side ACL — the "Ready means nothing" case.
const kmm2FailedTask = `{
  "metadata": {"generation": 1},
  "status": {
    "conditions": [
      {"status": "True", "type": "Ready"},
      {"status": "True", "type": "Warning", "reason": "ConnectorFailed", "message": "Task 0 of connector source->target.MirrorSourceConnector failed"}
    ],
    "connectors": [
      {"connector": {"state": "RUNNING"}, "name": "source->target.MirrorSourceConnector",
       "tasks": [{"id": 0, "state": "FAILED", "trace": "org.apache.kafka.common.errors.TopicAuthorizationException: Not authorized to access topics: [kates.orders]\n"}, {"id": 1, "state": "RUNNING"}]},
      {"connector": {"state": "RUNNING"}, "name": "source->target.MirrorCheckpointConnector", "tasks": [{"id": 0, "state": "RUNNING"}]}
    ],
    "observedGeneration": 1,
    "replicas": 1
  }
}`

func TestParseMirrorStatusRunning(t *testing.T) {
	s, err := ParseMirrorStatus([]byte(kmm2Running))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready || s.Reason != "" || s.Message != "" {
		t.Errorf("Ready=%v reason=%q message=%q", s.Ready, s.Reason, s.Message)
	}
	if s.Replicas != 1 || s.Generation != 2 || s.ObservedGeneration != 2 {
		t.Errorf("replicas %d generation %d observed %d", s.Replicas, s.Generation, s.ObservedGeneration)
	}
	want := []ConnectorStatus{
		{Name: "source->target.MirrorCheckpointConnector", State: "RUNNING", Tasks: []string{"RUNNING"}},
		{Name: "source->target.MirrorSourceConnector", State: "RUNNING", Tasks: []string{"RUNNING", "RUNNING"}},
	}
	if !reflect.DeepEqual(s.Connectors, want) {
		t.Errorf("connectors = %+v\nwant %+v", s.Connectors, want)
	}
	if !s.AllRunning() || s.RunningCount() != 2 || s.FailedTasks() != 0 {
		t.Errorf("AllRunning=%v RunningCount=%d FailedTasks=%d", s.AllRunning(), s.RunningCount(), s.FailedTasks())
	}
	if s.Stopped(SourceConnectorSuffix) || s.Stopped(CheckpointConnectorSuffix) {
		t.Error("nothing is stopped on a running mirror")
	}
	if got := s.Summary(); got != "source->target.MirrorCheckpointConnector=RUNNING source->target.MirrorSourceConnector=RUNNING" {
		t.Errorf("Summary = %q", got)
	}
	if c, ok := s.Connector(SourceConnectorSuffix); !ok || len(c.Tasks) != 2 {
		t.Errorf("Connector(source) = %+v, %v", c, ok)
	}
	if _, ok := s.Connector("HeartbeatConnector"); ok {
		t.Error("the v1 API has no heartbeat connector")
	}
}

func TestParseMirrorStatusCutover(t *testing.T) {
	s, err := ParseMirrorStatus([]byte(kmm2Cutover))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready {
		t.Error("a cut-over mirror stays Ready")
	}
	if !s.Stopped(SourceConnectorSuffix) || s.Stopped(CheckpointConnectorSuffix) {
		t.Errorf("Stopped(source)=%v Stopped(checkpoint)=%v", s.Stopped(SourceConnectorSuffix), s.Stopped(CheckpointConnectorSuffix))
	}
	if s.AllRunning() || s.RunningCount() != 1 {
		t.Errorf("AllRunning=%v RunningCount=%d on a cutover", s.AllRunning(), s.RunningCount())
	}
	if s.Generation != 3 || s.ObservedGeneration != 2 {
		t.Errorf("generation %d observed %d", s.Generation, s.ObservedGeneration)
	}
	if c, _ := s.Connector(SourceConnectorSuffix); len(c.Tasks) != 0 {
		t.Errorf("a STOPPED connector releases its tasks, got %v", c.Tasks)
	}
}

func TestParseMirrorStatusNotReadyAndFailures(t *testing.T) {
	s, err := ParseMirrorStatus([]byte(kmm2NotReady))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready || s.Reason != "InvalidResourceException" || !strings.Contains(s.Message, "kates-mm2 not found") {
		t.Errorf("NotReady: Ready=%v reason=%q message=%q", s.Ready, s.Reason, s.Message)
	}
	if s.AllRunning() || len(s.Connectors) != 0 || s.Replicas != 0 {
		t.Errorf("NotReady has connectors %v replicas %d", s.Connectors, s.Replicas)
	}

	s, err = ParseMirrorStatus([]byte(kmm2FailedTask))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready {
		t.Error("Ready is true even with a failed task — that is the point")
	}
	if s.AllRunning() || s.RunningCount() != 2 || s.FailedTasks() != 1 {
		t.Errorf("AllRunning=%v RunningCount=%d FailedTasks=%d", s.AllRunning(), s.RunningCount(), s.FailedTasks())
	}
	if s.Reason != "ConnectorFailed" {
		t.Errorf("Warning reason not surfaced: %q", s.Reason)
	}
	c, _ := s.Connector(SourceConnectorSuffix)
	if !strings.Contains(c.Trace, "TopicAuthorizationException") || c.Running() || c.FailedTasks() != 1 {
		t.Errorf("failed connector = %+v", c)
	}
}

func TestParseMirrorStatusEdgeCases(t *testing.T) {
	s, err := ParseMirrorStatus([]byte(`{"metadata": {"name": "new", "generation": 1}, "spec": {}}`))
	if err != nil {
		t.Fatalf("a CR without status must parse: %v", err)
	}
	if s.Ready || len(s.Connectors) != 0 || s.AllRunning() || s.Generation != 1 {
		t.Errorf("fresh CR = %+v", s)
	}
	if _, err := ParseMirrorStatus([]byte(`{"status": [}`)); err == nil {
		t.Error("malformed JSON must fail")
	}
	if _, err := ParseMirrorStatus(nil); err == nil {
		t.Error("empty input must fail")
	}
	ready := `{"status": {"conditions": [{"type": "Ready", "status": "False", "reason": "Reconciling", "message": "in progress"}]}}`
	s, err = ParseMirrorStatus([]byte(ready))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready || s.Reason != "Reconciling" {
		t.Errorf("Ready=False: %+v", s)
	}
	if (MirrorStatus{}).AllRunning() {
		t.Error("no connectors is not all running")
	}
	if (ConnectorStatus{State: "RUNNING"}).Running() != true {
		t.Error("a RUNNING connector without tasks yet counts as running")
	}
	if (ConnectorStatus{State: "PAUSED", Tasks: []string{"PAUSED"}}).Running() {
		t.Error("PAUSED is not running")
	}
}
