package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

func TestLSPDialectDetection(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		src  string
		want yamlDialect
	}{
		{
			name: "tier1 kates-schema disruption",
			uri:  "file:///tmp/custom.yaml",
			src:  "# kates-schema: disruption\nfoo: bar\n",
			want: dialectDisruption,
		},
		{
			name: "tier1 kates-schema scenario",
			uri:  "file:///tmp/custom.yaml",
			src:  "# kates-schema: scenario\n",
			want: dialectScenario,
		},
		{
			name: "tier1 kates-schema test-request",
			uri:  "file:///tmp/custom.yaml",
			src:  "# kates-schema: test-request\n",
			want: dialectTestRequest,
		},
		{
			name: "tier1 kates-schema flow",
			uri:  "file:///tmp/custom.yaml",
			src:  "# kates-schema: flow\n",
			want: dialectFlow,
		},
		{
			name: "tier1 kates-schema resilience",
			uri:  "file:///tmp/custom.yaml",
			src:  "# kates-schema: resilience\n",
			want: dialectResilience,
		},
		{
			name: "tier1 yaml-language-server compliance",
			uri:  "file:///tmp/custom.yaml",
			src:  "# yaml-language-server: $schema=https://example.com/kafkareference.v1alpha1.schema.json\n",
			want: dialectCompliance,
		},
		{
			name: "tier2 compliance AST",
			uri:  "file:///tmp/ref.yaml",
			src:  "apiVersion: kates.io/v1alpha1\nkind: KafkaReference\n",
			want: dialectCompliance,
		},
		{
			name: "tier2 resilience AST",
			uri:  "file:///tmp/test.yaml",
			src:  "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: pod-kill\n",
			want: dialectResilience,
		},
		{
			name: "tier2 disruption AST",
			uri:  "file:///tmp/plan.yaml",
			src:  "name: plan\nsteps:\n  - name: s1\n    faultSpec:\n      experimentName: pod-kill\n",
			want: dialectDisruption,
		},
		{
			name: "tier2 flow AST",
			uri:  "file:///tmp/pipeline.yaml",
			src:  "name: flow\nsteps:\n  - name: s1\n    action: test\n",
			want: dialectFlow,
		},
		{
			name: "tier2 multi-phase test-request AST",
			uri:  "file:///tmp/req.yaml",
			src:  "scenario:\n  name: multi\n  phases:\n    - name: p1\n      type: WARMUP\n      durationMs: 5000\n",
			want: dialectTestRequest,
		},
		{
			name: "tier2 wire single test-request AST",
			uri:  "file:///tmp/req-single.yaml",
			src:  "type: LOAD\nspec:\n  numRecords: 5000\n  durationMs: 10000\n",
			want: dialectTestRequest,
		},
		{
			name: "tier2 scenario AST",
			uri:  "file:///tmp/scn.yaml",
			src:  "scenarios:\n  - name: s1\n    type: LOAD\n",
			want: dialectScenario,
		},
		{
			name: "tier2 lone scenario AST",
			uri:  "file:///tmp/lone.yaml",
			src:  "name: s1\ntype: LOAD\nspec:\n  records: 1000\n",
			want: dialectScenario,
		},
		{
			name: "tier3 filename fallback scenario",
			uri:  "file:///workspace/perf-throughput.yaml",
			src:  "# empty file\n",
			want: dialectScenario,
		},
		{
			name: "tier3 filename fallback resilience",
			uri:  "file:///workspace/resilience-pod-kill.yaml",
			src:  "# empty file\n",
			want: dialectResilience,
		},
		{
			name: "tier3 filename fallback disruption",
			uri:  "file:///workspace/playbooks/az-outage.yaml",
			src:  "# empty file\n",
			want: dialectDisruption,
		},
		{
			name: "tier3 filename fallback flow",
			uri:  "file:///workspace/flow-ci.yaml",
			src:  "# empty file\n",
			want: dialectFlow,
		},
		{
			name: "tier3 filename fallback compliance",
			uri:  "file:///workspace/conformity/prod.yaml",
			src:  "# empty file\n",
			want: dialectCompliance,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDialect(tc.uri, []byte(tc.src)); got != tc.want {
				t.Errorf("detectDialect(%q) = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
}

func sendLSPMsg(w io.Writer, msg map[string]any) {
	data, _ := json.Marshal(msg)
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

func readLSPMsg(t *testing.T, r *bufio.Reader) rpcMessage {
	t.Helper()
	payload, err := readLSPFrame(r)
	if err != nil {
		t.Fatalf("readLSPFrame: %v", err)
	}
	var msg rpcMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("unmarshal rpcMessage: %v", err)
	}
	return msg
}

func TestLSPEndToEndProtocol(t *testing.T) {
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runLSPServer(ctx, serverIn, serverOut)
	}()

	reader := bufio.NewReader(clientIn)

	// 1. Initialize & initialized notification
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	initResp := readLSPMsg(t, reader)
	if string(initResp.ID) != "1" {
		t.Fatalf("expected initialize response id=1, got %s", initResp.ID)
	}
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
		"params":  map[string]any{},
	})

	// 2. Open scenario file with a typo ("partitons")
	scnURI := "file:///workspace/scenarios/test.yaml"
	scnText := `scenarios:
  - name: "Load Test"
    type: LOAD
    spec:
      records: 1000
      partitons: 6
`
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":  scnURI,
				"text": scnText,
			},
		},
	})
	diagNotif := readLSPMsg(t, reader)
	if diagNotif.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("expected publishDiagnostics, got %q", diagNotif.Method)
	}
	var pubParams lspPublishDiagnosticsParams
	if err := json.Unmarshal(diagNotif.Params, &pubParams); err != nil {
		t.Fatal(err)
	}
	if len(pubParams.Diagnostics) != 1 || pubParams.Diagnostics[0].Code != "unknown-field" {
		t.Fatalf("expected 1 unknown-field diagnostic, got %+v", pubParams.Diagnostics)
	}
	if pubParams.Diagnostics[0].Data["replacement"] != "partitions" {
		t.Errorf("expected replacement 'partitions', got %v", pubParams.Diagnostics[0].Data["replacement"])
	}

	// 3. Request Quick-Fix CodeAction for the typo on line 5
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "textDocument/codeAction",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": scnURI},
			"range": map[string]any{
				"start": map[string]any{"line": 5, "character": 6},
				"end":   map[string]any{"line": 5, "character": 15},
			},
			"context": map[string]any{
				"diagnostics": pubParams.Diagnostics,
			},
		},
	})
	caResp := readLSPMsg(t, reader)
	rawCA, _ := json.Marshal(caResp.Result)
	var actions []lspCodeAction
	_ = json.Unmarshal(rawCA, &actions)
	if len(actions) == 0 || actions[0].Title != `Replace with "partitions"` {
		t.Fatalf("expected Quick-Fix 'Replace with \"partitions\"', got %+v", actions)
	}

	// 4. Request Completions inside scenarios[0].spec
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": scnURI},
			"position":     map[string]any{"line": 4, "character": 6},
		},
	})
	compResp := readLSPMsg(t, reader)
	rawComp, _ := json.Marshal(compResp.Result)
	var compItems []lspCompletionItem
	_ = json.Unmarshal(rawComp, &compItems)
	labels := make([]string, 0, len(compItems))
	for _, item := range compItems {
		labels = append(labels, item.Label)
	}
	for _, wantKey := range []string{"parallelProducers", "recordSizeBytes", "durationSeconds", "acks", "compressionType", "enableIdempotence"} {
		if !slices.Contains(labels, wantKey) {
			t.Errorf("missing completion key %q in %v", wantKey, labels)
		}
	}

	// 5. Request Hover on "LOAD" (line 2, col 11)
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": scnURI},
			"position":     map[string]any{"line": 2, "character": 11},
		},
	})
	hoverResp := readLSPMsg(t, reader)
	rawHover, _ := json.Marshal(hoverResp.Result)
	var hover lspHover
	_ = json.Unmarshal(rawHover, &hover)
	if !strings.Contains(hover.Contents.Value, "**`LOAD`**") || !strings.Contains(hover.Contents.Value, "Shipped Default") {
		t.Errorf("unexpected hover markdown: %q", hover.Contents.Value)
	}

	// 6. Test didChange fixing the typo -> 0 diagnostics, then didSave
	fixedScnText := strings.Replace(scnText, "partitons: 6", "partitions: 6\n      topic: kates-events", 1)
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didChange",
		"params": map[string]any{
			"textDocument":   map[string]any{"uri": scnURI},
			"contentChanges": []map[string]any{{"text": fixedScnText}},
		},
	})
	changeDiag := readLSPMsg(t, reader)
	var changePub lspPublishDiagnosticsParams
	_ = json.Unmarshal(changeDiag.Params, &changePub)
	if len(changePub.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics after fixing typo via didChange, got %+v", changePub.Diagnostics)
	}

	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didSave",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": scnURI},
			"text":         fixedScnText,
		},
	})
	_ = readLSPMsg(t, reader) // publishDiagnostics from didSave

	// 7. Open a KafkaReference document and test dynamic waiver rule completion + Go-to-Definition + Semantic Tokens + Formatting
	compURI := "file:///workspace/conformity/prod.yaml"
	compText := `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-3az
spec:
  brokers:
    count: 3
    storage:
      size: 500Gi
  waivers:
    - rule: brokers.count
      reason: "scaling in progress"
      until: "2027-12-31"
      by: "sre@example.com"
`
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{
				"uri":  compURI,
				"text": compText,
			},
		},
	})
	compDiagNotif := readLSPMsg(t, reader)
	var compPub lspPublishDiagnosticsParams
	_ = json.Unmarshal(compDiagNotif.Params, &compPub)
	if len(compPub.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics on valid KafkaReference, got %+v", compPub.Diagnostics)
	}

	// 7a. Completion on `rule: ` (line 10, character 14) should suggest declared rules `brokers.count` and `brokers.storage.size`
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": compURI},
			"position":     map[string]any{"line": 10, "character": 14},
		},
	})
	waiverCompResp := readLSPMsg(t, reader)
	rawWaiverComp, _ := json.Marshal(waiverCompResp.Result)
	var waiverItems []lspCompletionItem
	_ = json.Unmarshal(rawWaiverComp, &waiverItems)
	waiverLabels := make([]string, 0, len(waiverItems))
	for _, item := range waiverItems {
		waiverLabels = append(waiverLabels, item.Label)
	}
	if !slices.Contains(waiverLabels, "brokers.count") || !slices.Contains(waiverLabels, "brokers.storage.size") {
		t.Errorf("expected dynamic waiver rule completions [brokers.count, brokers.storage.size], got %v", waiverLabels)
	}

	// 7b. Go-to-Definition on `rule: brokers.count` (line 10, character 14) should jump to line 6 (`count: 3`)
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "textDocument/definition",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": compURI},
			"position":     map[string]any{"line": 10, "character": 14},
		},
	})
	defResp := readLSPMsg(t, reader)
	rawDef, _ := json.Marshal(defResp.Result)
	var loc lspLocation
	_ = json.Unmarshal(rawDef, &loc)
	if loc.URI != compURI || loc.Range.Start.Line != 6 {
		t.Errorf("expected definition at line 6 in %s, got %+v", compURI, loc)
	}

	// 7c. Semantic tokens on KafkaReference
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "textDocument/semanticTokens/full",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": compURI},
		},
	})
	semResp := readLSPMsg(t, reader)
	rawSem, _ := json.Marshal(semResp.Result)
	var semResult struct {
		Data []int `json:"data"`
	}
	_ = json.Unmarshal(rawSem, &semResult)
	if len(semResult.Data) == 0 || len(semResult.Data)%5 != 0 {
		t.Errorf("expected non-empty multiple-of-5 semantic token array, got %v", semResult.Data)
	}

	// 8. Wire-level tests for codeLens, inlayHint, documentSymbol, foldingRange, references, rename, and custom kates/* RPCs
	for i, req := range []struct {
		method string
		params map[string]any
	}{
		{"textDocument/codeLens", map[string]any{"textDocument": map[string]any{"uri": scnURI}}},
		{"textDocument/inlayHint", map[string]any{"textDocument": map[string]any{"uri": scnURI}, "range": map[string]any{}}},
		{"textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": scnURI}}},
		{"textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": scnURI}}},
		{"textDocument/references", map[string]any{"textDocument": map[string]any{"uri": compURI}, "position": map[string]any{"line": 10, "character": 14}}},
		{"textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": compURI}, "position": map[string]any{"line": 10, "character": 14}, "newName": "brokers.count"}},
		{"kates/detectDialect", map[string]any{"uri": compURI}},
		{"kates/listTemplates", map[string]any{}},
		{"kates/scenarioDiff", map[string]any{"leftUri": scnURI, "rightUri": scnURI}},
		{"kates/clusterPreflight", map[string]any{"uri": scnURI}},
	} {
		id := 10 + i
		sendLSPMsg(clientOut, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  req.method,
			"params":  req.params,
		})
		resp := readLSPMsg(t, reader)
		if resp.Error != nil {
			t.Errorf("%s returned unexpected error: %+v", req.method, resp.Error)
		}
		if resp.Result == nil {
			t.Errorf("%s returned nil result", req.method)
		}
	}

	// 9. Unknown method returns -32601 error
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "kates/nonExistentMethod",
	})
	errResp := readLSPMsg(t, reader)
	if errResp.Error == nil || errResp.Error.Code != -32601 {
		t.Errorf("expected -32601 error for unknown method, got %+v", errResp.Error)
	}

	// 10. didClose clears document and publishes empty diagnostics
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didClose",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": scnURI},
		},
	})
	closeDiag := readLSPMsg(t, reader)
	if closeDiag.Method != "textDocument/publishDiagnostics" {
		t.Errorf("expected publishDiagnostics on didClose, got %s", closeDiag.Method)
	}

	// 11. Shutdown & Exit
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"id":      100,
		"method":  "shutdown",
	})
	_ = readLSPMsg(t, reader)
	sendLSPMsg(clientOut, map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	})
	if err := <-done; err != nil {
		t.Fatalf("LSP server exited with error: %v", err)
	}
}

