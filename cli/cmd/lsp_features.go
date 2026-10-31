package cmd

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/internal/compliance"
	"gopkg.in/yaml.v3"
)

var (
	lspSemanticTokenTypes = []string{
		"keyword",    // 0: apiVersion, kind, kates.io/v1alpha1, KafkaReference
		"type",       // 1: backend types, probe types
		"enumMember", // 2: TestType, PhaseType
		"property",   // 3: rule references in waivers
		"operator",   // 4: equals, min, max, oneOf, severity, comparators
		"number",     // 5: K8s quantities (8Gi, 500m)
		"string",     // 6: dates, selectors
		"variable",   // 7: $VAR, ${VAR}
		"macro",      // 8: DisruptionType (POD_KILL, NETWORK_PARTITION, etc.)
	}
	lspSemanticTokenModifiers = []string{
		"declaration",  // 1 << 0
		"deprecated",   // 1 << 1 (waived compliance rules)
		"modification", // 1 << 2
		"readonly",     // 1 << 3
	}

	disruptionTypeDocs = map[string]string{
		"POD_KILL":          "**POD_KILL** — Immediately terminates a target broker/controller pod (`SIGKILL`, grace period 0s). Simulates sudden hardware or kernel panic.",
		"POD_DELETE":        "**POD_DELETE** — Deletes target pod(s) via the Kubernetes API (`chaosDurationSec` controls repeat loop on Litmus).",
		"GRACEFUL_STOP":     "**GRACEFUL_STOP** — Terminates pod with `SIGTERM` and configurable `gracePeriodSec` (`0..300`), testing clean controlled shutdown leadership handoff.",
		"NETWORK_PARTITION": "**NETWORK_PARTITION** — Injects a Kubernetes `NetworkPolicy` isolating the target pod(s) from broker/client traffic (`chaosDurationSec > 0` required).",
		"NETWORK_LATENCY":   "**NETWORK_LATENCY** — Injects `tc netem` delay (`networkLatencyMs: 1..30000`) on pod network interfaces (`chaosDurationSec > 0` required).",
		"DISK_FILL":         "**DISK_FILL** — Fills the Kafka data volume up to `fillPercentage` (`1..100%`) (`chaosDurationSec > 0` required).",
		"CPU_STRESS":        "**CPU_STRESS** — Saturates `cpuCores` (`1..64`) on the target pod (`chaosDurationSec > 0` required).",
		"MEMORY_STRESS":     "**MEMORY_STRESS** — Allocates and touches `memoryMb` (`1..32768` MB) on the target pod (`chaosDurationSec > 0` required).",
		"IO_STRESS":         "**IO_STRESS** — Spawns `ioWorkers` (`1..64`) performing heavy disk I/O (`chaosDurationSec > 0` required).",
		"DNS_ERROR":         "**DNS_ERROR** — Corrupts CoreDNS / pod DNS resolution for Kafka service names (`chaosDurationSec > 0` required).",
		"ROLLING_RESTART":   "**ROLLING_RESTART** — Annotates Strimzi pods with `strimzi.io/manual-rolling-update=true` and monitors operator rolling restart.",
		"SCALE_DOWN":        "**SCALE_DOWN** — Scales down the Strimzi `KafkaNodePool` replica count and optionally rolls back (`autoRollback: true`).",
		"LEADER_ELECTION":   "**LEADER_ELECTION** — Resolves the partition leader for `targetTopic`/`targetPartition` and forces a leader re-election.",
		"NODE_DRAIN":        "**NODE_DRAIN** — Cordons and drains the Kubernetes worker node (`envOverrides.TARGET_NODE` or `NODE_LABEL`, `chaosDurationSec > 0` required).",
	}

	commonKafkaBrokerConfigs = []struct {
		key, desc string
	}{
		{"min.insync.replicas", "Minimum number of in-sync replicas required for acks=all writes (recommended: 2 for RF=3)."},
		{"default.replication.factor", "Default replication factor for automatically created topics (recommended: 3)."},
		{"unclean.leader.election.enable", "Whether replicas not in the ISR set may be elected leader (recommended: false)."},
		{"auto.create.topics.enable", "Whether brokers auto-create topics on produce/metadata requests (recommended: false in production)."},
		{"offsets.topic.replication.factor", "Replication factor for the internal __consumer_offsets topic (recommended: 3)."},
		{"transaction.state.log.replication.factor", "Replication factor for the __transaction_state topic (recommended: 3)."},
		{"transaction.state.log.min.isr", "Minimum ISR for the __transaction_state topic (recommended: 2)."},
		{"num.network.threads", "Number of threads handling network requests."},
		{"num.io.threads", "Number of threads executing disk I/O for requests."},
		{"log.retention.hours", "Hours to retain a log segment before deleting it."},
		{"log.retention.bytes", "Maximum size of the log before deleting segments."},
		{"log.segment.bytes", "Maximum size of a single log segment file."},
		{"message.max.bytes", "Largest record batch size allowed by the broker."},
		{"replica.lag.time.max.ms", "Max time a follower can lag behind the leader before being removed from ISR."},
		{"compression.type", "Final compression type for a given topic (producer, uncompressed, gzip, snappy, lz4, zstd)."},
	}

	commonStrimziSelectors = []struct {
		label, desc string
	}{
		{"strimzi.io/cluster=krafter,strimzi.io/broker-role=true", "Target only Kafka broker pods in Strimzi cluster 'krafter'"},
		{"strimzi.io/cluster=krafter,strimzi.io/controller-role=true", "Target only KRaft controller pods in Strimzi cluster 'krafter'"},
		{"strimzi.io/kind=Kafka,strimzi.io/broker-role=true", "Target all Strimzi Kafka broker pods in the namespace"},
		{"strimzi.io/component-type=kafka", "Target all Strimzi Kafka pods in the namespace"},
	}
)

