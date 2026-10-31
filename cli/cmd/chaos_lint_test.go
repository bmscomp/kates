package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/internal/compliance"
	"github.com/bmscomp/kates/cli/output"
)

func TestLintShippedResilienceFiles(t *testing.T) {
	files, err := filepath.Glob("../examples/resilience-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 12 {
		t.Fatalf("expected at least 12 resilience example files, got %d", len(files))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if ds := lintResilienceFile(src); len(ds) > 0 {
			t.Errorf("%s: got diagnostics %v, want none", f, diagKeys(ds))
		}
	}
}

func TestLintShippedPlaybookFiles(t *testing.T) {
	files, err := filepath.Glob("../../kates/src/main/resources/playbooks/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 6 {
		t.Fatalf("expected at least 6 playbook files, got %d", len(files))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if ds := lintDisruptionFile(src); len(ds) > 0 {
			t.Errorf("%s: got diagnostics %v, want none", f, diagKeys(ds))
		}
	}
}

func TestLintResilienceDiagnostics(t *testing.T) {
	src := `testRequest:
  type: LOAD
  spec:
    records: 1000
    minInsyncReplicas: 5
    replicationFactor: 3
    enableTransactions: true
    acks: "1"
chaosSpec:
  experimentName: network-latency
  disruptionType: NETWORK_LATENCY
  chaosDurationSec: 0
  networkLatencyMs: 50000
  targetLabel: "strimzi.io/cluster=<bad>"
  targetTopic: my-topic
  envOverrides:
    POD_NAME: kafka-0
    TARGET_NODE: ""
  probes:
    - name: bad-cmd-in-fault
      type: cmdProbe
      command: "echo ok"
      expectedOutput: "ok"
probes:
  - name: bad-kafka-probe
    type: kafkaProbe
    command: "unknown-check"
    comparator: ">="
    expectedOutput: "not-a-number"
    intervalSec: 0
`
	ds := lintResilienceFile([]byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code+":"+d.Path)
	}
	wantCodes := []string{
		"unknown-field:testRequest.spec.records",
		"isr-exceeds-rf:testRequest.spec.minInsyncReplicas",
		"idempotence-requires-acks-all:testRequest.spec.acks",
		"fault-requires-duration:chaosSpec.chaosDurationSec",
		"wrong-value:chaosSpec.networkLatencyMs",
		"invalid-label-selector:chaosSpec.targetLabel",
		"unused-field:chaosSpec.targetTopic",
		"invalid-env-override:chaosSpec.envOverrides.POD_NAME",
		"empty-env-override:chaosSpec.envOverrides.TARGET_NODE",
		"cmdprobe-in-faultspec:chaosSpec.probes[0].type",
		"invalid-kafka-probe-command:probes[0].command",
		"invalid-probe:probes[0].expectedOutput",
		"wrong-value:probes[0].intervalSec",
	}
	for _, want := range wantCodes {
		if !slices.Contains(codes, want) {
			t.Errorf("missing expected diagnostic %q in %v", want, codes)
		}
	}
}

func TestLintDisruptionDiagnostics(t *testing.T) {
	src := `name: "Bad Plan"
category: unknown-cat
autoRollback: false
steps:
  - name: "Step 1"
    steadyStateSec: -5
    observationWindowSec: 0
    faultSpec:
      experimentName: scale-down
      disruptionType: SCALE_DOWN
      chaosDurationSec: 0
      fillPercentage: 150
      nonExistentAttr: true
`
	ds := lintDisruptionFile([]byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code+":"+d.Path)
	}
	wantCodes := []string{
		"wrong-value:category",
		"scaledown-without-rollback:autoRollback",
		"wrong-value:steps[0].steadyStateSec",
		"wrong-value:steps[0].observationWindowSec",
		"zero-chaos-duration:steps[0].faultSpec.chaosDurationSec",
		"wrong-value:steps[0].faultSpec.fillPercentage",
		"unknown-field:steps[0].faultSpec.nonExistentAttr",
	}
	for _, want := range wantCodes {
		if !slices.Contains(codes, want) {
			t.Errorf("missing expected diagnostic %q in %v", want, codes)
		}
	}
}

