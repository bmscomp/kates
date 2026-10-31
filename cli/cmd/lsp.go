package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/internal/compliance"
	"github.com/bmscomp/kates/cli/schemas"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var lspStdioFlag bool

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Start the Kates YAML Language Server (LSP 3.17 over stdio)",
	Long: `Starts the Kates Language Server Protocol (LSP) server over standard
input/output (--stdio). It powers IDE extensions for Visual Studio Code and
Visual Studio 2022+ with real-time error detection (unknown attributes,
cross-field invariants, FaultLimits bounds, compliance rules), context-aware
auto-completion, hover documentation, quick-fix code actions, semantic tokens,
go-to-definition for compliance waivers, formatting, and CodeLens across all 6
Kates YAML dialects:

  • Scenario files (scenarios.yaml, perf-*.yaml, load-test.yaml)
  • Multi-phase CreateTestRequest / TestScenario files
  • Flow pipelines (flow-*.yaml)
  • Resilience test specifications (resilience-*.yaml)
  • Disruption plans and playbooks (playbooks/*.yaml)
  • Compliance references (KafkaReference kates.io/v1alpha1)`,
	Example: `  kates lsp --stdio`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runLSPServer(cmd.Context(), os.Stdin, os.Stdout)
	},
}

func init() {
	lspCmd.Flags().BoolVar(&lspStdioFlag, "stdio", true, "Communicate over standard input/output using LSP JSON-RPC 2.0")
	rootCmd.AddCommand(lspCmd)
}

// yamlDialect identifies which of the 6 Kates YAML dialects a document uses.
type yamlDialect string

const (
	dialectUnknown     yamlDialect = ""
	dialectScenario    yamlDialect = "scenario"
	dialectTestRequest yamlDialect = "test-request"
	dialectFlow        yamlDialect = "flow"
	dialectResilience  yamlDialect = "resilience"
	dialectDisruption  yamlDialect = "disruption"
	dialectCompliance  yamlDialect = "compliance"
)

func (d yamlDialect) schemaFilename() string {
	switch d {
	case dialectScenario:
		return "scenario.v1.schema.json"
	case dialectTestRequest:
		return "test-request.v1.schema.json"
	case dialectFlow:
		return "flow.v1.schema.json"
	case dialectResilience:
		return "resilience.v1.schema.json"
	case dialectDisruption:
		return "disruption.v1.schema.json"
	case dialectCompliance:
		return "kafkareference.v1alpha1.schema.json"
	default:
		return ""
	}
}

var (
	katesSchemaDirectiveRE = regexp.MustCompile(`(?m)^#\s*kates-schema:\s*(\S+)`)
	yamlLSPSchemaRE        = regexp.MustCompile(`(?m)^#\s*yaml-language-server:\s*\$schema=(\S+)`)
)

// detectDialect determines a YAML document's Kates dialect using a 3-tier
// strategy:
//  1. Explicit header comment (# kates-schema: <dialect> or # yaml-language-server: $schema=...)
//  2. Content-based top-level YAML AST fingerprinting
//  3. Filename / path conventions
func detectDialect(uri string, src []byte) yamlDialect {
	if d := detectDialectByHeader(string(src)); d != dialectUnknown {
		return d
	}
	if d := detectDialectByAST(src); d != dialectUnknown {
		return d
	}
	return detectDialectByURI(uri)
}

func detectDialectByHeader(text string) yamlDialect {
	if m := katesSchemaDirectiveRE.FindStringSubmatch(text); m != nil {
		switch strings.ToLower(strings.TrimSpace(m[1])) {
		case "scenario", "scenarios":
			return dialectScenario
		case "test-request", "testrequest", "create-test-request":
			return dialectTestRequest
		case "flow", "pipeline":
			return dialectFlow
		case "resilience", "chaos":
			return dialectResilience
		case "disruption", "playbook":
			return dialectDisruption
		case "compliance", "conformity", "kafkareference":
			return dialectCompliance
		}
	}
	if m := yamlLSPSchemaRE.FindStringSubmatch(text); m != nil {
		base := strings.ToLower(filepath.Base(strings.TrimSpace(m[1])))
		switch {
		case strings.Contains(base, "kafkareference"):
			return dialectCompliance
		case strings.Contains(base, "resilience"):
			return dialectResilience
		case strings.Contains(base, "disruption"):
			return dialectDisruption
		case strings.Contains(base, "flow"):
			return dialectFlow
		case strings.Contains(base, "test-request"):
			return dialectTestRequest
		case strings.Contains(base, "scenario"):
			return dialectScenario
		}
	}
	return dialectUnknown
}