// yamlCursorContext describes where the cursor sits in a YAML document.
type yamlCursorContext struct {
	path        []string // e.g., ["scenarios", "spec"] or ["chaosSpec", "probes"]
	key         string   // Current line's key if on a value, or prefix if typing a key
	inValue     bool     // True when cursor is after "key:"
	isArrayItem bool     // True when current line starts with "- "
	indent      int      // Leading spaces on the cursor's line (or after "- ")
}

var yamlLineKeyRE = regexp.MustCompile(`^(\s*)(?:-\s*)?([A-Za-z0-9_.-]+)?(\s*:\s*(.*))?$`)

func resolveCursorContext(lines []string, pos lspPosition) yamlCursorContext {
	if pos.Line < 0 || pos.Line >= len(lines) {
		return yamlCursorContext{}
	}
	curLine := lines[pos.Line]
	col := min(max(pos.Character, 0), len(curLine))
	prefix := curLine[:col]

	trimmedLeft := strings.TrimLeft(curLine, " ")
	indent := len(curLine) - len(trimmedLeft)
	isArrayItem := strings.HasPrefix(trimmedLeft, "- ") || trimmedLeft == "-"
	if isArrayItem {
		afterDash := strings.TrimPrefix(trimmedLeft, "-")
		trimmedAfter := strings.TrimLeft(afterDash, " ")
		if trimmedAfter != "" {
			indent = len(curLine) - len(trimmedAfter)
		} else {
			indent += 2
		}
	}

	inValue := false
	keyOnLine := ""
	if colonIdx := strings.IndexByte(prefix, ':'); colonIdx >= 0 {
		inValue = true
		kPart := strings.TrimSpace(curLine[:colonIdx])
		kPart = strings.TrimSpace(strings.TrimPrefix(kPart, "-"))
		keyOnLine = kPart
	} else if m := yamlLineKeyRE.FindStringSubmatch(curLine); m != nil {
		keyOnLine = m[2]
	}

	// Walk upwards to collect ancestor keys with strictly smaller indentation.
	var ancestors []string
	targetIndent := indent
	for l := pos.Line - 1; l >= 0; l-- {
		line := lines[l]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		leftTrim := strings.TrimLeft(line, " ")
		lineIndent := len(line) - len(leftTrim)
		isDash := strings.HasPrefix(leftTrim, "- ")
		keyIndent := lineIndent
		content := leftTrim
		if isDash {
			after := strings.TrimPrefix(leftTrim, "-")
			content = strings.TrimLeft(after, " ")
			keyIndent = len(line) - len(content)
		}
		if colon := strings.IndexByte(content, ':'); colon > 0 {
			k := strings.TrimSpace(content[:colon])
			if isDash && lineIndent < targetIndent && keyIndent == targetIndent {
				// Sibling key inside the same sequence item mapping; keep climbing
				continue
			}
			if keyIndent < targetIndent {
				ancestors = append(ancestors, k)
				targetIndent = keyIndent
				if isDash && lineIndent < targetIndent {
					targetIndent = lineIndent
				}
			}
		}
	}
	slices.Reverse(ancestors)
	return yamlCursorContext{
		path:        ancestors,
		key:         keyOnLine,
		inValue:     inValue,
		isArrayItem: isArrayItem,
		indent:      indent,
	}
}

func (s *lspServer) complete(uri, text string, pos lspPosition) []lspCompletionItem {
	dialect := detectDialect(uri, []byte(text))
	if dialect == dialectUnknown {
		dialect = dialectScenario
	}
	lines := strings.Split(text, "\n")
	ctx := resolveCursorContext(lines, pos)
	rootSchema := s.schemas[dialect]
	if rootSchema == nil {
		return nil
	}

	if ctx.inValue {
		return s.completeValue(dialect, rootSchema, text, ctx)
	}
	return s.completeKeys(dialect, rootSchema, lines, pos, ctx)
}