func TestLintMultiPhaseTestRequest(t *testing.T) {
	src := `backend: trogdor
scenario:
  name: "Multi-Phase Test"
  baseSpec:
    numRecords: 10000
    enableTransactions: true
    enableIdempotence: false
  phases:
    - name: "Ramp Up"
      type: RAMP
      durationMs: 10000
      rampSteps: 1
    - name: "Steady"
      type: STEADY
      durationMs: 30000
      rampSteps: 4
  sla:
    p99LatencyMs: 25.0
    maxErrorRatePercent: 150.0
`
	ds := lintTestRequestFile([]byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code+":"+d.Path)
	}
	wantCodes := []string{
		"invalid-transactions-config:scenario.baseSpec.enableTransactions",
		"invalid-ramp-steps:scenario.phases[0].rampSteps",
		"unused-field:scenario.phases[1].rampSteps",
		"wrong-value:scenario.sla.maxErrorRatePercent",
	}
	for _, want := range wantCodes {
		if !slices.Contains(codes, want) {
			t.Errorf("missing expected diagnostic %q in %v", want, codes)
		}
	}
}

func TestValidateLabelSelector(t *testing.T) {
	valid := []string{
		"app=kafka",
		"strimzi.io/cluster=my-cluster,strimzi.io/kind=Kafka",
		"app.kubernetes.io/name == kafka-broker",
		"env != prod",
		"tier in (frontend, backend)",
		"env notin (dev, staging)",
		"strimzi.io/pool-name in (brokers), !canary",
		"strimzi.io/controller-role",
	}
	for _, sel := range valid {
		if err := validateLabelSelector(sel); err != nil {
			t.Errorf("validateLabelSelector(%q) returned unexpected error: %v", sel, err)
		}
	}

	invalid := []struct {
		sel     string
		wantSub string
	}{
		{"", "cannot be empty"},
		{"   ", "cannot be empty"},
		{"strimzi.io/cluster=<my-cluster>", "cannot contain '<' or '>'"},
		{"app=kafka,", "empty clause"},
		{"tier in (a, b", "unclosed '('"},
		{"tier in a, b)", "unbalanced ')'"},
		{"tier in (a, )", "empty value in set selector"},
		{"tier in (-badval)", "invalid label value"},
		{"BAD_PREFIX/key in (a)", "invalid label key"},
		{"tier maybe (a)", "malformed set-based label selector"},
		{"UPPER_PREFIX/name=val", "invalid label key"},
		{"-badkey=val", "invalid label key"},
		{"app=a=b", "invalid label value"},
		{"app=-badval", "invalid label value"},
	}
	for _, tc := range invalid {
		err := validateLabelSelector(tc.sel)
		if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("validateLabelSelector(%q) = %v, want error containing %q", tc.sel, err, tc.wantSub)
		}
	}
}

func TestValidateKafkaProbeCommand(t *testing.T) {
	valid := []string{
		"under-replicated-partitions",
		"unavailable-partitions",
		"produce kates-probe",
		"produce my.topic_1-test",
		"consumer-lag",
		"consumer-lag billing-cg",
	}
	for _, cmd := range valid {
		if err := validateKafkaProbeCommand(cmd); err != nil {
			t.Errorf("validateKafkaProbeCommand(%q) unexpected error: %v", cmd, err)
		}
	}

	invalid := []string{
		"",
		"unknown-command",
		"produce",
		"produce   ",
		"produce .",
		"produce ..",
		"produce topic1 topic2",
		"produce bad/topic",
		"consumer-lag group1 group2",
	}
	for _, cmd := range invalid {
		if err := validateKafkaProbeCommand(cmd); err == nil {
			t.Errorf("validateKafkaProbeCommand(%q) expected error, got nil", cmd)
		}
	}
}