func detectDialectByAST(src []byte) yamlDialect {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return dialectUnknown
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return dialectUnknown
	}
	topKeys := map[string]*yaml.Node{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		topKeys[doc.Content[i].Value] = doc.Content[i+1]
	}
	if apiVer, ok := topKeys["apiVersion"]; ok && strings.Contains(apiVer.Value, "kates.io/") {
		return dialectCompliance
	}
	if kind, ok := topKeys["kind"]; ok && kind.Value == "KafkaReference" {
		return dialectCompliance
	}
	if _, ok := topKeys["testRequest"]; ok {
		return dialectResilience
	}
	if _, ok := topKeys["chaosSpec"]; ok {
		return dialectResilience
	}
	if _, ok := topKeys["scenarios"]; ok {
		return dialectScenario
	}
	if _, ok := topKeys["scenario"]; ok {
		return dialectTestRequest
	}
	if d := detectDialectFromStepsOrSpec(topKeys); d != dialectUnknown {
		return d
	}
	if _, ok := topKeys["type"]; ok {
		return dialectScenario
	}
	return dialectUnknown
}

func detectDialectFromStepsOrSpec(topKeys map[string]*yaml.Node) yamlDialect {
	if steps, ok := topKeys["steps"]; ok && steps.Kind == yaml.SequenceNode {
		for _, item := range steps.Content {
			if item.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(item.Content); j += 2 {
					switch item.Content[j].Value {
					case "faultSpec", "observationWindowSec", "requireRecovery", "steadyStateSec":
						return dialectDisruption
					case "action", "gate", "onFail":
						return dialectFlow
					}
				}
			}
		}
	}
	for _, k := range []string{"maxAffectedBrokers", "isrTrackingTopic", "lagTrackingGroupId", "autoRollback", "baselineDurationSec", "category"} {
		if _, ok := topKeys[k]; ok {
			return dialectDisruption
		}
	}
	if _, ok := topKeys["validate"]; ok {
		return dialectScenario
	}
	if spec, ok := topKeys["spec"]; ok && spec.Kind == yaml.MappingNode {
		for j := 0; j+1 < len(spec.Content); j += 2 {
			switch spec.Content[j].Value {
			case "numRecords", "numProducers", "recordSize", "durationMs", "throughput":
				return dialectTestRequest
			case "records", "parallelProducers", "recordSizeBytes", "durationSeconds":
				return dialectScenario
			case "versions", "controllers", "brokers", "kafka", "topics", "monitoring", "waivers":
				return dialectCompliance
			}
		}
	}
	return dialectUnknown
}

func detectDialectByURI(uri string) yamlDialect {
	lowerURI := strings.ToLower(uri)
	base := strings.ToLower(filepath.Base(uri))
	switch {
	case strings.Contains(lowerURI, "conformity") || strings.Contains(lowerURI, "compliance") || strings.Contains(base, "reference"):
		return dialectCompliance
	case strings.Contains(lowerURI, "playbook") || strings.Contains(base, "disruption"):
		return dialectDisruption
	case strings.Contains(base, "resilience") || strings.Contains(base, "chaos"):
		return dialectResilience
	case strings.Contains(base, "flow"):
		return dialectFlow
	case strings.Contains(base, "test-request"):
		return dialectTestRequest
	case strings.Contains(lowerURI, "scenario") || strings.HasPrefix(base, "perf-") || strings.HasPrefix(base, "load-"):
		return dialectScenario
	default:
		return dialectUnknown
	}
}