func (s *lspServer) completeKeys(dialect yamlDialect, rootSchema map[string]any, lines []string, pos lspPosition, ctx yamlCursorContext) []lspCompletionItem {
	dottedPath := strings.Join(ctx.path, ".")

	// Special case: Compliance spec.kafka.config popular Kafka broker keys
	if dialect == dialectCompliance && dottedPath == "spec.kafka.config" {
		items := make([]lspCompletionItem, 0, len(commonKafkaBrokerConfigs))
		for i, cfg := range commonKafkaBrokerConfigs {
			items = append(items, lspCompletionItem{
				Label:            cfg.key,
				Kind:             10, // Property
				Detail:           "Kafka broker configuration rule",
				Documentation:    &lspMarkup{Kind: "markdown", Value: cfg.desc},
				InsertText:       cfg.key + ": ${1}",
				InsertTextFormat: 2,
				SortText:         fmt.Sprintf("0_%02d_%s", i, cfg.key),
			})
		}
		return items
	}

	targetSchemas := resolveSchemaAtPath(rootSchema, ctx.path)
	props, required := collectSchemaProperties(rootSchema, targetSchemas)

	lineHasColon := pos.Line >= 0 && pos.Line < len(lines) && strings.Contains(lines[pos.Line], ":")
	var items []lspCompletionItem
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		propSchema := derefSchema(rootSchema, props[k])
		propType := schemaTypeString(propSchema)
		reqTag := ""
		sortPrefix := "1_"
		if required[k] {
			reqTag = " (required)"
			sortPrefix = "0_"
		}
		defTag := ""
		if defVal, ok := propSchema["default"]; ok {
			defTag = fmt.Sprintf(" [default: %v]", defVal)
		}
		doc := formatSchemaDoc(k, propSchema)

		insertText := k
		insertFormat := 1
		if !lineHasColon {
			insertFormat = 2
			switch propType {
			case "object":
				insertText = k + ":\n  $0"
			case "array":
				insertText = k + ":\n  - $0"
			default:
				insertText = k + ": $0"
			}
		}
		items = append(items, lspCompletionItem{
			Label:            k,
			Kind:             10, // Property
			Detail:           propType + reqTag + defTag,
			Documentation:    &lspMarkup{Kind: "markdown", Value: doc},
			InsertText:       insertText,
			InsertTextFormat: insertFormat,
			SortText:         sortPrefix + k,
		})
	}
	return items
}

func (s *lspServer) completeValue(dialect yamlDialect, rootSchema map[string]any, text string, ctx yamlCursorContext) []lspCompletionItem {
	fullPath := append(slices.Clone(ctx.path), ctx.key)
	dotted := strings.Join(fullPath, ".")

	if domainItems := completeDomainValue(dialect, text, ctx, dotted); len(domainItems) > 0 {
		return domainItems
	}
	if liveItems := s.completeLiveClusterValues(ctx.key); len(liveItems) > 0 {
		return liveItems
	}

	targetSchemas := resolveSchemaAtPath(rootSchema, fullPath)
	enums := collectSchemaEnums(rootSchema, targetSchemas)
	var items []lspCompletionItem
	for i, val := range enums {
		items = append(items, lspCompletionItem{
			Label:    val,
			Kind:     20,
			Detail:   fmt.Sprintf("Allowed value for %s", ctx.key),
			SortText: fmt.Sprintf("0_%02d_%s", i, val),
		})
	}
	return items
}

