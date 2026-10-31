package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/internal/compliance"
	"gopkg.in/yaml.v3"
)

// LSP 3.17 wire types for InlayHint, DocumentSymbol, and FoldingRange.

type lspInlayHint struct {
	Position    lspPosition `json:"position"`
	Label       string      `json:"label"`
	Kind        int         `json:"kind,omitempty"` // 1 = Type, 2 = Parameter
	PaddingLeft bool        `json:"paddingLeft,omitempty"`
	Tooltip     *lspMarkup  `json:"tooltip,omitempty"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail,omitempty"`
	Kind           int                 `json:"kind"` // 2 = Module, 5 = Class, 8 = Field, 12 = Function, 13 = Variable, 15 = String
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspFoldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"` // "comment" | "region"
}

// inlayHints computes inline ghost-text annotations showing effective defaults,
// data footprint, aggregate throughput rate, planned duration, chaos windows,
// and compliance rule/waiver counts.
func (s *lspServer) inlayHints(uri, text string, _ lspRange) []lspInlayHint {
	dialect := detectDialect(uri, []byte(text))
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return []lspInlayHint{}
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return []lspInlayHint{}
	}
	lines := strings.Split(text, "\n")
	var hints []lspInlayHint

	switch dialect {
	case dialectScenario:
		top := mappingToMap(doc)
		if scSeq, ok := top["scenarios"]; ok && scSeq.Kind == yaml.SequenceNode {
			for _, item := range scSeq.Content {
				hints = append(hints, scenarioNodeHints(item, lines)...)
			}
		} else {
			hints = append(hints, scenarioNodeHints(doc, lines)...)
		}

	case dialectTestRequest:
		hints = append(hints, testRequestInlayHints(doc, lines)...)

	case dialectDisruption, dialectResilience, dialectCompliance:
		hints = append(hints, chaosAndComplianceInlayHints(dialect, doc, lines)...)
	}

	return hints
}

func makeLineHint(lines []string, line1Based int, label, tooltip string) *lspInlayHint {
	idx := line1Based - 1
	if idx < 0 || idx >= len(lines) {
		return nil
	}
	h := &lspInlayHint{
		Position:    lspPosition{Line: idx, Character: len(lines[idx])},
		Label:       label,
		Kind:        1,
		PaddingLeft: true,
	}
	if tooltip != "" {
		h.Tooltip = &lspMarkup{Kind: "markdown", Value: tooltip}
	}
	return h
}

func testRequestInlayHints(doc *yaml.Node, lines []string) []lspInlayHint {
	top := mappingToMap(doc)
	hints := scenarioNodeHints(doc, lines)
	sc, ok := top["scenario"]
	if !ok || sc.Kind != yaml.MappingNode {
		return hints
	}
	scMap := mappingToMap(sc)
	if bs, ok := scMap["baseSpec"]; ok && bs.Kind == yaml.MappingNode {
		if h := specFootprintHint("LOAD", bs, lines); h != nil {
			hints = append(hints, *h)
		}
	}
	if phs, ok := scMap["phases"]; ok && phs.Kind == yaml.SequenceNode {
		var totalMs int64
		for _, ph := range phs.Content {
			pm := mappingToMap(ph)
			if dNode, ok := pm["durationMs"]; ok {
				if iv, err := strconv.ParseInt(dNode.Value, 10, 64); err == nil && iv > 0 {
					totalMs += iv
				}
			}
		}
		if totalMs > 0 {
			if h := makeLineHint(lines, phs.Line-1,
				fmt.Sprintf("# %d phase(s) · total duration: %ds", len(phs.Content), totalMs/1000),
				"Sum of `durationMs` across all phases in `scenario.phases`."); h != nil {
				hints = append(hints, *h)
			}
		}
	}
	return hints
}

func chaosAndComplianceInlayHints(dialect yamlDialect, doc *yaml.Node, lines []string) []lspInlayHint {
	var hints []lspInlayHint
	switch dialect {
	case dialectDisruption:
		top := mappingToMap(doc)
		baselineSec := nodeInt(top["baselineDurationSec"], 10)
		if stepsKey, stepsVal := findMappingEntry(doc, "steps"); stepsKey != nil && stepsVal != nil && stepsVal.Kind == yaml.SequenceNode {
			totalSec := baselineSec
			for _, step := range stepsVal.Content {
				sm := mappingToMap(step)
				steady := nodeInt(sm["steadyStateSec"], 15)
				obs := nodeInt(sm["observationWindowSec"], 30)
				chaosDur := 0
				if fs, ok := sm["faultSpec"]; ok && fs.Kind == yaml.MappingNode {
					chaosDur = nodeInt(mappingToMap(fs)["chaosDurationSec"], 0)
				}
				totalSec += steady + chaosDur + obs
			}
			if h := makeLineHint(lines, stepsKey.Line,
				fmt.Sprintf("# total window: ~%ds across %d step(s)", totalSec, len(stepsVal.Content)),
				"Estimated minimum execution window (`baselineDurationSec` + Σ(`steadyStateSec` + `chaosDurationSec` + `observationWindowSec`))."); h != nil {
				hints = append(hints, *h)
			}
		}

	case dialectResilience:
		if csKey, csVal := findMappingEntry(doc, "chaosSpec"); csKey != nil && csVal != nil && csVal.Kind == yaml.MappingNode {
			cm := mappingToMap(csVal)
			steady := nodeInt(cm["steadyStateSec"], 15)
			delay := nodeInt(cm["delayBeforeSec"], 5)
			chaosDur := nodeInt(cm["chaosDurationSec"], 30)
			maxWait := nodeInt(cm["maxRecoveryWaitSec"], 120)
			total := steady + delay + chaosDur + maxWait
			if h := makeLineHint(lines, csKey.Line,
				fmt.Sprintf("# total window: ~%ds (steady %ds + delay %ds + chaos %ds + recovery ≤%ds)", total, steady, delay, chaosDur, maxWait),
				"Total resilience test envelope (`steadyStateSec` + `delayBeforeSec` + `chaosDurationSec` + `maxRecoveryWaitSec`)."); h != nil {
				hints = append(hints, *h)
			}
		}

	case dialectCompliance:
		rules := compliance.DeclaredRules(doc)
		waiversCount := 0
		if _, specVal := findMappingEntry(doc, "spec"); specVal != nil && specVal.Kind == yaml.MappingNode {
			if _, wVal := findMappingEntry(specVal, "waivers"); wVal != nil && wVal.Kind == yaml.SequenceNode {
				waiversCount = len(wVal.Content)
			}
		}
		if specKey, _ := findMappingEntry(doc, "spec"); specKey != nil {
			if h := makeLineHint(lines, specKey.Line,
				fmt.Sprintf("# %d rule(s) declared · %d waived", len(rules), waiversCount),
				"Total compliance rules compiled from `spec:` and active waivers under `spec.waivers`."); h != nil {
				hints = append(hints, *h)
			}
		}
	}
	return hints
}

