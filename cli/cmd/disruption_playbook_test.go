package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

// Trimmed from GET /api/disruptions/playbooks/leader-cascade, plus a field no
// struct in the CLI names, which -o json and the dry run must still carry.
const leaderCascadePlanJSON = `{"name":"playbook:leader-cascade",` +
	`"description":"Kill partition leaders sequentially to test cascading election recovery",` +
	`"steps":[{"name":"kill-leader-partition-0","faultSpec":{"experimentName":"leader-cascade-p0",` +
	`"targetNamespace":"kafka","targetLabel":"strimzi.io/component-type=kafka","targetPod":"","targetAll":false,` +
	`"chaosDurationSec":10,"disruptionType":"POD_KILL","targetBrokerId":-1,"fillPercentage":80,` +
	`"gracePeriodSec":30,"ioWorkers":2,"targetTopic":"__consumer_offsets","targetPartition":0},` +
	`"steadyStateSec":30,"observationWindowSec":60,` +
	`"requireRecovery":true},{"name":"kill-leader-partition-1","faultSpec":{"experimentName":"leader-cascade-p1",` +
	`"targetNamespace":"kafka","targetLabel":"strimzi.io/component-type=kafka","targetPod":"","targetAll":false,` +
	`"chaosDurationSec":10,"disruptionType":"POD_KILL","targetBrokerId":-1,"fillPercentage":80,` +
	`"gracePeriodSec":30,"ioWorkers":2,"targetTopic":"__consumer_offsets","targetPartition":1},` +
	`"steadyStateSec":15,"observationWindowSec":60,` +
	`"requireRecovery":true}],"maxAffectedBrokers":2,"autoRollback":true,` +
	`"isrTrackingTopic":"__consumer_offsets","futureField":{"kept":true}}`

const storagePressurePlanJSON = `{"name":"playbook:storage-pressure","description":"Fill broker log directories",` +
	`"steps":[{"name":"fill-broker-disk","faultSpec":{"targetNamespace":"kafka",` +
	`"targetLabel":"strimzi.io/component-type=kafka","disruptionType":"DISK_FILL","targetBrokerId":0,` +
	`"fillPercentage":90,"chaosDurationSec":120},"steadyStateSec":30,"observationWindowSec":120,` +
	`"requireRecovery":true}],"maxAffectedBrokers":-1,"autoRollback":false}`

// Every fault type whose size comes from a field of its own, one step each,
// with every such field set so that a row printed for the wrong type shows.
// None of the shipped playbooks uses most of these types; the YAML can name
// any of them, and each fault carries every field, with defaults.
const faultRowsPlanJSON = `{"name":"playbook:fault-rows","steps":[` +
	`{"name":"graceful-delete","faultSpec":` + allSizes + `,"disruptionType":"POD_DELETE","gracePeriodSec":45}},` +
	`{"name":"force-kill","faultSpec":` + allSizes + `,"disruptionType":"POD_KILL","gracePeriodSec":0}},` +
	`{"name":"io-pressure","faultSpec":` + allSizes + `,"disruptionType":"IO_STRESS","fillPercentage":70,"ioWorkers":4}},` +
	`{"name":"cpu-hog","faultSpec":` + allSizes + `,"disruptionType":"CPU_STRESS","cpuCores":3,"delayBeforeSec":5}},` +
	`{"name":"memory-hog","faultSpec":` + allSizes + `,"disruptionType":"MEMORY_STRESS","memoryMb":512}},` +
	`{"name":"slow-network","faultSpec":` + allSizes + `,"disruptionType":"NETWORK_LATENCY","networkLatencyMs":250}},` +
	`{"name":"custom-experiment","faultSpec":` + allSizes + `,"experimentName":"kafka-broker-pod-delete-custom",` +
	`"disruptionType":null}},` +
	`{"name":"idle","steadyStateSec":10}` +
	`],"maxAffectedBrokers":1}`