// LSP JSON-RPC 2.0 wire types.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspDiagnostic struct {
	Range    lspRange       `json:"range"`
	Severity int            `json:"severity"` // 1 = Error, 2 = Warning, 3 = Information, 4 = Hint
	Code     string         `json:"code,omitempty"`
	Source   string         `json:"source,omitempty"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data,omitempty"`
}

type lspPublishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes"`
}

type lspCodeAction struct {
	Title       string            `json:"title"`
	Kind        string            `json:"kind,omitempty"`
	Diagnostics []lspDiagnostic   `json:"diagnostics,omitempty"`
	IsPreferred bool              `json:"isPreferred,omitempty"`
	Edit        *lspWorkspaceEdit `json:"edit,omitempty"`
}

type lspCompletionItem struct {
	Label            string     `json:"label"`
	Kind             int        `json:"kind,omitempty"` // 10 = Property/Field, 12 = Value, 13 = Enum, 20 = EnumMember
	Detail           string     `json:"detail,omitempty"`
	Documentation    *lspMarkup `json:"documentation,omitempty"`
	InsertText       string     `json:"insertText,omitempty"`
	InsertTextFormat int        `json:"insertTextFormat,omitempty"` // 1 = PlainText, 2 = Snippet
	SortText         string     `json:"sortText,omitempty"`
}

type lspMarkup struct {
	Kind  string `json:"kind"` // "markdown"
	Value string `json:"value"`
}

type lspHover struct {
	Contents lspMarkup `json:"contents"`
	Range    *lspRange `json:"range,omitempty"`
}

type lspCommand struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type lspCodeLens struct {
	Range   lspRange    `json:"range"`
	Command *lspCommand `json:"command,omitempty"`
}

// lspServer manages open documents, parsed schemas, and JSON-RPC 2.0 I/O.
type lspServer struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	out     io.Writer
	docs    map[string]string // URI -> current document text
	schemas map[yamlDialect]map[string]any
	cluster *lspClusterCache
}

func newLSPServer(out io.Writer) *lspServer {
	s := &lspServer{
		out:     out,
		docs:    map[string]string{},
		schemas: map[yamlDialect]map[string]any{},
		cluster: &lspClusterCache{},
	}
	for _, d := range []yamlDialect{
		dialectScenario, dialectTestRequest, dialectFlow,
		dialectResilience, dialectDisruption, dialectCompliance,
	} {
		if raw, err := schemas.Files.ReadFile(d.schemaFilename()); err == nil {
			var parsed map[string]any
			if json.Unmarshal(raw, &parsed) == nil {
				s.schemas[d] = parsed
			}
		}
	}
	return s
}

func runLSPServer(ctx context.Context, in io.Reader, out io.Writer) error {
	srv := newLSPServer(out)
	reader := bufio.NewReader(in)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		payload, err := readLSPFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		shouldExit := srv.handleMessage(payload)
		if shouldExit {
			return nil
		}
	}
}

func readLSPFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if after, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(after))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length: %q", line)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (s *lspServer) send(msg rpcMessage) {
	msg.JSONRPC = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	var frame bytes.Buffer
	fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(data))
	frame.Write(data)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(frame.Bytes())
}

func (s *lspServer) reply(id json.RawMessage, result any) {
	s.send(rpcMessage{ID: id, Result: result})
}

func (s *lspServer) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	s.send(rpcMessage{Method: method, Params: raw})
}

func (s *lspServer) getDoc(uri string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[uri]
}

func (s *lspServer) setDoc(uri, text string) {
	s.mu.Lock()
	s.docs[uri] = text
	s.mu.Unlock()
}

func (s *lspServer) deleteDoc(uri string) {
	s.mu.Lock()
	delete(s.docs, uri)
	s.mu.Unlock()
}