func scenarioNodeHints(scNode *yaml.Node, lines []string) []lspInlayHint {
	if scNode == nil || scNode.Kind != yaml.MappingNode {
		return nil
	}
	var hints []lspInlayHint
	m := mappingToMap(scNode)
	testType := "LOAD"
	if tNode, ok := m["type"]; ok && tNode.Value != "" {
		upper := strings.ToUpper(tNode.Value)
		if slices.Contains(lintTestTypes(), upper) {
			testType = upper
			d := mcpScnTypeDefaultsFor(testType)
			idx := tNode.Line - 1
			if idx >= 0 && idx < len(lines) {
				hints = append(hints, lspInlayHint{
					Position:    lspPosition{Line: idx, Character: len(lines[idx])},
					Label:       fmt.Sprintf("# defaults: %dp · %s rec · %ds · acks=%s", d.numProducers, formatCompactInt(d.numRecords), d.durationMs/1000, d.acks),
					Kind:        1,
					PaddingLeft: true,
					Tooltip:     &lspMarkup{Kind: "markdown", Value: formatTestTypeDoc(testType)},
				})
			}
		}
	}
	if specKey, specVal := findMappingEntry(scNode, "spec"); specKey != nil && specVal != nil && specVal.Kind == yaml.MappingNode {
		if h := specFootprintHintAtKey(testType, specKey, specVal, lines); h != nil {
			hints = append(hints, *h)
		}
	}
	return hints
}

func specFootprintHint(testType string, specNode *yaml.Node, lines []string) *lspInlayHint {
	return specFootprintHintAtKey(testType, specNode, specNode, lines)
}

func specFootprintHintAtKey(testType string, keyNode, specNode *yaml.Node, lines []string) *lspInlayHint {
	d := mcpScnTypeDefaultsFor(testType)
	sm := mappingToMap(specNode)

	producers := d.numProducers
	if p := nodeInt(sm["parallelProducers"], 0); p > 0 {
		producers = p
	} else if p := nodeInt(sm["numProducers"], 0); p > 0 {
		producers = p
	}
	if testType != "STRESS" && testType != "CAPACITY" && producers < 1 {
		producers = 1
	}

	records := d.numRecords
	if r := nodeInt64(sm["records"], 0); r > 0 {
		records = r
	} else if r := nodeInt64(sm["numRecords"], 0); r > 0 {
		records = r
	}

	recSize := d.recordSize
	if rs := nodeInt(sm["recordSizeBytes"], 0); rs > 0 {
		recSize = rs
	} else if rs := nodeInt(sm["recordSize"], 0); rs > 0 {
		recSize = rs
	}

	tp := d.throughput
	if t := nodeInt(sm["targetThroughput"], -999); t != -999 {
		tp = t
	} else if t := nodeInt(sm["throughput"], -999); t != -999 {
		tp = t
	}

	durSec := int(d.durationMs / 1000)
	if ds := nodeInt(sm["durationSeconds"], 0); ds > 0 {
		durSec = ds
	} else if dm := nodeInt(sm["durationMs"], 0); dm > 0 {
		durSec = dm / 1000
	}
	plannedSec := durSec
	if testType == "INTEGRITY" {
		plannedSec = durSec * 2
	}

	totalBytes := float64(producers) * float64(records) * float64(recSize)
	footprint := formatBytesHuman(totalBytes)
	rateStr := "unthrottled"
	if tp > 0 {
		rateStr = fmt.Sprintf("%s rec/s", formatCompactInt(int64(producers*tp)))
	}

	idx := keyNode.Line - 1
	if idx < 0 || idx >= len(lines) {
		return nil
	}
	return &lspInlayHint{
		Position:    lspPosition{Line: idx, Character: len(lines[idx])},
		Label:       fmt.Sprintf("# footprint: ~%s · rate: %s · planned: %ds", footprint, rateStr, plannedSec),
		Kind:        1,
		PaddingLeft: true,
		Tooltip: &lspMarkup{
			Kind:  "markdown",
			Value: fmt.Sprintf("**Effective Load Estimate**\n- Producers: `%d`\n- Records/producer: `%d`\n- Record size: `%d B`\n- Total volume: `~%s`\n- Aggregate rate: `%s`\n- Planned deadline: `%ds`", producers, records, recSize, footprint, rateStr, plannedSec),
		},
	}
}