func TestProbeAndFaultSpecEdgeCases(t *testing.T) {
	// Test empty resilience and disruption files
	if ds := lintResilienceFile([]byte("")); len(ds) != 1 || ds[0].Code != "empty" {
		t.Errorf("expected empty diagnostic on empty resilience file, got %+v", ds)
	}
	if ds := lintDisruptionFile([]byte("")); len(ds) != 1 || ds[0].Code != "empty" {
		t.Errorf("expected empty diagnostic on empty disruption file, got %+v", ds)
	}
	if ds := lintTestRequestFile([]byte("")); len(ds) != 1 || ds[0].Code != "empty" {
		t.Errorf("expected empty diagnostic on empty test-request file, got %+v", ds)
	}

	// Test missing required top-level fields and typo suggestions in FaultSpec & ProbeSpec
	resSrc := `steadyStateSec: -1
maxRecoveryWaitSec: -1
chaosSpec:
  experimentName: ""
  disruptionType: POD_KIL
  targetNamespace: [bad]
  targetLabel: [bad]
  targetTopic: ".."
  targetPartition: 0
  envOverrides:
    NODE_LABEL: "bad=<selector>"
    TARGET_NOD: "worker-1"
  probes: "not-a-list"
probes:
  - type: kafkaProb
    mode: Continuos
    comparator: "~="
    command: ""
    expectedOutput: [1, 2]
    timeoutSec: 99999
`
	ds := lintResilienceFile([]byte(resSrc))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code+":"+d.Path)
	}
	want := []string{
		"missing-field:testRequest",
		"wrong-value:steadyStateSec",
		"wrong-value:maxRecoveryWaitSec",
		"wrong-value:chaosSpec.experimentName",
		"unknown-type:chaosSpec.disruptionType",
		"wrong-type:chaosSpec.targetNamespace",
		"wrong-type:chaosSpec.targetLabel",
		"wrong-value:chaosSpec.targetTopic",
		"unused-field:chaosSpec.targetPartition",
		"invalid-label-selector:chaosSpec.envOverrides.NODE_LABEL",
		"invalid-env-override:chaosSpec.envOverrides.TARGET_NOD",
		"wrong-type:chaosSpec.probes",
		"missing-field:probes[0].name",
		"unknown-probe-type:probes[0].type",
		"unknown-probe-mode:probes[0].mode",
		"unknown-comparator:probes[0].comparator",
		"missing-field:probes[0].command",
		"wrong-type:probes[0].expectedOutput",
		"wrong-value:probes[0].timeoutSec",
	}
	for _, w := range want {
		if !slices.Contains(codes, w) {
			t.Errorf("missing expected diagnostic %q in %v", w, codes)
		}
	}

	// Disruption plan edge cases: missing name, empty steps, step missing name/faultSpec
	planSrc := `testType: LAOD
baselineDurationSec: -1
isrTrackingTopic: ".."
lagTrackingGroupId: "   "
maxAffectedBrokers: 0
steps:
  - steadyStateSec: 10
`
	planDs := lintDisruptionFile([]byte(planSrc))
	planCodes := make([]string, 0, len(planDs))
	for _, d := range planDs {
		planCodes = append(planCodes, d.Code+":"+d.Path)
	}
	for _, w := range []string{
		"missing-field:name",
		"unknown-type:testType",
		"wrong-value:baselineDurationSec",
		"wrong-value:isrTrackingTopic",
		"wrong-value:lagTrackingGroupId",
		"wrong-value:maxAffectedBrokers",
		"missing-field:steps[0].name",
		"missing-field:steps[0].faultSpec",
	} {
		if !slices.Contains(planCodes, w) {
			t.Errorf("missing disruption diagnostic %q in %v", w, planCodes)
		}
	}

	// Multi-phase TestRequest edge cases
	mpSrc := `backend: bogus
scenario:
  name: ""
  phases:
    - type: WARMP
      durationMs: 0
  labels:
    env: [not-a-scalar]
`
	mpDs := lintTestRequestFile([]byte(mpSrc))
	mpCodes := make([]string, 0, len(mpDs))
	for _, d := range mpDs {
		mpCodes = append(mpCodes, d.Code+":"+d.Path)
	}
	for _, w := range []string{
		"unknown-backend:backend",
		"missing-field:scenario.name",
		"missing-field:scenario.phases[0].name",
		"unknown-phase-type:scenario.phases[0].type",
		"wrong-value:scenario.phases[0].durationMs",
		"wrong-type:scenario.labels.env",
	} {
		if !slices.Contains(mpCodes, w) {
			t.Errorf("missing multi-phase diagnostic %q in %v", w, mpCodes)
		}
	}
}