func completeDomainValue(dialect yamlDialect, text string, ctx yamlCursorContext, dotted string) []lspCompletionItem {
	// 1. Dynamic compliance waiver rule completions from current document
	if dialect == dialectCompliance && (dotted == "spec.waivers.rule" || ctx.key == "rule") {
		var root yaml.Node
		if yaml.Unmarshal([]byte(text), &root) == nil && len(root.Content) > 0 {
			rules := compliance.DeclaredRules(root.Content[0])
			items := make([]lspCompletionItem, 0, len(rules))
			for i, r := range rules {
				items = append(items, lspCompletionItem{
					Label:         r.ID,
					Kind:          20,
					Detail:        fmt.Sprintf("Declared %s rule (line %d)", r.Kind, r.Line),
					Documentation: &lspMarkup{Kind: "markdown", Value: fmt.Sprintf("Waives rule `%s` declared at line %d.", r.ID, r.Line)},
					SortText:      fmt.Sprintf("0_%02d_%s", i, r.ID),
				})
			}
			if len(items) > 0 {
				return items
			}
		}
	}

	// 2. TestType completions with TestTypeDefaults summary
	if ctx.key == "type" && !strings.HasSuffix(dotted, "phases.type") && !strings.HasSuffix(dotted, "probes.type") && !strings.HasSuffix(dotted, "storage.type") && !strings.HasSuffix(dotted, "require.type") && !strings.HasSuffix(dotted, "every.type") || ctx.key == "testType" {
		items := make([]lspCompletionItem, 0, len(lintTestTypes()))
		for i, tt := range lintTestTypes() {
			items = append(items, lspCompletionItem{
				Label:         tt,
				Kind:          20,
				Detail:        describeType(tt),
				Documentation: &lspMarkup{Kind: "markdown", Value: formatTestTypeDoc(tt)},
				SortText:      fmt.Sprintf("0_%02d_%s", i, tt),
			})
		}
		return items
	}

	// 3. DisruptionType completions with FaultLimits summary
	if ctx.key == "disruptionType" {
		items := make([]lspCompletionItem, 0, len(validDisruptionTypes))
		for i, dt := range validDisruptionTypes {
			items = append(items, lspCompletionItem{
				Label:         dt,
				Kind:          20,
				Detail:        "Chaos fault disruption type",
				Documentation: &lspMarkup{Kind: "markdown", Value: disruptionTypeDocs[dt]},
				SortText:      fmt.Sprintf("0_%02d_%s", i, dt),
			})
		}
		return items
	}

	// 4. Strimzi / K8s label selector completions for targetLabel / NODE_LABEL
	if ctx.key == "targetLabel" || ctx.key == "NODE_LABEL" {
		items := make([]lspCompletionItem, 0, len(commonStrimziSelectors))
		for i, sel := range commonStrimziSelectors {
			items = append(items, lspCompletionItem{
				Label:         fmt.Sprintf("%q", sel.label),
				Kind:          12,
				Detail:        sel.desc,
				Documentation: &lspMarkup{Kind: "markdown", Value: sel.desc},
				SortText:      fmt.Sprintf("0_%02d", i),
			})
		}
		return items
	}

	// 5. kafkaProbe command completions
	if ctx.key == "command" && (strings.Contains(dotted, "probes") || dialect == dialectResilience || dialect == dialectDisruption) {
		cmds := []struct{ cmd, desc string }{
			{"under-replicated-partitions", "Count of under-replicated partitions across the cluster (expect 0 when healthy)."},
			{"unavailable-partitions", "Count of offline/leaderless partitions across the cluster (expect 0 when healthy)."},
			{"produce kates-probe", "Synthetic end-to-end produce probe to topic 'kates-probe'."},
			{"consumer-lag", "Total consumer group lag across all groups (or 'consumer-lag <group>')."},
		}
		items := make([]lspCompletionItem, 0, len(cmds))
		for i, c := range cmds {
			items = append(items, lspCompletionItem{
				Label:         c.cmd,
				Kind:          12,
				Detail:        "kafkaProbe built-in check",
				Documentation: &lspMarkup{Kind: "markdown", Value: c.desc},
				SortText:      fmt.Sprintf("0_%02d", i),
			})
		}
		return items
	}
	return nil
}

func (s *lspServer) hover(uri, text string, pos lspPosition) *lspHover {
	dialect := detectDialect(uri, []byte(text))
	if dialect == dialectUnknown {
		dialect = dialectScenario
	}
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	token, rng := wordAtPosition(lines[pos.Line], pos.Line, pos.Character)
	if token == "" {
		return nil
	}
	unquoted := strings.Trim(token, `"'`)

	// Check if hovering over a known TestType
	if slices.Contains(lintTestTypes(), unquoted) {
		return &lspHover{
			Contents: lspMarkup{Kind: "markdown", Value: formatTestTypeDoc(unquoted)},
			Range:    &rng,
		}
	}
	// Check if hovering over a known DisruptionType
	if doc, ok := disruptionTypeDocs[unquoted]; ok {
		return &lspHover{
			Contents: lspMarkup{Kind: "markdown", Value: doc},
			Range:    &rng,
		}
	}

	ctx := resolveCursorContext(lines, pos)
	rootSchema := s.schemas[dialect]
	if rootSchema == nil {
		return nil
	}
	lookupPath := append(slices.Clone(ctx.path), unquoted)
	if ctx.inValue && ctx.key != "" {
		lookupPath = append(slices.Clone(ctx.path), ctx.key)
	}
	schemasAt := resolveSchemaAtPath(rootSchema, lookupPath)
	for _, sc := range schemasAt {
		sc = derefSchema(rootSchema, sc)
		if desc, ok := sc["description"].(string); ok && desc != "" {
			keyName := lookupPath[len(lookupPath)-1]
			return &lspHover{
				Contents: lspMarkup{Kind: "markdown", Value: formatSchemaDoc(keyName, sc)},
				Range:    &rng,
			}
		}
	}
	return nil
}