func formatCompactInt(n int64) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000 && n%1_000 == 0:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func formatBytesHuman(b float64) string {
	const (
		KiB = 1024.0
		MiB = 1024.0 * KiB
		GiB = 1024.0 * MiB
	)
	switch {
	case b >= GiB:
		return fmt.Sprintf("%.2f GiB", b/GiB)
	case b >= MiB:
		return fmt.Sprintf("%.1f MiB", b/MiB)
	case b >= KiB:
		return fmt.Sprintf("%.1f KiB", b/KiB)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

// documentSymbols builds a hierarchical outline tree for VS Code Breadcrumbs,
// Outline view, Go to Symbol (Cmd+Shift+O), and the Test Explorer controller.
func (s *lspServer) documentSymbols(uri, text string) []lspDocumentSymbol {
	dialect := detectDialect(uri, []byte(text))
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return []lspDocumentSymbol{}
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return []lspDocumentSymbol{}
	}
	lines := strings.Split(text, "\n")
	top := mappingToMap(doc)
	var symbols []lspDocumentSymbol

	switch dialect {
	case dialectScenario:
		if scSeq, ok := top["scenarios"]; ok && scSeq.Kind == yaml.SequenceNode {
			for i, item := range scSeq.Content {
				m := mappingToMap(item)
				name := nodeString(m["name"], fmt.Sprintf("scenario[%d]", i))
				tt := strings.ToUpper(nodeString(m["type"], "LOAD"))
				sym := makeNodeSymbol(name, tt, 12, item, lines)
				for _, childKey := range []string{"spec", "validate"} {
					if kNode, vNode := findMappingEntry(item, childKey); kNode != nil && vNode != nil {
						sym.Children = append(sym.Children, makeNodeSymbol(childKey, "", 8, kNode, lines))
					}
				}
				symbols = append(symbols, sym)
			}
		} else {
			name := nodeString(top["name"], "Scenario")
			tt := strings.ToUpper(nodeString(top["type"], "LOAD"))
			sym := makeNodeSymbol(name, tt, 12, doc, lines)
			for _, childKey := range []string{"spec", "validate"} {
				if kNode, vNode := findMappingEntry(doc, childKey); kNode != nil && vNode != nil {
					sym.Children = append(sym.Children, makeNodeSymbol(childKey, "", 8, kNode, lines))
				}
			}
			symbols = append(symbols, sym)
		}

	case dialectTestRequest:
		if sc, ok := top["scenario"]; ok && sc.Kind == yaml.MappingNode {
			sm := mappingToMap(sc)
			name := nodeString(sm["name"], "Multi-Phase Scenario")
			sym := makeNodeSymbol(name, "TestScenario", 5, sc, lines)
			if phs, ok := sm["phases"]; ok && phs.Kind == yaml.SequenceNode {
				for i, ph := range phs.Content {
					pm := mappingToMap(ph)
					pName := nodeString(pm["name"], fmt.Sprintf("phase[%d]", i))
					pType := nodeString(pm["type"], "")
					sym.Children = append(sym.Children, makeNodeSymbol(pName, pType, 12, ph, lines))
				}
			}
			symbols = append(symbols, sym)
		} else {
			tt := strings.ToUpper(nodeString(top["type"], "CreateTestRequest"))
			symbols = append(symbols, makeNodeSymbol(tt, nodeString(top["backend"], "native"), 12, doc, lines))
		}

	case dialectFlow:
		flowName := nodeString(top["name"], "Flow Pipeline")
		rootSym := makeNodeSymbol(flowName, "FlowPipeline", 2, doc, lines)
		if steps, ok := top["steps"]; ok && steps.Kind == yaml.SequenceNode {
			for i, st := range steps.Content {
				sm := mappingToMap(st)
				sName := nodeString(sm["name"], fmt.Sprintf("step[%d]", i))
				sAct := nodeString(sm["action"], "")
				rootSym.Children = append(rootSym.Children, makeNodeSymbol(sName, sAct, 12, st, lines))
			}
		}
		symbols = append(symbols, rootSym)

	case dialectDisruption, dialectResilience, dialectCompliance:
		symbols = append(symbols, chaosAndComplianceSymbols(dialect, doc, top, lines)...)
	}

	return symbols
}

func chaosAndComplianceSymbols(dialect yamlDialect, doc *yaml.Node, top map[string]*yaml.Node, lines []string) []lspDocumentSymbol {
	var symbols []lspDocumentSymbol
	switch dialect {
	case dialectDisruption:
		planName := nodeString(top["name"], "Disruption Plan")
		rootSym := makeNodeSymbol(planName, "DisruptionPlan", 2, doc, lines)
		if steps, ok := top["steps"]; ok && steps.Kind == yaml.SequenceNode {
			for i, st := range steps.Content {
				sm := mappingToMap(st)
				sName := nodeString(sm["name"], fmt.Sprintf("step[%d]", i))
				dt := ""
				if fs, ok := sm["faultSpec"]; ok && fs.Kind == yaml.MappingNode {
					dt = nodeString(mappingToMap(fs)["disruptionType"], "")
				}
				rootSym.Children = append(rootSym.Children, makeNodeSymbol(sName, dt, 12, st, lines))
			}
		}
		symbols = append(symbols, rootSym)

	case dialectResilience:
		for _, section := range []string{"testRequest", "chaosSpec", "sla"} {
			if kNode, vNode := findMappingEntry(doc, section); kNode != nil && vNode != nil {
				detail := ""
				if section == "chaosSpec" && vNode.Kind == yaml.MappingNode {
					detail = nodeString(mappingToMap(vNode)["disruptionType"], "")
				}
				sym := makeNodeSymbol(section, detail, 5, kNode, lines)
				if section == "chaosSpec" && vNode.Kind == yaml.MappingNode {
					if probes, ok := mappingToMap(vNode)["probes"]; ok && probes.Kind == yaml.SequenceNode {
						for i, p := range probes.Content {
							pm := mappingToMap(p)
							sym.Children = append(sym.Children, makeNodeSymbol(
								nodeString(pm["name"], fmt.Sprintf("probe[%d]", i)),
								nodeString(pm["type"], ""), 12, p, lines))
						}
					}
				}
				symbols = append(symbols, sym)
			}
		}

	case dialectCompliance:
		for _, section := range []string{"metadata", "spec"} {
			if kNode, vNode := findMappingEntry(doc, section); kNode != nil && vNode != nil {
				sym := makeNodeSymbol(section, "", 2, kNode, lines)
				if section == "spec" && vNode.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(vNode.Content); i += 2 {
						ck, cv := vNode.Content[i], vNode.Content[i+1]
						childSym := makeNodeSymbol(ck.Value, "", 5, ck, lines)
						if ck.Value == "waivers" && cv.Kind == yaml.SequenceNode {
							for _, w := range cv.Content {
								ruleID := nodeString(mappingToMap(w)["rule"], "waiver")
								childSym.Children = append(childSym.Children, makeNodeSymbol(ruleID, "waiver", 13, w, lines))
							}
						}
						sym.Children = append(sym.Children, childSym)
					}
				}
				symbols = append(symbols, sym)
			}
		}
	}
	return symbols
}

func makeNodeSymbol(name, detail string, kind int, n *yaml.Node, lines []string) lspDocumentSymbol {
	startLine := max(n.Line-1, 0)
	startCol := max(n.Column-1, 0)
	endLine := max(nodeMaxLine(n)-1, startLine)
	endCol := 0
	if endLine < len(lines) {
		endCol = len(lines[endLine])
	}
	selEndCol := startCol + len(name)
	if startLine < len(lines) && selEndCol > len(lines[startLine]) {
		selEndCol = len(lines[startLine])
	}
	return lspDocumentSymbol{
		Name:   name,
		Detail: detail,
		Kind:   kind,
		Range: lspRange{
			Start: lspPosition{Line: startLine, Character: startCol},
			End:   lspPosition{Line: endLine, Character: endCol},
		},
		SelectionRange: lspRange{
			Start: lspPosition{Line: startLine, Character: startCol},
			End:   lspPosition{Line: startLine, Character: selEndCol},
		},
	}
}

func nodeMaxLine(n *yaml.Node) int {
	if n == nil {
		return 1
	}
	maxL := n.Line
	for _, c := range n.Content {
		if cl := nodeMaxLine(c); cl > maxL {
			maxL = cl
		}
	}
	return maxL
}

// references finds all occurrences of the symbol under the cursor (compliance
// rules/listeners, topics, or consumer groups) across the current document.
func (s *lspServer) references(uri, text string, pos lspPosition) []lspLocation {
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	token, _ := wordAtPosition(lines[pos.Line], pos.Line, pos.Character)
	target := strings.Trim(token, `"'`)
	if target == "" {
		return nil
	}

	dialect := detectDialect(uri, []byte(text))
	if dialect == dialectCompliance {
		var locs []lspLocation
		for _, r := range compliance.DeclaredRules(doc) {
			if r.ID == target || strings.HasSuffix(r.ID, "."+target) {
				locs = append(locs, lineColLocation(uri, lines, r.Line, r.Col))
			}
		}
		collectScalarValueLocations(uri, lines, doc, "rule", target, &locs)
		return locs
	}

	// Topics & consumer groups across scenario, resilience, disruption, and flow documents.
	var locs []lspLocation
	collectTopicAndGroupLocations(uri, lines, doc, target, &locs)
	return locs
}