func TestLSPIntelligenceAndRefactoring(t *testing.T) {
	srv := newLSPServer(io.Discard)

	// 1. Inlay Hints across all 5 dialects
	scURI := "file:///workspace/perf-load.yaml"
	scText := `# Header comment line 1
# Header comment line 2
scenarios:
  - name: "Baseline Load"
    type: INTEGRITY
    spec:
      topic: kates-events
      records: 1000000
      recordSizeBytes: 2048
      parallelProducers: 2
      targetThroughput: 5000
      durationSeconds: 60
    validate:
      maxP99LatencyMs: 100
`
	srv.setDoc(scURI, scText)
	hints := srv.inlayHints(scURI, scText, lspRange{})
	if len(hints) < 2 {
		t.Fatalf("expected at least 2 inlay hints on scenario, got %+v", hints)
	}
	if !strings.Contains(hints[0].Label, "defaults:") || !strings.Contains(hints[1].Label, "GiB") || !strings.Contains(hints[1].Label, "120s") {
		t.Errorf("unexpected inlay hints on INTEGRITY scenario: %+v", hints)
	}

	// Multi-phase TestRequest inlay hints
	mpURI := "file:///workspace/test-request-ramp.yaml"
	mpText := `backend: native
scenario:
  name: "Multi-Phase Ramp"
  baseSpec:
    numRecords: 50000
    recordSize: 512
  phases:
    - name: warmup
      type: WARMUP
      durationMs: 10000
    - name: steady
      type: STEADY
      durationMs: 20000
`
	mpHints := srv.inlayHints(mpURI, mpText, lspRange{})
	if len(mpHints) < 2 {
		t.Errorf("expected baseSpec and phases inlay hints on multi-phase test-request, got %+v", mpHints)
	}

	// Resilience inlay hints
	resURI := "file:///workspace/resilience-pod-kill.yaml"
	resText := `testRequest:
  type: LOAD
chaosSpec:
  experimentName: pod-kill
  disruptionType: POD_KILL
  steadyStateSec: 10
  delayBeforeSec: 5
  chaosDurationSec: 20
  maxRecoveryWaitSec: 60
`
	resHints := srv.inlayHints(resURI, resText, lspRange{})
	if len(resHints) == 0 || !strings.Contains(resHints[0].Label, "~95s") {
		t.Errorf("expected ~95s window inlay hint on resilience file, got %+v", resHints)
	}

	// Disruption inlay hints
	planURI := "file:///workspace/playbooks/plan.yaml"
	planText := `name: "Failover Plan"
baselineDurationSec: 10
isrTrackingTopic: kates-events
steps:
  - name: "Kill Leader"
    steadyStateSec: 15
    observationWindowSec: 30
    faultSpec:
      experimentName: kill-leader
      disruptionType: LEADER_ELECTION
      targetTopic: kates-events
      chaosDurationSec: 30
      probes:
        - name: "probe-produce"
          type: kafkaProbe
          mode: Continuous
          command: "produce kates-events"
          expectedOutput: "0"
`
	srv.setDoc(planURI, planText)
	planHints := srv.inlayHints(planURI, planText, lspRange{})
	if len(planHints) == 0 || !strings.Contains(planHints[0].Label, "~85s across 1 step(s)") {
		t.Errorf("expected ~85s window inlay hint on disruption plan, got %+v", planHints)
	}

	// Compliance inlay hints
	compURI := "file:///workspace/conformity/ref.yaml"
	compText := `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-ref
spec:
  brokers:
    count: 3
`
	srv.setDoc(compURI, compText)
	compHints := srv.inlayHints(compURI, compText, lspRange{})
	if len(compHints) == 0 || !strings.Contains(compHints[0].Label, "1 rule(s) declared · 0 waived") {
		t.Errorf("expected compliance rule count inlay hint, got %+v", compHints)
	}

	// 2. Document Symbols & CodeLens across all dialects
	flowURI := "file:///workspace/flow-ci.yaml"
	flowText := `name: "CI Flow"
steps:
  - name: step1
    action: test
    type: LOAD
`
	for _, tc := range []struct {
		uri, text string
		wantSyms  int
		wantLens  int
	}{
		{scURI, scText, 1, 4},
		{mpURI, mpText, 1, 0},
		{flowURI, flowText, 1, 2},
		{resURI, resText, 2, 2},
		{planURI, planText, 1, 3},
		{compURI, compText, 2, 2},
	} {
		syms := srv.documentSymbols(tc.uri, tc.text)
		if len(syms) != tc.wantSyms {
			t.Errorf("documentSymbols(%s): got %d symbols, want %d: %+v", tc.uri, len(syms), tc.wantSyms, syms)
		}
		lenses := srv.codeLens(tc.uri, tc.text)
		if len(lenses) != tc.wantLens {
			t.Errorf("codeLens(%s): got %d lenses, want %d: %+v", tc.uri, len(lenses), tc.wantLens, lenses)
		}
	}

	// 3. Folding Ranges
	folds := srv.foldingRanges(scURI, scText)
	hasCommentFold := false
	for _, f := range folds {
		if f.Kind == "comment" && f.StartLine == 0 && f.EndLine == 1 {
			hasCommentFold = true
		}
	}
	if !hasCommentFold {
		t.Errorf("expected comment folding range [0..1], got %+v", folds)
	}

	// 4. Find References, Go-to-Definition & Rename across Disruption Plan topic & probe
	refs := srv.references(planURI, planText, lspPosition{Line: 2, Character: 20})
	if len(refs) != 3 {
		t.Fatalf("expected 3 topic references across isrTrackingTopic, targetTopic, and kafkaProbe command, got %d: %+v", len(refs), refs)
	}
	defLoc := srv.definition(planURI, planText, lspPosition{Line: 10, Character: 20})
	if defLoc == nil || defLoc.Range.Start.Line != 2 {
		t.Errorf("expected Go-to-Definition on targetTopic to jump to line 2 (isrTrackingTopic), got %+v", defLoc)
	}
	renEdit := srv.rename(planURI, planText, lspPosition{Line: 2, Character: 20}, "orders-topic")
	if renEdit == nil || len(renEdit.Changes[planURI]) != 3 {
		t.Fatalf("expected 3 rename edits for topic, got %+v", renEdit)
	}
}