func TestScenarioStringEnumsAndBounds(t *testing.T) {
	src := `scenarios:
  - name: "Enum & Bound Checks"
    type: LOAD
    backend: unknown-engine
    spec:
      acks: "quorum"
      compressionType: "brotli"
      topic: ".."
      consumerGroup: "   "
      records: 0
      parallelProducers: 0
      recordSizeBytes: 0
      batchSize: -1
      lingerMs: -1
      partitions: 0
      replicationFactor: 20
      minInsyncReplicas: 0
      targetThroughput: -5
      fetchMinBytes: 0
      fetchMaxWaitMs: -1
    validate:
      maxP99LatencyMs: -5
      maxErrorRate: 0.1
      maxDataLossPercent: -1
`
	ds := lintScenarioFile("bounds.yaml", []byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Code+":"+d.Path)
	}
	want := []string{
		"unknown-backend:scenarios[0].backend",
		"wrong-value:scenarios[0].spec.acks",
		"wrong-value:scenarios[0].spec.compressionType",
		"wrong-value:scenarios[0].spec.topic",
		"wrong-value:scenarios[0].spec.consumerGroup",
		"wrong-value:scenarios[0].spec.records",
		"wrong-value:scenarios[0].spec.parallelProducers",
		"wrong-value:scenarios[0].spec.recordSizeBytes",
		"wrong-value:scenarios[0].spec.batchSize",
		"wrong-value:scenarios[0].spec.lingerMs",
		"wrong-value:scenarios[0].spec.partitions",
		"wrong-value:scenarios[0].spec.replicationFactor",
		"wrong-value:scenarios[0].spec.minInsyncReplicas",
		"wrong-value:scenarios[0].spec.targetThroughput",
		"wrong-value:scenarios[0].spec.fetchMinBytes",
		"wrong-value:scenarios[0].spec.fetchMaxWaitMs",
		"wrong-value:scenarios[0].validate.maxP99LatencyMs",
		"gate-not-checked:scenarios[0].validate.maxErrorRate",
		"wrong-value:scenarios[0].validate.maxDataLossPercent",
	}
	for _, w := range want {
		if !slices.Contains(codes, w) {
			t.Errorf("missing expected diagnostic %q in %v", w, codes)
		}
	}
}

func TestChaosLintCommands(t *testing.T) {
	dir := t.TempDir()
	resFile := filepath.Join(dir, "res.yaml")
	if err := os.WriteFile(resFile, []byte(`testRequest:
  type: LOAD
  spec:
    numRecords: 1000
chaosSpec:
  experimentName: pod-kill
  disruptionType: POD_KILL
  targetLabel: "strimzi.io/kind=Kafka"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { outputMode = "table" }()
	outputMode = "table"

	stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "resilience", "lint", resFile) })
	if exitCode(err) != 0 || !strings.Contains(string(stdout), "1 file: no problems") {
		t.Fatalf("resilience lint valid: err=%v stdout=%q", err, stdout)
	}

	badPlan := filepath.Join(dir, "plan.yaml")
	if err := os.WriteFile(badPlan, []byte(`name: "Plan"
steps:
  - name: "S1"
    faultSpec:
      experimentName: pod-kill
      disruptionTypo: POD_KILL
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = commandOutput(t, func() error { return runCommandLine(t, "disruption", "lint", badPlan) })
	if exitCode(err) != 1 || !strings.Contains(string(stdout), `did you mean "disruptionType"?`) {
		t.Fatalf("disruption lint invalid: err=%v stdout=%q", err, stdout)
	}

	// JSON output mode & exit code 2 on missing file
	outputMode = "json"
	stdout, _, err = commandOutput(t, func() error { return runCommandLine(t, "disruption", "lint", badPlan) })
	var findings []map[string]any
	if jerr := json.Unmarshal(stdout, &findings); jerr != nil || exitCode(err) != 1 || len(findings) != 1 || findings[0]["code"] != "unknown-field" {
		t.Fatalf("disruption lint json: err=%v jerr=%v stdout=%q", err, jerr, stdout)
	}
	if err := runCommandLine(t, "resilience", "lint", filepath.Join(dir, "missing.yaml")); exitCode(err) != 2 {
		t.Errorf("missing file: err=%v, want exit code 2", err)
	}
}

func TestDisruptionRunAcceptsYAMLConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/disruptions" && r.URL.Query().Get("dryRun") == "true" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode dry-run body: %v", err)
			}
			if body["name"] != "YAML Disruption Plan" {
				t.Errorf("expected plan name 'YAML Disruption Plan', got %v", body["name"])
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.DryRunResult{
				WouldSucceed: true,
				TotalBrokers: 3,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	prevClient := apiClient
	prevFile := disruptionFile
	prevDry := dryRunMode
	apiClient = client.New(srv.URL)
	defer func() {
		apiClient = prevClient
		disruptionFile = prevFile
		dryRunMode = prevDry
	}()

	yamlPath := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(yamlPath, []byte(`name: "YAML Disruption Plan"
steps:
  - name: "Kill Broker"
    faultSpec:
      experimentName: pod-kill
      disruptionType: POD_KILL
`), 0o644); err != nil {
		t.Fatal(err)
	}

	disruptionFile = yamlPath
	dryRunMode = true
	buf := output.ResetForTesting()
	err := disruptionRunCmd.RunE(disruptionRunCmd, nil)
	if err != nil {
		t.Fatalf("disruption run --dry-run with YAML file failed: %v", err)
	}
	if !strings.Contains(buf.String(), "SAFE") {
		t.Errorf("expected SAFE verdict in output, got: %s", buf.String())
	}
}

func TestScenarioCrossFieldChecks(t *testing.T) {
	src := `scenarios:
  - name: "Cross-Field Check"
    type: SPIKE
    backend: trogdor
    spec:
      records: 1000
      minInsyncReplicas: 4
      replicationFactor: 2
      enableTransactions: true
      acks: "1"
      targetThroughput: 5000
      consumerGroup: my-cg
      enableCrc: true
`
	ds := lintScenarioFile("cross.yaml", []byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Severity.String()+":"+d.Code+":"+d.Path)
	}
	want := []string{
		compliance.Error.String() + ":isr-exceeds-rf:scenarios[0].spec.minInsyncReplicas",
		compliance.Error.String() + ":idempotence-requires-acks-all:scenarios[0].spec.acks",
		compliance.Error.String() + ":invalid-transactions-config:scenarios[0].spec.enableTransactions",
		compliance.Warning.String() + ":inapplicable-field:scenarios[0].spec.targetThroughput",
		compliance.Warning.String() + ":inapplicable-field:scenarios[0].spec.consumerGroup",
		compliance.Warning.String() + ":inapplicable-field:scenarios[0].spec.enableCrc",
	}
	for _, w := range want {
		if !slices.Contains(codes, w) {
			t.Errorf("missing expected diagnostic %q in %v", w, codes)
		}
	}
}