// The fields every step of faultRowsPlanJSON shares, as an open JSON object:
// the step closes it after adding its own. JSON takes the last of two equal
// keys, which is how a step overrides one of these.
const allSizes = `{"targetNamespace":"kafka","targetLabel":"strimzi.io/component-type=kafka",` +
	`"targetBrokerId":-1,"chaosDurationSec":60,"delayBeforeSec":0,"gracePeriodSec":30,"fillPercentage":80,` +
	`"networkLatencyMs":100,"cpuCores":1,"memoryMb":500,"ioWorkers":2`

const leaderCascadeDryRunJSON = `{"wouldSucceed":true,"totalBrokers":3,"steps":[` +
	`{"name":"kill-leader-partition-0","disruptionType":"POD_KILL","targetPod":"krafter-broker-1",` +
	`"resolvedLeaderId":1,"affectedPods":["krafter-broker-1"],"warnings":[]},` +
	`{"name":"kill-leader-partition-1","disruptionType":"POD_KILL","targetPod":"krafter-broker-2",` +
	`"resolvedLeaderId":2,"affectedPods":["krafter-broker-2"],"warnings":[]}],` +
	`"warnings":["Only 1 broker would remain after disruption — high risk of data loss"],"errors":[]}`

const unsafeDryRunJSON = `{"wouldSucceed":false,"totalBrokers":3,"steps":[` +
	`{"name":"kill-leader-partition-0","disruptionType":"POD_KILL","affectedPods":["krafter-broker-1"]}],` +
	`"warnings":[],"errors":["Plan would affect 3 brokers but maxAffectedBrokers=2"]}`

// playbookBackend serves the playbook plans it is given and the plan dry run,
// records every request and what the dry run was sent, and fails the test on
// anything else. A request that starts a playbook or a plan is "anything
// else" unless the backend was built with allowRuns.
type playbookBackend struct {
	t            *testing.T
	plans        map[string]string
	runs         bool
	dryRunResult string

	mu       sync.Mutex // the handler runs on the server's goroutines
	dryRuns  [][]byte
	requests []string
}

// allowRuns lets the backend accept a playbook or plan run, as disruption
// d-42, and report it COMPLETED on the first poll.
func allowRuns(b *playbookBackend) { b.runs = true }

// dryRunAnswers makes the dry run return result instead of a SAFE verdict.
func dryRunAnswers(result string) func(*playbookBackend) {
	return func(b *playbookBackend) { b.dryRunResult = result }
}

// seen returns the requests made so far and the bodies the dry run was sent.
func (b *playbookBackend) seen() (requests []string, dryRuns [][]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...), append([][]byte(nil), b.dryRuns...)
}

// newPlaybookBackend starts the backend, points apiClient at it, and resets
// every variable a disruption command line in this file can set when the test
// ends, so the next test starts from the defaults, as a new process would.
// The options apply before the server starts: the handler reads them.
func newPlaybookBackend(t *testing.T, plans map[string]string, opts ...func(*playbookBackend)) *playbookBackend {
	t.Helper()
	b := &playbookBackend{t: t, plans: plans, dryRunResult: leaderCascadeDryRunJSON}
	for _, opt := range opts {
		opt(b)
	}
	ts := httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(ts.Close)

	apiClient = client.New(ts.URL)
	apiClient.MaxRetries = 1
	reset := func() {
		outputMode = "table"
		playbookRunDryRun = false
		disruptionFile, dryRunMode = "", false
	}
	reset()
	t.Cleanup(reset)
	return b
}