func (s *lspServer) handleMessage(payload []byte) (shouldExit bool) {
	var msg rpcMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return false
	}

	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, lspInitializeResult())
		return false
	case "initialized":
		return false
	case "shutdown":
		s.reply(msg.ID, nil)
		return false
	case "exit":
		return true
	}

	if s.handleDocumentSync(msg) || s.handlePosQuery(msg) || s.handleDocQuery(msg) || s.handleCustomRequest(msg) {
		return false
	}

	if len(msg.ID) > 0 {
		s.send(rpcMessage{
			ID:    msg.ID,
			Error: &rpcError{Code: -32601, Message: "Method not found: " + msg.Method},
		})
	}
	return false
}

func lspInitializeResult() map[string]any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1,
				"save":      map[string]any{"includeText": true},
			},
			"completionProvider":         map[string]any{"triggerCharacters": []string{":", " ", "-", "[", "\""}},
			"hoverProvider":              true,
			"codeActionProvider":         map[string]any{"codeActionKinds": []string{"quickfix", "refactor", "refactor.rewrite", "source.fixAll"}},
			"definitionProvider":         true,
			"referencesProvider":         true,
			"renameProvider":             true,
			"documentSymbolProvider":     true,
			"inlayHintProvider":          true,
			"foldingRangeProvider":       true,
			"documentFormattingProvider": true,
			"codeLensProvider":           map[string]any{"resolveProvider": false},
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{"tokenTypes": lspSemanticTokenTypes, "tokenModifiers": lspSemanticTokenModifiers},
				"full":   true,
			},
		},
		"serverInfo": map[string]any{"name": "kates-lsp", "version": "1.1.0"},
	}
}