func TestScenarioSafetyAndContinuityChecks(t *testing.T) {
	src := `scenarios:
  - name: "Safety Envelope & SLA Checks"
    type: INTEGRITY
    backend: trogdor
    spec:
      records: 10000
      durationSeconds: 2000
      parallelProducers: 2
      targetThroughput: 1000
    validate:
      maxAvgLatencyMs: 250
      maxP99LatencyMs: 100
      minThroughputRecPerSec: 5000
  - name: "Unevaluable RTO on LOAD"
    type: LOAD
    validate:
      maxRtoMs: 5000
`
	ds := lintScenarioFile("safety.yaml", []byte(src))
	codes := make([]string, 0, len(ds))
	for _, d := range ds {
		codes = append(codes, d.Severity.String()+":"+d.Path+":"+d.Code)
	}
	want := []string{
		compliance.Error.String() + ":scenarios[0].backend:incompatible-backend",
		compliance.Warning.String() + ":scenarios[0].spec.durationSeconds:reaper-deadline",
		compliance.Warning.String() + ":scenarios[0].validate.maxAvgLatencyMs:contradictory-sla",
		compliance.Warning.String() + ":scenarios[0].validate.minThroughputRecPerSec:impossible-sla-gate",
		compliance.Warning.String() + ":scenarios[1].validate.maxRtoMs:unevaluable-gate",
	}
	for _, w := range want {
		if !slices.Contains(codes, w) {
			t.Errorf("missing expected diagnostic %q in %v", w, codes)
		}
	}

	reqSrc := `scenario:
  name: "Multi-Phase Continuity"
  phases:
    - name: "Ramp Without Overrides"
      type: RAMP
      durationMs: 10000
      rampSteps: 3
  sla:
    p95LatencyMs: 200
    p99LatencyMs: 50
`
	reqDs := lintTestRequestFile([]byte(reqSrc))
	reqCodes := make([]string, 0, len(reqDs))
	for _, d := range reqDs {
		reqCodes = append(reqCodes, d.Severity.String()+":"+d.Code+":"+d.Path)
	}
	wantReq := []string{
		compliance.Warning.String() + ":ramp-without-override:scenario.phases[0].specOverrides",
		compliance.Error.String() + ":contradictory-sla:scenario.sla.p95LatencyMs",
	}
	for _, w := range wantReq {
		if !slices.Contains(reqCodes, w) {
			t.Errorf("missing expected test-request diagnostic %q in %v", w, reqCodes)
		}
	}
}