// rename atomically renames a topic, consumer group, or compliance listener/rule
// across all references in the document.
func (s *lspServer) rename(uri, text string, pos lspPosition, newName string) *lspWorkspaceEdit {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return nil
	}
	locs := s.references(uri, text, pos)
	if len(locs) == 0 {
		return nil
	}
	lines := strings.Split(text, "\n")
	token, _ := wordAtPosition(lines[pos.Line], pos.Line, pos.Character)
	oldTarget := strings.Trim(token, `"'`)

	var edits []lspTextEdit
	for _, loc := range locs {
		lineIdx := loc.Range.Start.Line
		if lineIdx < 0 || lineIdx >= len(lines) {
			continue
		}
		lineStr := lines[lineIdx]
		seg := lineStr[loc.Range.Start.Character:min(loc.Range.End.Character, len(lineStr))]
		unquoted := strings.Trim(seg, `"'`)
		var replacement string
		switch {
		case strings.HasPrefix(unquoted, "produce ") || strings.HasPrefix(unquoted, "consumer-lag "):
			parts := strings.Fields(unquoted)
			if len(parts) == 2 && parts[1] == oldTarget {
				replacement = parts[0] + " " + newName
			} else {
				continue
			}
		case strings.HasPrefix(unquoted, "listeners.require.") && oldTarget != unquoted:
			replacement = "listeners.require." + newName
		default:
			replacement = newName
		}
		if len(seg) >= 2 && (seg[0] == '"' || seg[0] == '\'') {
			q := string(seg[0])
			replacement = q + replacement + q
		}
		edits = append(edits, lspTextEdit{
			Range:   loc.Range,
			NewText: replacement,
		})
	}
	if len(edits) == 0 {
		return nil
	}
	return &lspWorkspaceEdit{
		Changes: map[string][]lspTextEdit{uri: edits},
	}
}

func lineColLocation(uri string, lines []string, line1, col1 int) lspLocation {
	l := max(line1-1, 0)
	c := max(col1-1, 0)
	endC := c + 1
	if l < len(lines) && c < len(lines[l]) {
		endC = scanTokenEnd(lines[l], c)
	}
	return lspLocation{
		URI: uri,
		Range: lspRange{
			Start: lspPosition{Line: l, Character: c},
			End:   lspPosition{Line: l, Character: endC},
		},
	}
}

func collectScalarValueLocations(uri string, lines []string, n *yaml.Node, keyFilter, target string, out *[]lspLocation) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if (keyFilter == "" || k.Value == keyFilter) && v.Kind == yaml.ScalarNode {
				if v.Value == target || strings.HasSuffix(v.Value, "."+target) {
					*out = append(*out, lineColLocation(uri, lines, v.Line, v.Column))
				}
			}
			collectScalarValueLocations(uri, lines, v, keyFilter, target, out)
		}
		return
	}
	for _, c := range n.Content {
		collectScalarValueLocations(uri, lines, c, keyFilter, target, out)
	}
}

var topicAndGroupKeys = []string{"topic", "targetTopic", "isrTrackingTopic", "consumerGroup", "lagTrackingGroupId"}

func collectTopicAndGroupLocations(uri string, lines []string, n *yaml.Node, target string, out *[]lspLocation) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if v.Kind == yaml.ScalarNode {
				if slices.Contains(topicAndGroupKeys, k.Value) && v.Value == target {
					*out = append(*out, lineColLocation(uri, lines, v.Line, v.Column))
				} else if k.Value == "command" {
					fields := strings.Fields(strings.TrimSpace(v.Value))
					if len(fields) == 2 && (fields[0] == "produce" || fields[0] == "consumer-lag") && fields[1] == target {
						l := max(v.Line-1, 0)
						c := max(v.Column-1, 0)
						endC := c + len(v.Value)
						if l < len(lines) {
							lineStr := lines[l]
							if c < len(lineStr) && (lineStr[c] == '"' || lineStr[c] == '\'') {
								endC = min(c+len(v.Value)+2, len(lineStr))
							} else {
								endC = min(c+len(v.Value), len(lineStr))
							}
						}
						*out = append(*out, lspLocation{
							URI: uri,
							Range: lspRange{
								Start: lspPosition{Line: l, Character: c},
								End:   lspPosition{Line: l, Character: endC},
							},
						})
					}
				}
			}
			collectTopicAndGroupLocations(uri, lines, v, target, out)
		}
		return
	}
	for _, c := range n.Content {
		collectTopicAndGroupLocations(uri, lines, c, target, out)
	}
}

// foldingRanges returns foldable regions for multi-line YAML mappings, sequence
// items, and contiguous comment blocks.
func (s *lspServer) foldingRanges(_ string, text string) []lspFoldingRange {
	var ranges []lspFoldingRange
	lines := strings.Split(text, "\n")

	// 1. Contiguous comment blocks
	commentStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if commentStart < 0 {
				commentStart = i
			}
		} else {
			if commentStart >= 0 && i-1 > commentStart {
				ranges = append(ranges, lspFoldingRange{StartLine: commentStart, EndLine: i - 1, Kind: "comment"})
			}
			commentStart = -1
		}
	}
	if commentStart >= 0 && len(lines)-1 > commentStart {
		ranges = append(ranges, lspFoldingRange{StartLine: commentStart, EndLine: len(lines) - 1, Kind: "comment"})
	}

	// 2. Multi-line YAML AST nodes
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) == nil && len(root.Content) > 0 {
		collectNodeFoldingRanges(root.Content[0], &ranges)
	}
	return ranges
}

func collectNodeFoldingRanges(n *yaml.Node, out *[]lspFoldingRange) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			start := max(k.Line-1, 0)
			end := max(nodeMaxLine(v)-1, start)
			if end > start {
				*out = append(*out, lspFoldingRange{StartLine: start, EndLine: end, Kind: "region"})
			}
			collectNodeFoldingRanges(v, out)
		}
		return
	}
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			start := max(item.Line-1, 0)
			end := max(nodeMaxLine(item)-1, start)
			if end > start {
				*out = append(*out, lspFoldingRange{StartLine: start, EndLine: end, Kind: "region"})
			}
			collectNodeFoldingRanges(item, out)
		}
	}
}