func (b *playbookBackend) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.requests = append(b.requests, r.Method+" "+r.URL.RequestURI())
	b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const prefix = "/api/disruptions/playbooks/"
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, prefix):
		name := strings.TrimPrefix(r.URL.Path, prefix)
		plan, ok := b.plans[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status":404,"error":"Not Found","message":"Playbook not found: `+name+`"}`)
			return
		}
		_, _ = io.WriteString(w, plan)
	case r.Method == http.MethodPost && r.URL.Path == "/api/disruptions" && r.URL.Query().Get("dryRun") == "true":
		body, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		b.dryRuns = append(b.dryRuns, body)
		b.mu.Unlock()
		_, _ = io.WriteString(w, b.dryRunResult)
	case b.runs && r.Method == http.MethodPost &&
		(strings.HasPrefix(r.URL.Path, prefix) || r.URL.Path == "/api/disruptions" && r.URL.RawQuery == ""):
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":"d-42","status":"RUNNING","planName":"playbook:leader-cascade"}`)
	case b.runs && r.Method == http.MethodGet && r.URL.Path == "/api/disruptions/d-42":
		_, _ = io.WriteString(w, `{"planName":"playbook:leader-cascade","status":"COMPLETED"}`)
	default:
		b.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}
}

// runCommandLine runs a kates command line the way cobra runs one: it finds
// the command from the words, parses the flags into the variables they are
// bound to, checks the arguments, and calls RunE. A test that sets those
// variables itself would pass with a flag bound to the wrong one. It skips
// the root's pre-run, which would replace apiClient with a client built from
// ~/.kates.yaml; newPlaybookBackend resets the variables afterwards.
func runCommandLine(t *testing.T, args ...string) error {
	t.Helper()
	cmd, rest, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("no command for %q: %v", args, err)
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return err
	}
	positional := cmd.Flags().Args()
	if err := cmd.ValidateArgs(positional); err != nil {
		return err
	}
	return cmd.RunE(cmd, positional)
}

// commandOutput runs a command with output.Out on os.Stdout, as in the real
// binary, and returns what it wrote to stdout, what it wrote to output.Err,
// and its error. A buffer on output.Out alone misses a stray fmt.Println.
func commandOutput(t *testing.T, run func() error) (stdout []byte, stderr string, err error) {
	t.Helper()
	var errBuf bytes.Buffer
	out := captureStdout(t, func() {
		prevOut, prevErr := output.Out, output.Err
		output.Out, output.Err = os.Stdout, &errBuf
		defer func() { output.Out, output.Err = prevOut, prevErr }()
		err = run()
	})
	return []byte(out), errBuf.String(), err
}

// commandStdout is commandOutput for a command that must succeed.
func commandStdout(t *testing.T, run func() error) []byte {
	t.Helper()
	stdout, stderr, err := commandOutput(t, run)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s%s", err, stdout, stderr)
	}
	return stdout
}

func compactJSON(t *testing.T, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
	return buf.String()
}

func TestDisruptionPlaybookShow_Table(t *testing.T) {
	tests := []struct {
		name     string
		playbook string
		plan     string
		want     []string
		notWant  []string
	}{
		{
			name:     "leader-targeted steps",
			playbook: "leader-cascade",
			plan:     leaderCascadePlanJSON,
			want: []string{
				"Playbook Plan: playbook:leader-cascade",
				"Kill partition leaders sequentially",
				"Max Affected Brokers", "2",
				"ISR Tracking Topic", "__consumer_offsets",
				"Step 1: kill-leader-partition-0 (POD_KILL)",
				"Step 2: kill-leader-partition-1 (POD_KILL)",
				"Leader Of", "__consumer_offsets-0", "__consumer_offsets-1",
				"Label Selector", "strimzi.io/component-type=kafka",
				"Chaos Duration", "10s",
				"Steady State", "30s", "15s",
				"Observation Window", "60s",
				"kates disruption playbook run leader-cascade --dry-run",
			},
			// targetBrokerId -1 means no broker was named, and a POD_KILL
			// reads neither the fill percentage nor the grace period it
			// carries: it force-deletes.
			notWant: []string{"Target Broker", "Fill Percentage", "Grace Period", "IO Workers", "Target All"},
		},
		{
			name:     "broker 0 and a disk fill",
			playbook: "storage-pressure",
			plan:     storagePressurePlanJSON,
			want: []string{
				"Step 1: fill-broker-disk (DISK_FILL)",
				"Target Broker", "Fill Percentage", "90%",
				"Chaos Duration", "120s",
			},
			notWant: []string{"Leader Of", "ISR Tracking Topic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newPlaybookBackend(t, map[string]string{tt.playbook: tt.plan})
			buf := output.ResetForTesting()

			if err := runCommandLine(t, "disruption", "playbook", "show", tt.playbook); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			out := stripAnsi(buf.String())
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q:\n%s", w, out)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q, want it left out:\n%s", w, out)
				}
			}
			if _, dryRuns := b.seen(); len(dryRuns) != 0 {
				t.Errorf("show posted %d dry runs, want none", len(dryRuns))
			}
		})
	}
}