func (s *lspServer) codeActions(uri, text string, rng lspRange, diags []lspDiagnostic) []lspCodeAction {
	if len(diags) == 0 {
		diags = recomputeRangeDiagnostics(uri, text, rng)
	}

	var actions []lspCodeAction
	addedFormatAction := false
	for _, d := range diags {
		repl, _ := d.Data["replacement"].(string)
		if repl == "" {
			repl = extractSuggestedReplacement(d.Message)
		}
		if repl != "" {
			actions = append(actions, lspCodeAction{
				Title:       fmt.Sprintf("Replace with %q", repl),
				Kind:        "quickfix",
				Diagnostics: []lspDiagnostic{d},
				IsPreferred: true,
				Edit:        &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: {{Range: d.Range, NewText: repl}}}},
			})
		}

		quoteVal, _ := d.Data["quote"].(string)
		if quoteVal == "" {
			if m := quoteItRE.FindStringSubmatch(d.Message); m != nil {
				quoteVal = m[1]
			}
		}
		if quoteVal != "" {
			actions = append(actions, lspCodeAction{
				Title:       fmt.Sprintf("Quote value as %q", quoteVal),
				Kind:        "quickfix",
				Diagnostics: []lspDiagnostic{d},
				IsPreferred: true,
				Edit:        &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: {{Range: d.Range, NewText: fmt.Sprintf("%q", quoteVal)}}}},
			})
		}

		if d.Code == "fault-requires-duration" {
			actions = append(actions, lspCodeAction{
				Title:       "Set chaosDurationSec to 30",
				Kind:        "quickfix",
				Diagnostics: []lspDiagnostic{d},
				IsPreferred: true,
				Edit:        &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: {{Range: d.Range, NewText: "30"}}}},
			})
		}

		fixable, _ := d.Data["fixable"].(bool)
		if (fixable || d.Code == "redundant-bound" || d.Code == "singular-one-of") && !addedFormatAction {
			if edits := s.formatDocument(uri, text); len(edits) > 0 {
				addedFormatAction = true
				actions = append(actions, lspCodeAction{
					Title:       "Format reference canonically (kates compliance fmt)",
					Kind:        "quickfix",
					Diagnostics: []lspDiagnostic{d},
					IsPreferred: true,
					Edit:        &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{uri: edits}},
				})
			}
		}
	}
	actions = append(actions, s.refactorCodeActions(uri, text, rng)...)
	return actions
}

func recomputeRangeDiagnostics(uri, text string, rng lspRange) []lspDiagnostic {
	_, rawDiags := lintDocument(uri, []byte(text))
	lines := strings.Split(text, "\n")
	var diags []lspDiagnostic
	for _, d := range rawDiags {
		lineIdx := max(d.Line-1, 0)
		if lineIdx < rng.Start.Line || lineIdx > rng.End.Line {
			continue
		}
		colIdx := max(d.Col-1, 0)
		endCol := colIdx + 1
		if lineIdx < len(lines) && colIdx < len(lines[lineIdx]) {
			endCol = scanTokenEnd(lines[lineIdx], colIdx)
		}
		data := map[string]any{"code": d.Code, "path": d.Path, "fixable": d.Fixable}
		if repl := extractSuggestedReplacement(d.Message); repl != "" {
			data["replacement"] = repl
		} else if m := quoteItRE.FindStringSubmatch(d.Message); m != nil {
			data["quote"] = m[1]
		}
		diags = append(diags, lspDiagnostic{
			Range:   lspRange{Start: lspPosition{Line: lineIdx, Character: colIdx}, End: lspPosition{Line: lineIdx, Character: endCol}},
			Code:    d.Code,
			Message: d.Message,
			Data:    data,
		})
	}
	return diags
}

func (s *lspServer) definition(uri, text string, pos lspPosition) *lspLocation {
	if detectDialect(uri, []byte(text)) != dialectCompliance {
		if refs := s.references(uri, text, pos); len(refs) > 0 {
			return &refs[0]
		}
		return nil
	}
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	ctx := resolveCursorContext(lines, pos)
	if !ctx.inValue || ctx.key != "rule" {
		return nil
	}
	line := lines[pos.Line]
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return nil
	}
	ruleID := strings.TrimSpace(line[colon+1:])
	ruleID = strings.Trim(ruleID, `"'`)
	if ruleID == "" {
		return nil
	}

	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return nil
	}
	for _, r := range compliance.DeclaredRules(root.Content[0]) {
		if r.ID == ruleID {
			targetLine := max(r.Line-1, 0)
			targetCol := max(r.Col-1, 0)
			endCol := targetCol + 1
			if targetLine < len(lines) && targetCol < len(lines[targetLine]) {
				endCol = scanTokenEnd(lines[targetLine], targetCol)
			}
			return &lspLocation{
				URI: uri,
				Range: lspRange{
					Start: lspPosition{Line: targetLine, Character: targetCol},
					End:   lspPosition{Line: targetLine, Character: endCol},
				},
			}
		}
	}
	return nil
}