// refactorCodeActions provides non-diagnostic refactoring Code Actions:
//  1. Convert spec keys between CLI scenario format and wire TestSpec format
//  2. Convert a CLI scenario into a ResilienceTestRequest
//  3. Scaffold a compliance waiver for the rule under the cursor
func (s *lspServer) refactorCodeActions(uri, text string, rng lspRange) []lspCodeAction {
	dialect := detectDialect(uri, []byte(text))
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	lines := strings.Split(text, "\n")
	var actions []lspCodeAction

	// 1. CLI <-> Wire spec key conversion
	cliToWire := map[string]string{
		"records":           "numRecords",
		"parallelProducers": "numProducers",
		"recordSizeBytes":   "recordSize",
	}
	wireToCLI := map[string]string{
		"numRecords":   "records",
		"numProducers": "parallelProducers",
		"recordSize":   "recordSizeBytes",
	}
	var toWireEdits, toCLIEdits []lspTextEdit
	collectSpecConversionEdits(doc, lines, cliToWire, wireToCLI, &toWireEdits, &toCLIEdits)
	if len(toWireEdits) > 0 {
		actions = append(actions, lspCodeAction{
			Title: "Convert spec keys to wire TestSpec format (numRecords, durationMs, ...)",
			Kind:  "refactor.rewrite",
			Edit:  &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: toWireEdits}},
		})
	}
	if len(toCLIEdits) > 0 {
		actions = append(actions, lspCodeAction{
			Title: "Convert spec keys to CLI scenario format (records, durationSeconds, ...)",
			Kind:  "refactor.rewrite",
			Edit:  &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: toCLIEdits}},
		})
	}

	// 2. Convert Scenario -> ResilienceTestRequest
	if dialect == dialectScenario {
		if resYAML := convertScenarioToResilienceYAML(text); resYAML != "" {
			lastLine := max(len(lines)-1, 0)
			lastCol := 0
			if len(lines) > 0 {
				lastCol = len(lines[lastLine])
			}
			actions = append(actions, lspCodeAction{
				Title: "Refactor: Convert scenario to Resilience Chaos Test (ResilienceTestRequest)",
				Kind:  "refactor.rewrite",
				Edit: &lspWorkspaceEdit{
					Changes: map[string][]lspTextEdit{
						uri: {{
							Range: lspRange{
								Start: lspPosition{Line: 0, Character: 0},
								End:   lspPosition{Line: lastLine, Character: lastCol},
							},
							NewText: resYAML,
						}},
					},
				},
			})
		}
	}

	// 3. Create compliance waiver for rule at cursor
	if dialect == dialectCompliance {
		cursorLine1 := rng.Start.Line + 1
		rules := compliance.DeclaredRules(doc)
		var matchedRule *compliance.DeclaredRule
		for i := range rules {
			if rules[i].Line == cursorLine1 {
				matchedRule = &rules[i]
				break
			}
		}
		if matchedRule != nil {
			expiry := time.Now().UTC().AddDate(0, 3, 0).Format("2006-01-02")
			edit := buildComplianceWaiverEdit(doc, lines, matchedRule.ID, expiry)
			if edit != nil {
				actions = append(actions, lspCodeAction{
					Title: fmt.Sprintf("Create compliance waiver for rule %q", matchedRule.ID),
					Kind:  "refactor",
					Edit:  &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: {*edit}}},
				})
			}
		}
	}

	return actions
}

func collectSpecConversionEdits(n *yaml.Node, lines []string, cliToWire, wireToCLI map[string]string, toWire, toCLI *[]lspTextEdit) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			l := max(k.Line-1, 0)
			c := max(k.Column-1, 0)
			if wireKey, ok := cliToWire[k.Value]; ok {
				*toWire = append(*toWire, lspTextEdit{
					Range: lspRange{
						Start: lspPosition{Line: l, Character: c},
						End:   lspPosition{Line: l, Character: c + len(k.Value)},
					},
					NewText: wireKey,
				})
			} else if k.Value == "durationSeconds" && v.Kind == yaml.ScalarNode {
				if sec, err := strconv.ParseInt(v.Value, 10, 64); err == nil && l == v.Line-1 && l < len(lines) {
					valEnd := max(v.Column-1, 0) + len(v.Value)
					*toWire = append(*toWire, lspTextEdit{
						Range: lspRange{
							Start: lspPosition{Line: l, Character: c},
							End:   lspPosition{Line: l, Character: valEnd},
						},
						NewText: fmt.Sprintf("durationMs: %d", sec*1000),
					})
				}
			}
			if cliKey, ok := wireToCLI[k.Value]; ok {
				*toCLI = append(*toCLI, lspTextEdit{
					Range: lspRange{
						Start: lspPosition{Line: l, Character: c},
						End:   lspPosition{Line: l, Character: c + len(k.Value)},
					},
					NewText: cliKey,
				})
			} else if k.Value == "durationMs" && v.Kind == yaml.ScalarNode {
				if ms, err := strconv.ParseInt(v.Value, 10, 64); err == nil && ms >= 1000 && l == v.Line-1 && l < len(lines) {
					valEnd := max(v.Column-1, 0) + len(v.Value)
					*toCLI = append(*toCLI, lspTextEdit{
						Range: lspRange{
							Start: lspPosition{Line: l, Character: c},
							End:   lspPosition{Line: l, Character: valEnd},
						},
						NewText: fmt.Sprintf("durationSeconds: %d", ms/1000),
					})
				}
			}
			collectSpecConversionEdits(v, lines, cliToWire, wireToCLI, toWire, toCLI)
		}
		return
	}
	for _, ch := range n.Content {
		collectSpecConversionEdits(ch, lines, cliToWire, wireToCLI, toWire, toCLI)
	}
}

func convertScenarioToResilienceYAML(text string) string {
	sf, err := parseScenarioFile("scenario.yaml", []byte(text))
	if err != nil || len(sf.Scenarios) == 0 {
		return ""
	}
	req := scenarioToRequest(sf.Scenarios[0])
	topic, acks, partitions, rf, isr, numRecords, recordSize, throughput := extractResilienceSpecParams(req)

	var b strings.Builder
	b.WriteString("# kates-schema: resilience\n")
	b.WriteString("testRequest:\n")
	fmt.Fprintf(&b, "  type: %s\n", strings.ToUpper(req.TestType))
	b.WriteString("  spec:\n")
	fmt.Fprintf(&b, "    topic: %s\n", topic)
	fmt.Fprintf(&b, "    partitions: %d\n", partitions)
	fmt.Fprintf(&b, "    replicationFactor: %d\n", rf)
	fmt.Fprintf(&b, "    minInsyncReplicas: %d\n", isr)
	fmt.Fprintf(&b, "    numRecords: %d\n", numRecords)
	if recordSize > 0 {
		fmt.Fprintf(&b, "    recordSize: %d\n", recordSize)
	}
	if throughput > 0 {
		fmt.Fprintf(&b, "    throughput: %d\n", throughput)
	}
	fmt.Fprintf(&b, "    acks: %q\n", acks)
	b.WriteString("steadyStateSec: 15\nmaxRecoveryWaitSec: 120\nchaosSpec:\n")
	b.WriteString("  experimentName: leader-failover\n  disruptionType: LEADER_ELECTION\n")
	b.WriteString("  targetLabel: \"strimzi.io/kind=Kafka\"\n  delayBeforeSec: 5\n  chaosDurationSec: 30\n")
	b.WriteString("probes:\n  - name: urp-zero\n    type: kafkaProbe\n    mode: Continuous\n")
	b.WriteString("    command: under-replicated-partitions\n    comparator: \"==\"\n    expectedOutput: \"0\"\n")
	return b.String()
}