func TestLSPRefactoringAndDiff(t *testing.T) {
	srv := newLSPServer(io.Discard)
	scURI := "file:///workspace/perf-load.yaml"
	scText := `scenarios:
  - name: "Baseline Load"
    type: INTEGRITY
    spec:
      topic: kates-events
      records: 1000000
      recordSizeBytes: 2048
      parallelProducers: 2
      targetThroughput: 5000
      durationSeconds: 60
    validate:
      maxP99LatencyMs: 100
`
	resURI := "file:///workspace/resilience-pod-kill.yaml"
	compURI := "file:///workspace/conformity/ref.yaml"
	compText := `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-ref
spec:
  brokers:
    count: 3
`
	srv.setDoc(scURI, scText)
	srv.setDoc(compURI, compText)

	// 5. Quick-Fixes (unquoted-version, fault-requires-duration, redundant-bound) & FormatDocument
	unquotedComp := `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-ref
spec:
  versions:
    kafka:
      min: 3.9
`
	qActions := srv.codeActions(compURI, unquotedComp, lspRange{
		Start: lspPosition{Line: 0, Character: 0},
		End:   lspPosition{Line: 7, Character: 14},
	}, nil)
	var qaTitles []string
	for _, a := range qActions {
		qaTitles = append(qaTitles, a.Title)
	}
	if !slices.Contains(qaTitles, `Quote value as "3.9"`) {
		t.Errorf("expected unquoted-version Quick-Fix in %v", qaTitles)
	}

	redundantComp := `kind: KafkaReference
apiVersion: conformity.kates.io/v1alpha1
metadata:
  name: prod-ref
spec:
  brokers:
    count:
      oneOf: [3, 3]
`
	fmtActions := srv.codeActions(compURI, redundantComp, lspRange{
		Start: lspPosition{Line: 0, Character: 0},
		End:   lspPosition{Line: 8, Character: 12},
	}, nil)
	var fmtTitles []string
	for _, a := range fmtActions {
		fmtTitles = append(fmtTitles, a.Title)
	}
	if !slices.Contains(fmtTitles, "Format reference canonically (kates compliance fmt)") {
		t.Errorf("expected Format Quick-Fix in %v", fmtTitles)
	}
	fmtEdits := srv.formatDocument(compURI, redundantComp)
	if len(fmtEdits) == 0 || !strings.HasPrefix(fmtEdits[0].NewText, "apiVersion:") {
		t.Errorf("expected formatDocument to canonicalize KafkaReference key order, got %+v", fmtEdits)
	}

	zeroDurRes := `testRequest:
  type: LOAD
chaosSpec:
  experimentName: cpu-hog
  disruptionType: CPU_STRESS
  chaosDurationSec: 0
`
	durActions := srv.codeActions(resURI, zeroDurRes, lspRange{
		Start: lspPosition{Line: 5, Character: 0},
		End:   lspPosition{Line: 5, Character: 20},
	}, nil)
	hasDurFix := false
	for _, a := range durActions {
		if a.Title == "Set chaosDurationSec to 30" {
			hasDurFix = true
		}
	}
	if !hasDurFix {
		t.Errorf("expected 'Set chaosDurationSec to 30' Quick-Fix, got %+v", durActions)
	}

	// 6. Refactoring Code Actions (CLI <-> Wire spec keys & Scenario -> Resilience)
	actions := srv.codeActions(scURI, scText, lspRange{
		Start: lspPosition{Line: 6, Character: 0},
		End:   lspPosition{Line: 6, Character: 10},
	}, nil)
	var resConverted string
	titles := make([]string, 0, len(actions))
	for _, a := range actions {
		titles = append(titles, a.Title)
		if strings.Contains(a.Title, "Resilience Chaos Test") && a.Edit != nil {
			if edits := a.Edit.Changes[scURI]; len(edits) == 1 {
				resConverted = edits[0].NewText
			}
		}
	}
	if !slices.Contains(titles, "Convert spec keys to wire TestSpec format (numRecords, durationMs, ...)") {
		t.Errorf("missing CLI-to-wire conversion code action in %v", titles)
	}
	if resConverted == "" {
		t.Fatalf("missing Scenario-to-Resilience refactoring output in %v", titles)
	}
	if ds := lintResilienceFile([]byte(resConverted)); len(ds) > 0 {
		t.Errorf("converted ResilienceTestRequest has lint diagnostics: %+v\nYAML:\n%s", ds, resConverted)
	}

	// Wire -> CLI conversion action
	wireText := `type: LOAD
spec:
  numRecords: 5000
  numProducers: 2
  recordSize: 512
  durationMs: 30000
`
	wireActions := srv.codeActions("file:///workspace/test-request.yaml", wireText, lspRange{}, nil)
	hasWireToCLI := false
	for _, a := range wireActions {
		if strings.Contains(a.Title, "Convert spec keys to CLI scenario format") {
			hasWireToCLI = true
		}
	}
	if !hasWireToCLI {
		t.Errorf("expected wire-to-CLI conversion code action, got %+v", wireActions)
	}

	// 7. Compliance Waiver Scaffolding Code Action (both without and with existing waivers block)
	compActions := srv.codeActions(compURI, compText, lspRange{
		Start: lspPosition{Line: 6, Character: 4},
		End:   lspPosition{Line: 6, Character: 12},
	}, nil)
	hasWaiverAction := false
	for _, a := range compActions {
		if strings.Contains(a.Title, `Create compliance waiver for rule "brokers.count"`) {
			hasWaiverAction = true
		}
	}
	if !hasWaiverAction {
		t.Errorf("expected compliance waiver code action on brokers.count, got %+v", compActions)
	}
}