func (s *lspServer) formatDocument(uri, text string) []lspTextEdit {
	if detectDialect(uri, []byte(text)) != dialectCompliance {
		return nil
	}
	formatted, _ := compliance.Format([]byte(text))
	if formatted == nil || string(formatted) == text {
		return nil
	}
	lines := strings.Split(text, "\n")
	lastLine := max(len(lines)-1, 0)
	lastCol := 0
	if len(lines) > 0 {
		lastCol = len(lines[lastLine])
	}
	return []lspTextEdit{{
		Range: lspRange{
			Start: lspPosition{Line: 0, Character: 0},
			End:   lspPosition{Line: lastLine, Character: lastCol},
		},
		NewText: string(formatted),
	}}
}

func (s *lspServer) codeLens(uri, text string) []lspCodeLens {
	dialect := detectDialect(uri, []byte(text))
	topRange := lspRange{
		Start: lspPosition{Line: 0, Character: 0},
		End:   lspPosition{Line: 0, Character: 0},
	}
	var lenses []lspCodeLens
	switch dialect {
	case dialectScenario:
		lenses = append(lenses,
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "▶ Run Scenario (kates test apply)", Command: "kates.runScenario", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "✓ Lint Scenario", Command: "kates.lintFile", Arguments: []any{uri, "scenario"}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "🔍 Cluster Preflight", Command: "kates.clusterPreflight", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "⇄ Diff Scenario", Command: "kates.diffScenarios", Arguments: []any{uri}},
			},
		)
	case dialectFlow:
		lenses = append(lenses,
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "▶ Run Flow (kates flow run)", Command: "kates.runFlow", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "✓ Lint Flow", Command: "kates.lintFile", Arguments: []any{uri, "flow"}},
			},
		)
	case dialectResilience:
		lenses = append(lenses,
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "⚡ Run Resilience Test", Command: "kates.runResilience", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "✓ Lint Resilience Spec", Command: "kates.lintFile", Arguments: []any{uri, "resilience"}},
			},
		)
	case dialectDisruption:
		lenses = append(lenses,
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "🔍 Dry-Run Disruption Plan", Command: "kates.dryRunDisruption", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "⚡ Run Disruption Plan", Command: "kates.runDisruption", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "✓ Lint Plan", Command: "kates.lintFile", Arguments: []any{uri, "disruption"}},
			},
		)
	case dialectCompliance:
		lenses = append(lenses,
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "🛡 Check Cluster Compliance", Command: "kates.checkCompliance", Arguments: []any{uri}},
			},
			lspCodeLens{
				Range:   topRange,
				Command: &lspCommand{Title: "✨ Format Reference", Command: "kates.formatCompliance", Arguments: []any{uri}},
			},
		)
	}
	return lenses
}

type rawSemanticToken struct {
	line      int
	startChar int
	length    int
	tokType   int
	modifiers int
}

var (
	semQuantityRE = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)$`)
	semEnvVarRE   = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
)

func (s *lspServer) semanticTokens(uri, text string) []int {
	dialect := detectDialect(uri, []byte(text))
	var tokens []rawSemanticToken
	waivedLines := waivedRuleLines(dialect, text)

	lines := strings.Split(text, "\n")
	for lineIdx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, loc := range semEnvVarRE.FindAllStringIndex(line, -1) {
			tokens = append(tokens, rawSemanticToken{
				line: lineIdx, startChar: loc[0], length: loc[1] - loc[0], tokType: 7,
			})
		}

		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		keyPart := line[:colon]
		keyTrim := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(keyPart), "-"))
		keyCol := strings.Index(line, keyTrim)
		if keyCol >= 0 && keyTrim != "" {
			switch keyTrim {
			case "apiVersion", "kind", "scenarios", "testRequest", "chaosSpec", "steps", "validate", "sla", "waivers":
				tokens = append(tokens, rawSemanticToken{line: lineIdx, startChar: keyCol, length: len(keyTrim), tokType: 0})
			case "equals", "min", "max", "oneOf", "severity", "comparator":
				tokens = append(tokens, rawSemanticToken{line: lineIdx, startChar: keyCol, length: len(keyTrim), tokType: 4})
			}
		}

		valPart := line[colon+1:]
		if hashIdx := strings.Index(valPart, " #"); hashIdx >= 0 {
			valPart = valPart[:hashIdx]
		}
		valTrim := strings.TrimSpace(valPart)
		if valTrim == "" {
			continue
		}
		valCol := colon + 1 + strings.Index(line[colon+1:], valTrim)
		mod := 0
		if waivedLines[lineIdx] {
			mod = 1 << 1
		}
		if tok, ok := classifySemanticValueToken(strings.Trim(valTrim, `"'`), valTrim, keyTrim, lineIdx, valCol, mod, dialect); ok {
			tokens = append(tokens, tok)
		}
	}

	return encodeSemanticTokens(tokens)
}