func extractResilienceSpecParams(req *client.CreateTestRequest) (topic, acks string, partitions, rf, isr, numRecords, recordSize, throughput int) {
	topic, acks = "kates-resilience-events", "all"
	partitions, rf, isr, numRecords = 6, 3, 2, 500000
	if req == nil || req.Spec == nil {
		return
	}
	if req.Spec.Topic != "" {
		topic = req.Spec.Topic
	}
	if req.Spec.Partitions > 0 {
		partitions = req.Spec.Partitions
	}
	if req.Spec.ReplicationFactor > 0 {
		rf = req.Spec.ReplicationFactor
	}
	if req.Spec.MinInsyncReplicas > 0 {
		isr = req.Spec.MinInsyncReplicas
	}
	if req.Spec.Records > 0 {
		numRecords = req.Spec.Records
	}
	recordSize = req.Spec.RecordSizeBytes
	throughput = req.Spec.TargetThroughput
	if req.Spec.Acks != "" {
		acks = req.Spec.Acks
	}
	return
}

func buildComplianceWaiverEdit(doc *yaml.Node, lines []string, ruleID, expiry string) *lspTextEdit {
	_, specVal := findMappingEntry(doc, "spec")
	if specVal == nil || specVal.Kind != yaml.MappingNode {
		return nil
	}
	waiverEntry := fmt.Sprintf("    - rule: %s\n      reason: \"TODO: document justification\"\n      until: %q\n      by: \"sre@example.com\"\n", ruleID, expiry)
	if _, wVal := findMappingEntry(specVal, "waivers"); wVal != nil && wVal.Kind == yaml.SequenceNode {
		insertLine := nodeMaxLine(wVal)
		return &lspTextEdit{
			Range: lspRange{
				Start: lspPosition{Line: insertLine, Character: 0},
				End:   lspPosition{Line: insertLine, Character: 0},
			},
			NewText: waiverEntry,
		}
	}
	insertLine := len(lines)
	prefix := ""
	if len(lines) > 0 && lines[len(lines)-1] != "" {
		prefix = "\n"
	}
	return &lspTextEdit{
		Range: lspRange{
			Start: lspPosition{Line: max(insertLine-1, 0), Character: len(lines[max(insertLine-1, 0)])},
			End:   lspPosition{Line: max(insertLine-1, 0), Character: len(lines[max(insertLine-1, 0)])},
		},
		NewText: prefix + "  waivers:\n" + waiverEntry,
	}
}

// Live Cluster Cache & Preflight.

type lspClusterCache struct {
	mu          sync.Mutex
	fetchedAt   time.Time
	connected   bool
	brokerCount int
	brokerIDs   []string
	topics      []string
	groups      []string
	namespaces  []string
	nodes       []string
}

func (c *lspClusterCache) snapshot() (connected bool, brokers int, brokerIDs, topics, groups, namespaces, nodes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetchedAt) > 15*time.Second && apiClient != nil {
		c.fetchedAt = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
		defer cancel()
		if info, err := apiClient.ClusterInfo(ctx); err == nil && info != nil {
			c.connected = true
			c.brokerCount = info.BrokerCount.Int()
			ids := make([]string, 0, len(info.Brokers))
			for _, b := range info.Brokers {
				ids = append(ids, strconv.Itoa(b.ID.Int()))
			}
			c.brokerIDs = ids
			if tList, err := apiClient.Topics(ctx); err == nil {
				c.topics = tList
			}
			if gList, err := apiClient.ConsumerGroups(ctx); err == nil {
				gs := make([]string, 0, len(gList))
				for _, g := range gList {
					if g.GroupID != "" {
						gs = append(gs, g.GroupID)
					}
				}
				c.groups = gs
			}
			if topo, err := apiClient.MCPClusterTopology(ctx); err == nil && topo != nil {
				if topo.Cluster != nil && topo.Cluster.Namespace != "" {
					c.namespaces = []string{topo.Cluster.Namespace}
				}
				ns := make([]string, 0, len(topo.Nodes))
				for _, n := range topo.Nodes {
					if n.K8sNode != "" && !slices.Contains(ns, n.K8sNode) {
						ns = append(ns, n.K8sNode)
					}
				}
				c.nodes = ns
			}
		} else {
			c.connected = false
		}
	}
	return c.connected, c.brokerCount, c.brokerIDs, c.topics, c.groups, c.namespaces, c.nodes
}

func (s *lspServer) completeLiveClusterValues(key string) []lspCompletionItem {
	if s.cluster == nil {
		return nil
	}
	connected, _, brokerIDs, topics, groups, namespaces, nodes := s.cluster.snapshot()
	if !connected {
		return nil
	}
	var values []string
	var detail string
	switch key {
	case "topic", "targetTopic", "isrTrackingTopic":
		values = topics
		detail = "Live Kafka topic on connected cluster"
	case "consumerGroup", "lagTrackingGroupId":
		values = groups
		detail = "Live consumer group on connected cluster"
	case "targetNamespace":
		values = namespaces
		detail = "Active Strimzi Kafka namespace"
	case "TARGET_NODE":
		values = nodes
		detail = "Kubernetes worker node hosting Kafka pods"
	case "targetBrokerId":
		values = brokerIDs
		detail = "Active Kafka broker ID on connected cluster"
	default:
		return nil
	}
	items := make([]lspCompletionItem, 0, len(values))
	for i, v := range values {
		items = append(items, lspCompletionItem{
			Label:    v,
			Kind:     12,
			Detail:   detail,
			SortText: fmt.Sprintf("0_live_%03d_%s", i, v),
		})
	}
	return items
}

func (s *lspServer) clusterPreflight(uri, text string) map[string]any {
	dialect := detectDialect(uri, []byte(text))
	_, offlineDiags := lintDocument(uri, []byte(text))
	offlineErrs := 0
	for _, d := range offlineDiags {
		if d.Severity == compliance.Error {
			offlineErrs++
		}
	}
	connected, brokers, _, topics, _, _, _ := s.cluster.snapshot()
	var warnings []string
	var dryRunSummary any

	if connected && brokers > 0 {
		var root yaml.Node
		if yaml.Unmarshal([]byte(text), &root) == nil && len(root.Content) > 0 {
			checkClusterBrokerBounds(root.Content[0], brokers, &warnings)
		}
		if dialect == dialectDisruption && apiClient != nil && offlineErrs == 0 {
			var plan map[string]any
			if yaml.Unmarshal([]byte(text), &plan) == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if res, err := apiClient.RunDryRun(ctx, plan); err == nil && res != nil {
					dryRunSummary = res
					warnings = append(warnings, res.Warnings...)
					warnings = append(warnings, res.Errors...)
				}
			}
		}
	}

	return map[string]any{
		"dialect":       string(dialect),
		"connected":     connected,
		"brokerCount":   brokers,
		"topicCount":    len(topics),
		"offlineErrors": offlineErrs,
		"warnings":      warnings,
		"dryRun":        dryRunSummary,
	}
}