func TestLSPTemplatesAndScenarioDiff(t *testing.T) {
	srv := newLSPServer(io.Discard)
	scURI := "file:///workspace/perf-load.yaml"
	scText := `scenarios:
  - name: "Baseline Load"
    type: INTEGRITY
    spec:
      topic: kates-events
      records: 1000000
      recordSizeBytes: 2048
      parallelProducers: 2
      targetThroughput: 5000
      durationSeconds: 60
    validate:
      maxP99LatencyMs: 100
`
	srv.setDoc(scURI, scText)

	// 8. Templates & Scenario Diff (including on-disk file:// URIs via loadURIOrDoc)
	tpls := srv.listTemplates()
	if len(tpls) < 6 {
		t.Errorf("expected templates across all dialects, got %d", len(tpls))
	}
	for _, tpl := range tpls {
		id, _ := tpl["id"].(string)
		content, _ := tpl["content"].(string)
		d, ds := lintDocument(id+".yaml", []byte(content))
		if d == dialectUnknown {
			t.Errorf("template %s has unknown dialect", id)
		}
		for _, diag := range ds {
			if diag.Severity == 1 {
				t.Errorf("template %s has lint error: %+v", id, diag)
			}
		}
	}

	tmpDir := t.TempDir()
	rightFile := filepath.Join(tmpDir, "right.yaml")
	if err := os.WriteFile(rightFile, []byte(`type: STRESS
spec:
  records: 500000
  parallelProducers: 4
`), 0o644); err != nil {
		t.Fatal(err)
	}
	diffRes := srv.scenarioDiff(scURI, scText, "file://"+rightFile, "")
	md, _ := diffRes["markdown"].(string)
	if !strings.Contains(md, "Scenario Comparison") || !strings.Contains(md, "**changed**") {
		t.Errorf("unexpected scenarioDiff markdown: %q", md)
	}
}