func waivedRuleLines(dialect yamlDialect, text string) map[int]bool {
	waivedLines := map[int]bool{}
	if dialect != dialectCompliance {
		return waivedLines
	}
	var root yaml.Node
	if yaml.Unmarshal([]byte(text), &root) != nil || len(root.Content) == 0 {
		return waivedLines
	}
	doc := root.Content[0]
	declared := compliance.DeclaredRules(doc)
	waivedIDs := map[string]bool{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "spec" && doc.Content[i+1].Kind == yaml.MappingNode {
			spec := doc.Content[i+1]
			for j := 0; j+1 < len(spec.Content); j += 2 {
				if spec.Content[j].Value == "waivers" && spec.Content[j+1].Kind == yaml.SequenceNode {
					for _, w := range spec.Content[j+1].Content {
						for k := 0; k+1 < len(w.Content); k += 2 {
							if w.Content[k].Value == "rule" {
								waivedIDs[w.Content[k+1].Value] = true
							}
						}
					}
				}
			}
		}
	}
	for _, r := range declared {
		if waivedIDs[r.ID] {
			waivedLines[r.Line-1] = true
		}
	}
	return waivedLines
}

func classifySemanticValueToken(unquoted, valTrim, keyTrim string, lineIdx, valCol, mod int, dialect yamlDialect) (rawSemanticToken, bool) {
	switch {
	case unquoted == "conformity.kates.io/v1alpha1" || unquoted == "kates.io/v1alpha1" || unquoted == "KafkaReference":
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 0, modifiers: mod}, true
	case slices.Contains(lintTestTypes(), unquoted) || slices.Contains(validPhaseTypes, unquoted):
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 2, modifiers: mod}, true
	case slices.Contains(validDisruptionTypes, unquoted):
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 8, modifiers: mod}, true
	case slices.Contains(validProbeTypes, unquoted) || slices.Contains(validProbeModes, unquoted) || unquoted == "native" || unquoted == "trogdor":
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 1, modifiers: mod}, true
	case keyTrim == "rule" && dialect == dialectCompliance:
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 3, modifiers: 1 << 2}, true
	case semQuantityRE.MatchString(unquoted):
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 5, modifiers: mod}, true
	case mod != 0:
		return rawSemanticToken{line: lineIdx, startChar: valCol, length: len(valTrim), tokType: 3, modifiers: mod}, true
	default:
		return rawSemanticToken{}, false
	}
}

func encodeSemanticTokens(tokens []rawSemanticToken) []int {
	slices.SortStableFunc(tokens, func(a, b rawSemanticToken) int {
		if a.line != b.line {
			return a.line - b.line
		}
		return a.startChar - b.startChar
	})
	data := make([]int, 0, len(tokens)*5)
	prevLine, prevChar := 0, 0
	for _, t := range tokens {
		dLine := t.line - prevLine
		dChar := t.startChar
		if dLine == 0 {
			dChar = t.startChar - prevChar
		}
		data = append(data, dLine, dChar, t.length, t.tokType, t.modifiers)
		prevLine = t.line
		prevChar = t.startChar
	}
	return data
}

// Schema navigation helpers for completion & hover.

func derefSchema(root, s map[string]any) map[string]any {
	for s != nil {
		ref, ok := s["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/$defs/") {
			break
		}
		defs, _ := root["$defs"].(map[string]any)
		next, _ := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		if next == nil {
			break
		}
		// Preserve description if the referring property set one
		if desc, hasDesc := s["description"]; hasDesc && next["description"] == nil {
			merged := make(map[string]any, len(next)+1)
			for k, v := range next {
				merged[k] = v
			}
			merged["description"] = desc
			return merged
		}
		s = next
	}
	return s
}