// Each step prints the fields its fault type reads and none of the others,
// which every fault carries too.
func TestDisruptionPlaybookShow_FaultRows(t *testing.T) {
	newPlaybookBackend(t, map[string]string{"fault-rows": faultRowsPlanJSON})
	buf := output.ResetForTesting()

	if err := runCommandLine(t, "disruption", "playbook", "show", "fault-rows"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())

	sizeRows := []string{"Grace Period", "Fill Percentage", "IO Workers", "CPU Cores", "Memory", "Latency", "Delay Before"}
	tests := []struct {
		header string
		want   []string
	}{
		{header: "Step 1: graceful-delete (POD_DELETE)", want: []string{"Grace Period", "45s"}},
		{header: "Step 2: force-kill (POD_KILL)"},
		{header: "Step 3: io-pressure (IO_STRESS)", want: []string{"Fill Percentage", "70%", "IO Workers", "4"}},
		{header: "Step 4: cpu-hog (CPU_STRESS)", want: []string{"CPU Cores", "3", "Delay Before", "5s"}},
		{header: "Step 5: memory-hog (MEMORY_STRESS)", want: []string{"Memory", "512 MB"}},
		{header: "Step 6: slow-network (NETWORK_LATENCY)", want: []string{"Latency", "250ms"}},
		{
			// Litmus runs the experiment the step names; "no fault" would
			// say it injects nothing.
			header: "Step 7: custom-experiment (no disruptionType)",
			want:   []string{"Litmus Experiment", "kafka-broker-pod-delete-custom", "Namespace", "Chaos Duration"},
		},
		{header: "Step 8: idle (no fault)", want: []string{"Steady State", "10s"}},
	}

	for i, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			start := strings.Index(out, tt.header)
			if start < 0 {
				t.Fatalf("output missing %q:\n%s", tt.header, out)
			}
			block := out[start:]
			if end := strings.Index(block, fmt.Sprintf("Step %d: ", i+2)); end > 0 {
				block = block[:end]
			}
			for _, w := range tt.want {
				if !strings.Contains(block, w) {
					t.Errorf("step missing %q:\n%s", w, block)
				}
			}
			for _, row := range sizeRows {
				if strings.Contains(block, row) && !contains(tt.want, row) {
					t.Errorf("step has %q, which its fault type does not read:\n%s", row, block)
				}
			}
			if strings.Contains(block, "Litmus Experiment") && !contains(tt.want, "Litmus Experiment") {
				t.Errorf("a typed step shows its experiment name as the experiment it runs:\n%s", block)
			}
		})
	}

	idle := out[strings.Index(out, "Step 8: idle"):]
	for _, row := range []string{"Namespace", "Chaos Duration"} {
		if strings.Contains(idle, row) {
			t.Errorf("a step without a faultSpec shows %q:\n%s", row, idle)
		}
	}
}