func (s *lspServer) handleDocumentSync(msg rpcMessage) bool {
	switch msg.Method {
	case "textDocument/didOpen":
		var params struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			s.setDoc(params.TextDocument.URI, params.TextDocument.Text)
			s.publishDiagnostics(params.TextDocument.URI, params.TextDocument.Text)
		}
		return true

	case "textDocument/didChange":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && len(params.ContentChanges) > 0 {
			text := params.ContentChanges[len(params.ContentChanges)-1].Text
			s.setDoc(params.TextDocument.URI, text)
			s.publishDiagnostics(params.TextDocument.URI, text)
		}
		return true

	case "textDocument/didSave":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Text *string `json:"text,omitempty"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			text := s.getDoc(params.TextDocument.URI)
			if params.Text != nil {
				text = *params.Text
				s.setDoc(params.TextDocument.URI, text)
			}
			s.publishDiagnostics(params.TextDocument.URI, text)
		}
		return true

	case "textDocument/didClose":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			s.deleteDoc(params.TextDocument.URI)
			s.notify("textDocument/publishDiagnostics", lspPublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: []lspDiagnostic{},
			})
		}
		return true
	}
	return false
}

func (s *lspServer) handlePosQuery(msg rpcMessage) bool {
	type docPosParams struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position lspPosition `json:"position"`
	}

	switch msg.Method {
	case "textDocument/completion", "textDocument/hover", "textDocument/definition", "textDocument/references":
		var p docPosParams
		ok := json.Unmarshal(msg.Params, &p) == nil
		uri, pos := p.TextDocument.URI, p.Position
		text := s.getDoc(uri)
		switch msg.Method {
		case "textDocument/completion":
			if ok {
				s.reply(msg.ID, s.complete(uri, text, pos))
			} else {
				s.reply(msg.ID, []lspCompletionItem{})
			}
		case "textDocument/hover":
			if ok {
				s.reply(msg.ID, s.hover(uri, text, pos))
			} else {
				s.reply(msg.ID, nil)
			}
		case "textDocument/definition":
			if ok {
				s.reply(msg.ID, s.definition(uri, text, pos))
			} else {
				s.reply(msg.ID, nil)
			}
		case "textDocument/references":
			if ok {
				s.reply(msg.ID, s.references(uri, text, pos))
			} else {
				s.reply(msg.ID, []lspLocation{})
			}
		}
		return true

	case "textDocument/codeAction":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Range   lspRange `json:"range"`
			Context struct {
				Diagnostics []lspDiagnostic `json:"diagnostics"`
			} `json:"context"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.reply(msg.ID, s.codeActions(p.TextDocument.URI, s.getDoc(p.TextDocument.URI), p.Range, p.Context.Diagnostics))
		} else {
			s.reply(msg.ID, []lspCodeAction{})
		}
		return true

	case "textDocument/rename":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position lspPosition `json:"position"`
			NewName  string      `json:"newName"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.reply(msg.ID, s.rename(p.TextDocument.URI, s.getDoc(p.TextDocument.URI), p.Position, p.NewName))
		} else {
			s.reply(msg.ID, nil)
		}
		return true
	}
	return false
}

func (s *lspServer) handleDocQuery(msg rpcMessage) bool {
	type docOnlyParams struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}

	switch msg.Method {
	case "textDocument/formatting", "textDocument/codeLens", "textDocument/semanticTokens/full", "textDocument/documentSymbol", "textDocument/foldingRange":
		var p docOnlyParams
		ok := json.Unmarshal(msg.Params, &p) == nil
		uri := p.TextDocument.URI
		text := s.getDoc(uri)
		switch msg.Method {
		case "textDocument/formatting":
			if ok {
				s.reply(msg.ID, s.formatDocument(uri, text))
			} else {
				s.reply(msg.ID, []lspTextEdit{})
			}
		case "textDocument/codeLens":
			if ok {
				s.reply(msg.ID, s.codeLens(uri, text))
			} else {
				s.reply(msg.ID, []lspCodeLens{})
			}
		case "textDocument/semanticTokens/full":
			if ok {
				s.reply(msg.ID, map[string]any{"data": s.semanticTokens(uri, text)})
			} else {
				s.reply(msg.ID, map[string]any{"data": []int{}})
			}
		case "textDocument/documentSymbol":
			if ok {
				s.reply(msg.ID, s.documentSymbols(uri, text))
			} else {
				s.reply(msg.ID, []lspDocumentSymbol{})
			}
		case "textDocument/foldingRange":
			if ok {
				s.reply(msg.ID, s.foldingRanges(uri, text))
			} else {
				s.reply(msg.ID, []lspFoldingRange{})
			}
		}
		return true

	case "textDocument/inlayHint":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Range lspRange `json:"range"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.reply(msg.ID, s.inlayHints(p.TextDocument.URI, s.getDoc(p.TextDocument.URI), p.Range))
		} else {
			s.reply(msg.ID, []lspInlayHint{})
		}
		return true
	}
	return false
}

func (s *lspServer) handleCustomRequest(msg rpcMessage) bool {
	switch msg.Method {
	case "kates/detectDialect":
		var p struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			d := detectDialect(p.URI, []byte(s.getDoc(p.URI)))
			s.reply(msg.ID, map[string]any{"dialect": string(d), "schema": d.schemaFilename()})
		} else {
			s.reply(msg.ID, map[string]any{"dialect": ""})
		}
		return true

	case "kates/listTemplates":
		s.reply(msg.ID, s.listTemplates())
		return true

	case "kates/scenarioDiff":
		var p struct {
			LeftURI   string `json:"leftUri"`
			LeftText  string `json:"leftText,omitempty"`
			RightURI  string `json:"rightUri"`
			RightText string `json:"rightText,omitempty"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.reply(msg.ID, s.scenarioDiff(p.LeftURI, p.LeftText, p.RightURI, p.RightText))
		} else {
			s.reply(msg.ID, map[string]any{"markdown": ""})
		}
		return true

	case "kates/clusterPreflight":
		var p struct {
			URI  string `json:"uri"`
			Text string `json:"text,omitempty"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			text := p.Text
			if text == "" {
				text = s.getDoc(p.URI)
			}
			s.reply(msg.ID, s.clusterPreflight(p.URI, text))
		} else {
			s.reply(msg.ID, map[string]any{"connected": false})
		}
		return true
	}
	return false
}