func checkClusterBrokerBounds(n *yaml.Node, brokers int, warnings *[]string) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if (k.Value == "replicationFactor" || k.Value == "minInsyncReplicas") && v.Kind == yaml.ScalarNode {
				if iv, err := strconv.Atoi(v.Value); err == nil && iv > brokers {
					*warnings = append(*warnings, fmt.Sprintf("line %d: %s (%d) exceeds connected cluster brokerCount (%d)", v.Line, k.Value, iv, brokers))
				}
			} else if k.Value == "targetBrokerId" && v.Kind == yaml.ScalarNode {
				if iv, err := strconv.Atoi(v.Value); err == nil && iv >= brokers {
					*warnings = append(*warnings, fmt.Sprintf("line %d: targetBrokerId (%d) out of range for connected cluster brokerCount (%d)", v.Line, iv, brokers))
				}
			}
			checkClusterBrokerBounds(v, brokers, warnings)
		}
		return
	}
	for _, c := range n.Content {
		checkClusterBrokerBounds(c, brokers, warnings)
	}
}

// listTemplates returns curated starter templates across all 6 Kates dialects.
func (s *lspServer) listTemplates() []map[string]any {
	var list []map[string]any
	for _, meta := range builtinScenarios {
		if data, err := scenarioFS.ReadFile("scenarios/" + meta.filename); err == nil {
			list = append(list, map[string]any{
				"id":          "scenario:" + meta.name,
				"name":        meta.name,
				"dialect":     string(dialectScenario),
				"description": meta.description + " (" + meta.testType + ")",
				"content":     "# kates-schema: scenario\n" + string(data),
			})
		}
	}
	list = append(list, staticDialectTemplates...)
	return list
}

var staticDialectTemplates = []map[string]any{
	{
		"id":          "test-request:multi-phase",
		"name":        "multi-phase-ramp",
		"dialect":     string(dialectTestRequest),
		"description": "Multi-phase WARMUP → RAMP → STEADY → COOLDOWN TestScenario with SLA gates",
		"content": `# kates-schema: test-request
backend: native
scenario:
  name: "Multi-Phase Throughput Ramp"
  baseSpec:
    topic: kates-ramp-events
    partitions: 12
    replicationFactor: 3
    minInsyncReplicas: 2
    recordSize: 1024
    acks: "all"
    enableIdempotence: true
    compressionType: lz4
  phases:
    - name: warmup
      type: WARMUP
      durationMs: 15000
      specOverrides:
        throughput: 2000
    - name: step-ramp
      type: RAMP
      durationMs: 60000
      rampSteps: 4
      specOverrides:
        throughput: 20000
    - name: steady-peak
      type: STEADY
      durationMs: 60000
    - name: cooldown
      type: COOLDOWN
      durationMs: 15000
  sla:
    p95LatencyMs: 50
    p99LatencyMs: 150
    minThroughputRecPerSec: 15000
    maxErrorRatePercent: 0.1
`,
	},
	{
		"id":          "resilience:kill-leader",
		"name":        "resilience-kill-leader",
		"dialect":     string(dialectResilience),
		"description": "Resilience chaos test injecting LEADER_ELECTION during an INTEGRITY workload",
		"content": `# kates-schema: resilience
testRequest:
  type: INTEGRITY
  spec:
    topic: kates-resilience-tx
    partitions: 6
    replicationFactor: 3
    minInsyncReplicas: 2
    numRecords: 300000
    recordSize: 512
    acks: "all"
    enableIdempotence: true
    enableTransactions: true
    enableCrc: true
steadyStateSec: 15
maxRecoveryWaitSec: 120
chaosSpec:
  experimentName: leader-failover
  disruptionType: LEADER_ELECTION
  targetTopic: kates-resilience-tx
  delayBeforeSec: 5
  chaosDurationSec: 30
probes:
  - name: urp-zero
    type: kafkaProbe
    mode: Continuous
    command: under-replicated-partitions
    comparator: "=="
    expectedOutput: "0"
`,
	},
	{
		"id":          "disruption:playbook",
		"name":        "disruption-broker-failover",
		"dialect":     string(dialectDisruption),
		"description": "Multi-step DisruptionPlan with ISR tracking and automatic rollback",
		"content": `# kates-schema: disruption
name: "Broker Leader Failover & Network Latency Plan"
description: "Validates ISR recovery under leader failover followed by network latency"
baselineDurationSec: 15
maxAffectedBrokers: 1
autoRollback: true
isrTrackingTopic: kates-events
steps:
  - name: "Step 1 — Partition Leader Failover"
    steadyStateSec: 15
    observationWindowSec: 30
    requireRecovery: true
    faultSpec:
      experimentName: leader-failover
      disruptionType: LEADER_ELECTION
      targetTopic: kates-events
      chaosDurationSec: 20
  - name: "Step 2 — Broker Network Latency"
    steadyStateSec: 15
    observationWindowSec: 30
    requireRecovery: true
    faultSpec:
      experimentName: broker-net-latency
      disruptionType: NETWORK_LATENCY
      targetLabel: "strimzi.io/kind=Kafka"
      chaosDurationSec: 30
      networkLatencyMs: 50
sla:
  p99LatencyMs: 500
  maxRecoveryTimeMs: 30000
  maxIsrRecoveryTimeMs: 45000
`,
	},
	{
		"id":          "flow:pipeline",
		"name":        "flow-ci-pipeline",
		"dialect":     string(dialectFlow),
		"description": "Sequential CI flow pipeline with warmup, load test gate, and cooldown",
		"content": `# kates-schema: flow
name: "Production Readiness Flow Pipeline"
steps:
  - name: baseline-load
    action: test
    type: LOAD
    records: 100000
    spec:
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
      recordSize: 1024
      acks: "all"
    gate:
      minGrade: B
      maxP99: 100
      minThroughput: 5000
    onFail: stop
  - name: settle-wait
    action: wait
    records: 5
`,
	},
	{
		"id":          "compliance:kafkareference",
		"name":        "compliance-production-reference",
		"dialect":     string(dialectCompliance),
		"description": "KafkaReference (conformity.kates.io/v1alpha1) production baseline specification",
		"content": `# kates-schema: compliance
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: production-kafka-baseline
  description: "Production Strimzi Kafka 4.x KRaft compliance baseline"
spec:
  versions:
    kafka:
      min: "4.0.0"
  controllers:
    count:
      min: 3
  brokers:
    count:
      min: 3
    storage:
      type: persistent-claim
  kafka:
    config:
      min.insync.replicas:
        min: 2
      unclean.leader.election.enable: false
      auto.create.topics.enable: false
  topics:
    replicationFactor:
      min: 3
`,
	},
}