// The plan's strings reach a terminal, and an escape sequence in one of them
// would act on it: recolour it, clear it, retitle the window, or reorder the
// line with a bidi override.
func TestDisruptionPlaybookShow_StripsTerminalControls(t *testing.T) {
	const hostile = `{"name":"playbook:hostile\u001b]0;pwned\u0007",` +
		`"description":"Kill \u001b[31mleaders\u001b[0m\u001b[2J\nnow",` +
		`"steps":[{"name":"kill\u202eevil","faultSpec":{"disruptionType":"POD_KILL",` +
		`"targetNamespace":"kafka\u001bc","targetLabel":"app=\u200bkafka","targetTopic":"orders\u2066"}}]}`
	newPlaybookBackend(t, map[string]string{"hostile": hostile})
	buf := output.ResetForTesting()

	if err := runCommandLine(t, "disruption", "playbook", "show", "hostile"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := buf.String()
	for _, bad := range []string{"\x1b]", "\x1b[2J", "\x1b[31m", "\x1bc", "pwned", "\u202e", "\u200b", "\u2066", "\a"} {
		if strings.Contains(raw, bad) {
			t.Errorf("output carries %q from the plan:\n%q", bad, raw)
		}
	}
	for _, w := range []string{"playbook:hostile", "Kill leaders now", "killevil", "kafka", "app=kafka", "orders-0"} {
		if !strings.Contains(raw, w) {
			t.Errorf("output missing %q:\n%q", w, raw)
		}
	}
}

// Max Affected Brokers of -1 or 0 is no limit, as the safety guard reads it.
func TestDisruptionPlaybookShow_NoBrokerLimit(t *testing.T) {
	newPlaybookBackend(t, map[string]string{"storage-pressure": storagePressurePlanJSON})
	buf := output.ResetForTesting()

	if err := runCommandLine(t, "disruption", "playbook", "show", "storage-pressure"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var limit string
	for _, line := range strings.Split(stripAnsi(buf.String()), "\n") {
		if strings.Contains(line, "Max Affected Brokers") {
			limit = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Max Affected Brokers"))
		}
	}
	if limit != "none" {
		t.Errorf("Max Affected Brokers = %q, want none", limit)
	}
}

func TestDisruptionPlaybookShow_JSONIsTheBackendPlan(t *testing.T) {
	newPlaybookBackend(t, map[string]string{"leader-cascade": leaderCascadePlanJSON})

	stdout := commandStdout(t, func() error {
		return runCommandLine(t, "disruption", "playbook", "show", "leader-cascade", "-o", "json")
	})

	// Byte for byte once whitespace is gone: same fields, same order, and
	// nothing printed around it, so it can be saved and run as a plan.
	if got, want := compactJSON(t, stdout), compactJSON(t, []byte(leaderCascadePlanJSON)); got != want {
		t.Errorf("-o json printed\n  %s\nwant the backend's plan\n  %s", got, want)
	}
}

func TestDisruptionPlaybookShow_UnknownPlaybook(t *testing.T) {
	newPlaybookBackend(t, map[string]string{})
	output.ResetForTesting()

	err := runCommandLine(t, "disruption", "playbook", "show", "not-a-playbook")
	if err == nil || !strings.Contains(err.Error(), "Playbook not found: not-a-playbook") {
		t.Fatalf("error = %v, want the backend's 404 message", err)
	}
}

// The requests each command line sends, in order. The backend fails the test
// on any other request, and it refuses runs unless a case allows them, so a
// dry run that reached the run endpoint fails twice over.
func TestDisruptionPlaybookRun(t *testing.T) {
	previewRequests := []string{
		"GET /api/disruptions/playbooks/leader-cascade",
		"POST /api/disruptions?dryRun=true",
	}
	runRequests := []string{
		"POST /api/disruptions/playbooks/leader-cascade",
		"GET /api/disruptions/d-42",
	}

	tests := []struct {
		name         string
		args         []string
		runs         bool
		wantRequests []string
		wantOut      []string // table output only
	}{
		{
			name:         "dry run",
			args:         []string{"leader-cascade", "--dry-run"},
			wantRequests: previewRequests,
			wantOut: []string{
				"Dry-Run Results",
				"Step: kill-leader-partition-0 (POD_KILL)",
				"Resolved Leader", "broker-1",
				"krafter-broker-2",
				"Only 1 broker would remain",
			},
		},
		{
			name:         "dry run, flag first, JSON",
			args:         []string{"--dry-run", "leader-cascade", "-o", "json"},
			wantRequests: previewRequests,
		},
		{
			name:         "run",
			args:         []string{"leader-cascade"},
			runs:         true,
			wantRequests: runRequests,
			wantOut:      []string{"Running playbook: leader-cascade", "Disruption ID: d-42", "Status: COMPLETED"},
		},
		{
			name:         "run, JSON",
			args:         []string{"leader-cascade", "-o", "json"},
			runs:         true,
			wantRequests: runRequests,
		},
		{
			name:         "dry run switched off",
			args:         []string{"leader-cascade", "--dry-run=false"},
			runs:         true,
			wantRequests: runRequests,
			wantOut:      []string{"Disruption ID: d-42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []func(*playbookBackend)
			if tt.runs {
				opts = append(opts, allowRuns)
			}
			b := newPlaybookBackend(t, map[string]string{"leader-cascade": leaderCascadePlanJSON}, opts...)

			stdout := commandStdout(t, func() error {
				return runCommandLine(t, append([]string{"disruption", "playbook", "run"}, tt.args...)...)
			})

			requests, dryRuns := b.seen()
			if strings.Join(requests, "\n") != strings.Join(tt.wantRequests, "\n") {
				t.Errorf("requests = %q, want %q", requests, tt.wantRequests)
			}
			preview := !tt.runs
			if preview {
				if len(dryRuns) != 1 {
					t.Fatalf("dry runs = %d, want 1", len(dryRuns))
				}
				if got, want := compactJSON(t, dryRuns[0]), compactJSON(t, []byte(leaderCascadePlanJSON)); got != want {
					t.Errorf("dry run was sent\n  %s\nwant the playbook's plan as the backend returned it\n  %s", got, want)
				}
			}

			out := stripAnsi(string(stdout))
			if outputMode == "json" {
				// stdout is the result and nothing else.
				if preview {
					var result client.DryRunResult
					if err := json.Unmarshal(stdout, &result); err != nil {
						t.Fatalf("-o json output is not the dry-run result alone: %v\n%s", err, out)
					}
					if !result.WouldSucceed || len(result.Steps) != 2 {
						t.Errorf("dry-run result = %+v", result)
					}
				} else {
					var result client.DisruptionRunResponse
					if err := json.Unmarshal(stdout, &result); err != nil {
						t.Fatalf("-o json output is not the run result alone: %v\n%s", err, out)
					}
					if result.ID != "d-42" || result.Report.Status != "COMPLETED" {
						t.Errorf("run result = %+v", result)
					}
				}
				return
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q:\n%s", w, out)
				}
			}
			if preview && strings.Contains(out, "Running playbook") {
				t.Errorf("a dry run announced a run:\n%s", out)
			}
		})
	}
}

func TestDisruptionPlaybookRun_DryRunUnknownPlaybook(t *testing.T) {
	b := newPlaybookBackend(t, map[string]string{})
	output.ResetForTesting()

	err := runCommandLine(t, "disruption", "playbook", "run", "not-a-playbook", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "Playbook not found: not-a-playbook") {
		t.Fatalf("error = %v, want the backend's 404 message", err)
	}
	if _, dryRuns := b.seen(); len(dryRuns) != 0 {
		t.Errorf("dry runs = %d, want none for a playbook that does not exist", len(dryRuns))
	}
}

// With -o json, stdout carries the result alone, dry run or not. The plan-file
// dry run shares runDryRun with the playbook one, and the progress line of
// both used to precede the JSON.
func TestDisruptionRun_JSONIsTheResultAlone(t *testing.T) {
	tests := []struct {
		name         string
		dryRun       bool
		wantRequests []string
	}{
		{name: "dry run", dryRun: true, wantRequests: []string{"POST /api/disruptions?dryRun=true"}},
		{name: "run", wantRequests: []string{"POST /api/disruptions", "GET /api/disruptions/d-42"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []func(*playbookBackend)
			if !tt.dryRun {
				opts = append(opts, allowRuns)
			}
			b := newPlaybookBackend(t, map[string]string{}, opts...)
			planFile := t.TempDir() + "/plan.json"
			if err := os.WriteFile(planFile, []byte(leaderCascadePlanJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"disruption", "run", "--config", planFile, "-o", "json"}
			if tt.dryRun {
				args = append(args, "--dry-run")
			}

			stdout := commandStdout(t, func() error { return runCommandLine(t, args...) })

			var result any
			if tt.dryRun {
				result = &client.DryRunResult{}
			} else {
				result = &client.DisruptionRunResponse{}
			}
			if err := json.Unmarshal(stdout, result); err != nil {
				t.Fatalf("-o json output is not the result alone: %v\n%s", err, stdout)
			}
			if requests, _ := b.seen(); strings.Join(requests, "\n") != strings.Join(tt.wantRequests, "\n") {
				t.Errorf("requests = %q, want %q", requests, tt.wantRequests)
			}
		})
	}
}

// An UNSAFE verdict is one the launcher would refuse, so the command prints
// the result and then fails, for a script to stop on. The message goes to
// stderr, which keeps -o json output parseable.
func TestDisruptionDryRun_UnsafeVerdictFails(t *testing.T) {
	planFile := t.TempDir() + "/plan.json"
	if err := os.WriteFile(planFile, []byte(leaderCascadePlanJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "playbook", args: []string{"disruption", "playbook", "run", "leader-cascade", "--dry-run"}},
		{name: "playbook, JSON", args: []string{"disruption", "playbook", "run", "leader-cascade", "--dry-run", "-o", "json"}},
		{name: "plan file", args: []string{"disruption", "run", "--config", planFile, "--dry-run"}},
		{name: "plan file, JSON", args: []string{"disruption", "run", "--config", planFile, "--dry-run", "-o", "json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newPlaybookBackend(t, map[string]string{"leader-cascade": leaderCascadePlanJSON},
				dryRunAnswers(unsafeDryRunJSON))

			stdout, stderr, err := commandOutput(t, func() error { return runCommandLine(t, tt.args...) })

			var silent *silentErr
			if !errors.As(err, &silent) {
				t.Fatalf("error = %v, want the UNSAFE verdict as a failure", err)
			}
			if !strings.Contains(stderr, "UNSAFE") {
				t.Errorf("stderr = %q, want the verdict", stderr)
			}
			if outputMode == "json" {
				var result client.DryRunResult
				if err := json.Unmarshal(stdout, &result); err != nil {
					t.Fatalf("-o json output is not the dry-run result alone: %v\n%s", err, stdout)
				}
				if result.WouldSucceed || len(result.Errors) != 1 {
					t.Errorf("dry-run result = %+v", result)
				}
				return
			}
			out := stripAnsi(string(stdout))
			for _, w := range []string{"Verdict", "UNSAFE", "Plan would affect 3 brokers but maxAffectedBrokers=2"} {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q:\n%s", w, out)
				}
			}
		})
	}
}

// Each runnable playbook command carries a doc_entries.yaml entry under its
// full path, and run documents its --dry-run flag, as the migrate tree does.
func TestDisruptionPlaybookCommandsHaveDocEntries(t *testing.T) {
	entries := map[string]DocEntry{}
	for _, e := range docEntries {
		entries[e.Name] = e
	}
	for _, sub := range disruptionPlaybookCmd.Commands() {
		name := strings.TrimPrefix(sub.CommandPath(), "kates ")
		if _, ok := entries[name]; sub.Runnable() && !ok {
			t.Errorf("no doc_entries.yaml entry for %q", name)
		}
	}

	run := entries["disruption playbook run"]
	documented := false
	for _, f := range run.Flags {
		documented = documented || f.Name == "--dry-run"
	}
	if !documented {
		t.Error(`doc_entries.yaml: "disruption playbook run" does not document --dry-run`)
	}
	if disruptionPlaybookRunCmd.Flags().Lookup("dry-run") == nil {
		t.Error("kates disruption playbook run has no --dry-run flag")
	}
}