func TestLSPLiveClusterCompletionsAndPreflight(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"clusterId":"cluster-live","brokerCount":3,"brokers":[{"id":0,"host":"b0","port":9092},{"id":1,"host":"b1","port":9092},{"id":2,"host":"b2","port":9092}]}`)
	})
	mux.HandleFunc("/api/cluster/topics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":["orders-events","payments-stream"]}`)
	})
	mux.HandleFunc("/api/cluster/groups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"groupId":"billing-cg","state":"Stable"}]}`)
	})
	mux.HandleFunc("/api/cluster/topology", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcpClusterLabTopology("cluster-live"))
	})
	mux.HandleFunc("/api/disruptions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"wouldSucceed":true,"totalBrokers":3,"warnings":["1 partition under-replicated during step 1"]}`)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	prevClient := apiClient
	apiClient = client.New(ts.URL)
	defer func() { apiClient = prevClient }()

	srv := newLSPServer(io.Discard)

	// 1. Live cluster completions across all supported keys
	for _, tc := range []struct {
		key      string
		wantItem string
	}{
		{"topic", "orders-events"},
		{"targetTopic", "payments-stream"},
		{"isrTrackingTopic", "orders-events"},
		{"consumerGroup", "billing-cg"},
		{"lagTrackingGroupId", "billing-cg"},
		{"targetNamespace", "kafka"},
		{"TARGET_NODE", "kind-worker"},
		{"targetBrokerId", "0"},
	} {
		items := srv.completeLiveClusterValues(tc.key)
		labels := make([]string, 0, len(items))
		for _, it := range items {
			labels = append(labels, it.Label)
		}
		if !slices.Contains(labels, tc.wantItem) {
			t.Errorf("completeLiveClusterValues(%q) = %v, want %q", tc.key, labels, tc.wantItem)
		}
	}

	// 2. Cluster preflight catches replicationFactor, minInsyncReplicas, and targetBrokerId exceeding live brokerCount (3)
	badBoundsYAML := `name: "Oversized Plan"
steps:
  - name: "Step 1"
    faultSpec:
      experimentName: pod-kill
      disruptionType: POD_KILL
      targetBrokerId: 5
`
	pf := srv.clusterPreflight("file:///workspace/playbooks/oversized.yaml", badBoundsYAML)
	if pf["connected"] != true || pf["brokerCount"] != 3 {
		t.Fatalf("unexpected preflight status: %+v", pf)
	}
	warnings, _ := pf["warnings"].([]string)
	hasBrokerWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "targetBrokerId (5) out of range") {
			hasBrokerWarn = true
		}
	}
	if !hasBrokerWarn {
		t.Errorf("expected targetBrokerId out-of-range warning in preflight, got %v", warnings)
	}

	// 3. Cluster preflight on scenario with replicationFactor: 5 > brokerCount: 3
	scnPF := srv.clusterPreflight("file:///workspace/scenarios/rf5.yaml", `type: LOAD
spec:
  replicationFactor: 5
  minInsyncReplicas: 4
`)
	scnWarns, _ := scnPF["warnings"].([]string)
	if len(scnWarns) < 2 {
		t.Errorf("expected warnings for replicationFactor (5) and minInsyncReplicas (4) exceeding brokerCount (3), got %v", scnWarns)
	}
}