// scenarioDiff compares two Kates scenario documents and returns a Markdown
// side-by-side table of their effective workload specifications and SLA gates.
func (s *lspServer) scenarioDiff(leftURI, leftText, rightURI, rightText string) map[string]any {
	if leftText == "" {
		leftText = s.loadURIOrDoc(leftURI)
	}
	if rightText == "" {
		rightText = s.loadURIOrDoc(rightURI)
	}
	leftSF, err1 := parseScenarioFile(leftURI, []byte(leftText))
	rightSF, err2 := parseScenarioFile(rightURI, []byte(rightText))
	if err1 != nil || len(leftSF.Scenarios) == 0 || err2 != nil || len(rightSF.Scenarios) == 0 {
		return map[string]any{
			"error": "Both files must be valid Kates scenario files containing at least one scenario.",
		}
	}
	lSummary := summarizeEffectiveScenario(leftSF.Scenarios[0])
	rSummary := summarizeEffectiveScenario(rightSF.Scenarios[0])

	keys := []struct{ label, key string }{
		{"Scenario Name", "name"},
		{"Test Type", "type"},
		{"Backend", "backend"},
		{"Topic", "topic"},
		{"Partitions", "partitions"},
		{"Replication Factor", "replicationFactor"},
		{"Min In-Sync Replicas", "minInsyncReplicas"},
		{"Producers", "producers"},
		{"Records / Producer", "records"},
		{"Record Size (B)", "recordSize"},
		{"Estimated Footprint", "footprint"},
		{"Target Rate (rec/s)", "rate"},
		{"Planned Duration (s)", "durationSec"},
		{"Acks", "acks"},
		{"Compression", "compression"},
		{"Idempotence", "idempotence"},
		{"Transactions", "transactions"},
		{"SLA maxP99LatencyMs", "maxP99LatencyMs"},
		{"SLA maxAvgLatencyMs", "maxAvgLatencyMs"},
		{"SLA minThroughputRecPerSec", "minThroughputRecPerSec"},
		{"SLA maxDataLossPercent", "maxDataLossPercent"},
	}

	var md strings.Builder
	fmt.Fprintf(&md, "## Scenario Comparison: `%s` vs `%s`\n\n", lSummary["name"], rSummary["name"])
	md.WriteString("| Parameter | Left (`" + lSummary["name"] + "`) | Right (`" + rSummary["name"] + "`) | Delta |\n")
	md.WriteString("| :--- | :--- | :--- | :--- |\n")
	for _, row := range keys {
		lv, rv := lSummary[row.key], rSummary[row.key]
		delta := "—"
		if lv != rv {
			delta = "**changed**"
		}
		fmt.Fprintf(&md, "| %s | `%s` | `%s` | %s |\n", row.label, lv, rv, delta)
	}

	return map[string]any{
		"leftName":  lSummary["name"],
		"rightName": rSummary["name"],
		"markdown":  md.String(),
	}
}

func (s *lspServer) loadURIOrDoc(uri string) string {
	if doc := s.getDoc(uri); doc != "" {
		return doc
	}
	path := uri
	if strings.HasPrefix(uri, "file://") {
		if u, err := url.Parse(uri); err == nil {
			path = u.Path
		}
	}
	if b, err := os.ReadFile(path); err == nil {
		return string(b)
	}
	return ""
}

func summarizeEffectiveScenario(sc TestScenario) map[string]string {
	req := scenarioToRequest(sc)
	tt := cmpOr(strings.ToUpper(req.TestType), "LOAD")
	backend := cmpOr(req.Backend, "native")
	topic, acks, comp, partitions, rf, isr, producers, records, recSize, tp, durMs, idem, tx := resolveEffectiveSpecValues(tt, req.Spec)

	rateStr := "unthrottled"
	if tp > 0 {
		rateStr = fmt.Sprintf("%d (%d×%d)", producers*tp, producers, tp)
	}
	footprint := formatBytesHuman(float64(producers) * float64(records) * float64(recSize))
	var maxP99, maxAvg, minTp, maxLoss float64
	if sc.Validate != nil {
		maxP99, maxAvg, minTp, maxLoss = sc.Validate.MaxP99Latency, sc.Validate.MaxAvgLatency, sc.Validate.MinThroughput, sc.Validate.MaxDataLoss
	}
	return map[string]string{
		"name":                   cmpOr(sc.Name, tt),
		"type":                   tt,
		"backend":                backend,
		"topic":                  topic,
		"partitions":             strconv.Itoa(partitions),
		"replicationFactor":      strconv.Itoa(rf),
		"minInsyncReplicas":      strconv.Itoa(isr),
		"producers":              strconv.Itoa(producers),
		"records":                strconv.FormatInt(records, 10),
		"recordSize":             strconv.Itoa(recSize),
		"footprint":              footprint,
		"rate":                   rateStr,
		"durationSec":            strconv.FormatInt(durMs/1000, 10),
		"acks":                   acks,
		"compression":            comp,
		"idempotence":            strconv.FormatBool(idem),
		"transactions":           strconv.FormatBool(tx),
		"maxP99LatencyMs":        fmt.Sprintf("%v", maxP99),
		"maxAvgLatencyMs":        fmt.Sprintf("%v", maxAvg),
		"minThroughputRecPerSec": fmt.Sprintf("%v", minTp),
		"maxDataLossPercent":     fmt.Sprintf("%v", maxLoss),
	}
}

func resolveEffectiveSpecValues(tt string, sp *client.TestSpec) (topic, acks, comp string, partitions, rf, isr, producers int, records int64, recSize, tp int, durMs int64, idem, tx bool) {
	d := mcpScnTypeDefaultsFor(tt)
	topic, acks, comp = strings.ToLower(tt)+"-test", d.acks, "none"
	partitions, rf, isr, producers, records, recSize, tp, durMs = d.partitions, 3, 2, d.numProducers, d.numRecords, d.recordSize, d.throughput, d.durationMs
	if sp == nil {
		return
	}
	topic, acks, comp = cmpOr(sp.Topic, topic), cmpOr(sp.Acks, acks), cmpOr(sp.CompressionType, comp)
	if sp.Partitions > 0 {
		partitions = sp.Partitions
	}
	if sp.ReplicationFactor > 0 {
		rf = sp.ReplicationFactor
	}
	if sp.MinInsyncReplicas > 0 {
		isr = sp.MinInsyncReplicas
	}
	if sp.ParallelProducers > 0 {
		producers = sp.ParallelProducers
	}
	if sp.Records > 0 {
		records = int64(sp.Records)
	}
	if sp.RecordSizeBytes > 0 {
		recSize = sp.RecordSizeBytes
	}
	if sp.TargetThroughput != 0 {
		tp = sp.TargetThroughput
	}
	if sp.DurationMs > 0 {
		durMs = int64(sp.DurationMs)
	}
	if sp.EnableIdempotence != nil {
		idem = *sp.EnableIdempotence
	}
	if sp.EnableTransactions != nil {
		tx = *sp.EnableTransactions
	}
	return
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// YAML AST helper utilities.

func mappingToMap(n *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}

func findMappingEntry(n *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

func nodeString(n *yaml.Node, fallback string) string {
	if n != nil && n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) != "" {
		return n.Value
	}
	return fallback
}

func nodeInt(n *yaml.Node, fallback int) int {
	if n != nil && n.Kind == yaml.ScalarNode {
		if iv, err := strconv.Atoi(strings.TrimSpace(n.Value)); err == nil {
			return iv
		}
	}
	return fallback
}

func nodeInt64(n *yaml.Node, fallback int64) int64 {
	if n != nil && n.Kind == yaml.ScalarNode {
		if iv, err := strconv.ParseInt(strings.TrimSpace(n.Value), 10, 64); err == nil {
			return iv
		}
	}
	return fallback
}