// lintDocument runs the offline Kates linter for the detected dialect.
func lintDocument(uri string, src []byte) (yamlDialect, []compliance.Diagnostic) {
	d := detectDialect(uri, src)
	switch d {
	case dialectScenario:
		return d, lintScenarioFile(uri, src)
	case dialectTestRequest:
		return d, lintTestRequestFile(src)
	case dialectFlow:
		return d, lintFlowFile(src)
	case dialectResilience:
		return d, lintResilienceFile(src)
	case dialectDisruption:
		return d, lintDisruptionFile(src)
	case dialectCompliance:
		return d, compliance.Compile(src, time.Now().UTC()).Diagnostics
	default:
		return dialectUnknown, nil
	}
}

var (
	didYouMeanRE  = regexp.MustCompile(`did you mean "([^"]+)"\?`)
	scenarioKeyRE = regexp.MustCompile(`the scenario key (?:for the producer rate )?is ([A-Za-z0-9_]+)`)
	wireSpecKeyRE = regexp.MustCompile(`(?:wire TestSpec|a flow spec|DisruptionStep) (?:uses|takes the request's names:) ([A-Za-z0-9_]+)`)
	quoteItRE     = regexp.MustCompile(`quote it \("([^"]+)"\)`)
)

func extractSuggestedReplacement(msg string) string {
	if m := didYouMeanRE.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	if m := scenarioKeyRE.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	if m := wireSpecKeyRE.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

func (s *lspServer) publishDiagnostics(uri, text string) {
	dialect, ds := lintDocument(uri, []byte(text))
	lines := strings.Split(text, "\n")
	out := make([]lspDiagnostic, 0, len(ds))
	source := "kates"
	if dialect != dialectUnknown {
		source = "kates (" + string(dialect) + ")"
	}
	for _, d := range ds {
		lineIdx := max(d.Line-1, 0)
		colIdx := max(d.Col-1, 0)
		endCol := colIdx + 1
		if lineIdx < len(lines) {
			lineStr := lines[lineIdx]
			if colIdx < len(lineStr) {
				endCol = scanTokenEnd(lineStr, colIdx)
			} else {
				colIdx = 0
				endCol = len(lineStr)
			}
		}
		sev := 1
		if d.Severity == compliance.Warning {
			sev = 2
		}
		msg := d.Message
		if d.Path != "" && !strings.Contains(msg, d.Path) {
			msg = d.Path + ": " + msg
		}
		data := map[string]any{
			"code":    d.Code,
			"path":    d.Path,
			"fixable": d.Fixable,
		}
		if repl := extractSuggestedReplacement(d.Message); repl != "" {
			data["replacement"] = repl
		} else if m := quoteItRE.FindStringSubmatch(d.Message); m != nil {
			data["quote"] = m[1]
		}
		out = append(out, lspDiagnostic{
			Range: lspRange{
				Start: lspPosition{Line: lineIdx, Character: colIdx},
				End:   lspPosition{Line: lineIdx, Character: endCol},
			},
			Severity: sev,
			Code:     d.Code,
			Source:   source,
			Message:  msg,
			Data:     data,
		})
	}
	slices.SortStableFunc(out, func(a, b lspDiagnostic) int {
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line - b.Range.Start.Line
		}
		return a.Range.Start.Character - b.Range.Start.Character
	})
	s.notify("textDocument/publishDiagnostics", lspPublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: out,
	})
}

func scanTokenEnd(line string, start int) int {
	if start >= len(line) {
		return start
	}
	if line[start] == '"' || line[start] == '\'' {
		quote := line[start]
		for i := start + 1; i < len(line); i++ {
			if line[i] == quote {
				return i + 1
			}
		}
		return len(line)
	}
	i := start
	for i < len(line) && !strings.ContainsRune(" \t:#,[]{}", rune(line[i])) {
		i++
	}
	if i == start {
		return min(start+1, len(line))
	}
	return i
}