func expandSchemaBranches(root, s map[string]any) []map[string]any {
	s = derefSchema(root, s)
	if s == nil {
		return nil
	}
	out := []map[string]any{s}
	for _, comb := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := s[comb].([]any); ok {
			for _, item := range list {
				if sub, ok := item.(map[string]any); ok {
					out = append(out, expandSchemaBranches(root, sub)...)
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		out = append(out, expandSchemaBranches(root, items)...)
	}
	return out
}

func resolveSchemaAtPath(root map[string]any, path []string) []map[string]any {
	current := expandSchemaBranches(root, root)
	for _, seg := range path {
		var next []map[string]any
		for _, s := range current {
			if props, ok := s["properties"].(map[string]any); ok {
				if sub, ok := props[seg].(map[string]any); ok {
					next = append(next, expandSchemaBranches(root, sub)...)
				}
			}
			if addl, ok := s["additionalProperties"].(map[string]any); ok {
				next = append(next, expandSchemaBranches(root, addl)...)
			}
		}
		current = next
	}
	return current
}

func collectSchemaProperties(root map[string]any, schemas []map[string]any) (map[string]map[string]any, map[string]bool) {
	props := map[string]map[string]any{}
	req := map[string]bool{}
	for _, s := range schemas {
		s = derefSchema(root, s)
		if rList, ok := s["required"].([]any); ok {
			for _, r := range rList {
				if rs, ok := r.(string); ok {
					req[rs] = true
				}
			}
		}
		if pMap, ok := s["properties"].(map[string]any); ok {
			for k, v := range pMap {
				if vm, ok := v.(map[string]any); ok {
					if _, exists := props[k]; !exists {
						props[k] = vm
					}
				}
			}
		}
	}
	return props, req
}

func collectSchemaEnums(root map[string]any, schemas []map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, s := range schemas {
		s = derefSchema(root, s)
		if c, ok := s["const"].(string); ok {
			add(c)
		}
		if eList, ok := s["enum"].([]any); ok {
			for _, e := range eList {
				add(fmt.Sprint(e))
			}
		}
		if schemaTypeString(s) == "boolean" {
			add("true")
			add("false")
		}
	}
	return out
}

func schemaTypeString(s map[string]any) string {
	switch t := s["type"].(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " | ")
	}
	if _, ok := s["properties"]; ok {
		return "object"
	}
	if _, ok := s["items"]; ok {
		return "array"
	}
	if _, ok := s["enum"]; ok {
		return "string"
	}
	return "any"
}

func formatSchemaDoc(name string, s map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**`%s`** (`%s`)\n\n", name, schemaTypeString(s))
	if desc, ok := s["description"].(string); ok && desc != "" {
		b.WriteString(desc + "\n\n")
	}
	var meta []string
	if def, ok := s["default"]; ok {
		meta = append(meta, fmt.Sprintf("Default: `%v`", def))
	}
	if minV, ok := s["minimum"]; ok {
		meta = append(meta, fmt.Sprintf("Min: `%v`", minV))
	}
	if maxV, ok := s["maximum"]; ok {
		meta = append(meta, fmt.Sprintf("Max: `%v`", maxV))
	}
	if eList, ok := s["enum"].([]any); ok && len(eList) > 0 {
		vals := make([]string, 0, len(eList))
		for _, e := range eList {
			vals = append(vals, fmt.Sprintf("`%v`", e))
		}
		meta = append(meta, "Allowed: "+strings.Join(vals, ", "))
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " · "))
	}
	return b.String()
}

func formatTestTypeDoc(tt string) string {
	d := mcpScnTypeDefaultsFor(tt)
	tp := fmt.Sprintf("%d rec/s", d.throughput)
	if d.throughput == -1 {
		tp = "unlimited (-1)"
	}
	return fmt.Sprintf(
		"**`%s`** — %s\n\n"+
			"| Shipped Default | Value |\n"+
			"|---|---|\n"+
			"| `records` / `numRecords` | `%d` |\n"+
			"| `durationSeconds` / `durationMs` | `%ds` (`%dms`) |\n"+
			"| `parallelProducers` / `numProducers` | `%d` |\n"+
			"| `recordSizeBytes` / `recordSize` | `%d` bytes |\n"+
			"| `partitions` | `%d` |\n"+
			"| `acks` | `%q` |\n"+
			"| `targetThroughput` / `throughput` | `%s` |",
		tt, describeType(tt),
		d.numRecords, d.durationMs/1000, d.durationMs,
		d.numProducers, d.recordSize, d.partitions, d.acks, tp,
	)
}

func wordAtPosition(line string, lineIdx, col int) (string, lspRange) {
	if len(line) == 0 {
		return "", lspRange{}
	}
	col = min(max(col, 0), len(line)-1)
	isWordChar := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.' || b == '/'
	}
	start := col
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := col
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	if start == end {
		return "", lspRange{}
	}
	return line[start:end], lspRange{
		Start: lspPosition{Line: lineIdx, Character: start},
		End:   lspPosition{Line: lineIdx, Character: end},
	}
}