func TestLSPValueCompletionsAndSchemaHover(t *testing.T) {
	srv := newLSPServer(io.Discard)

	resURI := "file:///workspace/resilience-test.yaml"
	resText := `testRequest:
  type: LOAD
  spec:
    acks: all
    compressionType: lz4
chaosSpec:
  experimentName: pod-kill
  disruptionType: POD_KILL
  targetLabel: "strimzi.io/kind=Kafka"
probes:
  - name: p1
    type: kafkaProbe
    mode: Continuous
    command: under-replicated-partitions
    comparator: "=="
    expectedOutput: "0"
`
	srv.setDoc(resURI, resText)

	// 1. Value completions for type, acks, compressionType, disruptionType, targetLabel, command, mode
	for _, tc := range []struct {
		name     string
		pos      lspPosition
		wantItem string
	}{
		{"TestType value", lspPosition{Line: 1, Character: 9}, "STRESS"},
		{"Schema enum acks", lspPosition{Line: 3, Character: 10}, "all"},
		{"Schema enum compressionType", lspPosition{Line: 4, Character: 21}, "zstd"},
		{"DisruptionType value", lspPosition{Line: 7, Character: 19}, "NETWORK_LATENCY"},
		{"Strimzi targetLabel value", lspPosition{Line: 8, Character: 16}, `"strimzi.io/component-type=kafka"`},
		{"Probe mode enum", lspPosition{Line: 12, Character: 11}, "Continuous"},
		{"kafkaProbe command value", lspPosition{Line: 13, Character: 14}, "under-replicated-partitions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := srv.complete(resURI, resText, tc.pos)
			labels := make([]string, 0, len(items))
			for _, it := range items {
				labels = append(labels, it.Label)
			}
			if !slices.Contains(labels, tc.wantItem) {
				t.Errorf("complete(%s) = %v, want %q", tc.name, labels, tc.wantItem)
			}
		})
	}

	// 2. Hover on DisruptionType ("POD_KILL") and schema properties ("chaosDurationSec" / "compressionType")
	dtHover := srv.hover(resURI, resText, lspPosition{Line: 7, Character: 20})
	if dtHover == nil || !strings.Contains(dtHover.Contents.Value, "POD_KILL") {
		t.Errorf("expected hover on POD_KILL DisruptionType, got %+v", dtHover)
	}
	propHover := srv.hover(resURI, resText, lspPosition{Line: 4, Character: 8})
	if propHover == nil || !strings.Contains(propHover.Contents.Value, "**`compressionType`**") || !strings.Contains(propHover.Contents.Value, "Allowed:") {
		t.Errorf("expected schema property hover on compressionType with Allowed:, got %+v", propHover)
	}
}
