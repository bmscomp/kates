package cmd

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/internal/compliance"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var resilienceLintCmd = &cobra.Command{
	Use:   "lint <resilience.yaml>...",
	Short: "Check resilience test files offline, as kates resilience run reads them",
	Long: `Checks resilience test files (ResilienceTestRequest) without a Kates API:
YAML or JSON syntax, required testRequest and chaosSpec blocks, every key
against the backend's ResilienceTestRequest, CreateTestRequest, FaultSpec and
ProbeSpec schemas (a typo is an error, with the key it is closest to),
FaultLimits bounds, Kubernetes label selector syntax, envOverrides allow-list,
and probe commands and comparators. Each finding is printed as
file:line:col: severity: path: message [code], with the source line under it;
-o json lists the findings instead.

` + fileLintExitCodes,
	Example: `  kates resilience lint cli/examples/resilience-pod-kill.yaml
  kates resilience lint cli/examples/resilience-*.yaml
  kates resilience lint -o json cli/examples/resilience-*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, files []string) error {
		return runFileLint(files, func(_ string, src []byte) []compliance.Diagnostic {
			return lintResilienceFile(src)
		})
	},
}

var disruptionLintCmd = &cobra.Command{
	Use:   "lint <plan.yaml>...",
	Short: "Check disruption plan and playbook files offline",
	Long: `Checks disruption plan and playbook files (DisruptionPlan) without a Kates
API: YAML or JSON syntax, required name and steps, every key against the
backend's DisruptionPlan, DisruptionStep, FaultSpec, ProbeSpec and
SlaDefinition schemas (a typo is an error, with the key it is closest to),
FaultLimits bounds, Kubernetes label selector syntax, envOverrides allow-list,
and probe commands and comparators. Each finding is printed as
file:line:col: severity: path: message [code], with the source line under it;
-o json lists the findings instead.

` + fileLintExitCodes,
	Example: `  kates disruption lint kates/src/main/resources/playbooks/single-broker-kill.yaml
  kates disruption lint kates/src/main/resources/playbooks/*.yaml
  kates disruption lint -o json kates/src/main/resources/playbooks/*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, files []string) error {
		return runFileLint(files, func(_ string, src []byte) []compliance.Diagnostic {
			return lintDisruptionFile(src)
		})
	},
}

var (
	resilienceTopKeys = []string{
		"testRequest", "chaosSpec", "probes", "steadyStateSec", "maxRecoveryWaitSec",
	}
	resilienceTopHints = map[string]string{
		"steps":     "multi-step chaos plans use DisruptionPlan (kates disruption lint / run)",
		"faultSpec": "ResilienceTestRequest uses chaosSpec at the top level",
		"type":      "nest the benchmark type under testRequest.type",
		"spec":      "nest the benchmark spec under testRequest.spec",
	}

	disruptionTopKeys = []string{
		"name", "description", "category", "steps", "sla", "testType",
		"baselineDurationSec", "isrTrackingTopic", "lagTrackingGroupId",
		"isrPollIntervalMs", "lagPollIntervalMs", "maxAffectedBrokers", "autoRollback",
	}
	disruptionTopHints = map[string]string{
		"testRequest": "DisruptionPlan uses testType and baselineDurationSec instead of testRequest",
		"chaosSpec":   "DisruptionPlan nests faults under steps[].faultSpec",
	}
	validPlaybookCategories = []string{
		"infrastructure", "network", "compute", "storage", "operations", "kafka", "general",
		"broker", "resource", "controller", "combined",
	}

	disruptionStepKeys = []string{
		"name", "faultSpec", "steadyStateSec", "observationWindowSec", "requireRecovery",
	}
	disruptionStepHints = map[string]string{
		"chaosSpec": "DisruptionStep uses faultSpec",
	}

	faultSpecKeys = []string{
		"experimentName", "targetNamespace", "targetLabel", "targetAll", "chaosDurationSec",
		"delayBeforeSec", "envOverrides", "disruptionType", "targetBrokerId",
		"gracePeriodSec", "networkLatencyMs", "fillPercentage", "cpuCores",
		"memoryMb", "ioWorkers", "targetTopic", "targetPartition", "probes",
	}
	validDisruptionTypes = []string{
		"POD_KILL", "POD_DELETE", "GRACEFUL_STOP", "NETWORK_PARTITION",
		"NETWORK_LATENCY", "DISK_FILL", "CPU_STRESS", "MEMORY_STRESS",
		"IO_STRESS", "DNS_ERROR", "ROLLING_RESTART", "SCALE_DOWN",
		"LEADER_ELECTION", "NODE_DRAIN",
	}
	durationRequiredTypes = []string{
		"NETWORK_PARTITION", "NETWORK_LATENCY", "CPU_STRESS", "MEMORY_STRESS",
		"IO_STRESS", "DNS_ERROR", "DISK_FILL", "NODE_DRAIN",
	}
	allowedEnvOverrideKeys = []string{"TARGET_NODE", "NODE_LABEL"}

	faultSpecIntBounds = map[string][2]int{
		"chaosDurationSec": {0, 3600},
		"delayBeforeSec":   {0, 600},
		"gracePeriodSec":   {0, 300},
		"networkLatencyMs": {1, 30000},
		"fillPercentage":   {1, 100},
		"cpuCores":         {1, 64},
		"memoryMb":         {1, 32768},
		"ioWorkers":        {1, 64},
		"targetPartition":  {0, 10000},
		"targetBrokerId":   {-1, 10000},
	}

	probeSpecKeys = []string{
		"name", "type", "mode", "command", "comparator", "expectedOutput",
		"intervalSec", "timeoutSec",
	}
	validProbeTypes       = []string{"kafkaProbe", "k8sProbe", "cmdProbe"}
	validProbeModes       = []string{"SOT", "EOT", "Continuous", "Edge", "OnChaos"}
	validProbeComparators = []string{
		"equal", "contains", "notContains", "==", "!=", ">=", "<=", ">", "<",
	}
	numericComparators = []string{"==", "!=", ">=", "<=", ">", "<"}
)

// lintResilienceFile checks a ResilienceTestRequest YAML/JSON file offline.
func lintResilienceFile(src []byte) []compliance.Diagnostic {
	l := &fileLinter{}
	doc := l.parseNode(src)
	if doc == nil {
		if !l.hasErrors() {
			l.errorAt(nil, "empty", "", "the file is empty: a resilience test requires testRequest and chaosSpec")
		}
		return l.sorted()
	}
	fields := l.mapping(doc, "", resilienceTopKeys, resilienceTopHints)
	if fields == nil {
		return l.sorted()
	}
	if tr, ok := fields["testRequest"]; ok && tr.Tag != "!!null" {
		l.testRequest(tr, "testRequest")
	} else {
		l.errorAt(doc, "missing-field", "testRequest", "testRequest is required")
	}
	if cs, ok := fields["chaosSpec"]; ok && cs.Tag != "!!null" {
		l.faultSpec(cs, "chaosSpec", true)
	} else {
		l.errorAt(doc, "missing-field", "chaosSpec", "chaosSpec is required")
	}
	if pr, ok := fields["probes"]; ok && pr.Tag != "!!null" {
		if pr.Kind != yaml.SequenceNode {
			l.errorAt(pr, "wrong-type", "probes", "probes is "+nodeKind(pr)+"; write a list of probes")
		} else {
			for i, item := range pr.Content {
				l.probeSpec(item, fmt.Sprintf("probes[%d]", i), false)
			}
		}
	}
	if ss, ok := fields["steadyStateSec"]; ok {
		l.checkIntBounds(ss, "steadyStateSec", 0, 86400)
	}
	if mr, ok := fields["maxRecoveryWaitSec"]; ok {
		l.checkIntBounds(mr, "maxRecoveryWaitSec", 1, 86400)
	}
	return l.sorted()
}

// lintDisruptionFile checks a DisruptionPlan or Playbook YAML/JSON file offline.
func lintDisruptionFile(src []byte) []compliance.Diagnostic {
	l := &fileLinter{}
	doc := l.parseNode(src)
	if doc == nil {
		if !l.hasErrors() {
			l.errorAt(nil, "empty", "", "the file is empty: a disruption plan requires name and steps")
		}
		return l.sorted()
	}
	fields := l.mapping(doc, "", disruptionTopKeys, disruptionTopHints)
	if fields == nil {
		return l.sorted()
	}
	l.checkDisruptionTopScalars(doc, fields)
	autoRollback := true
	var autoRollbackNode *yaml.Node
	if ar, ok := fields["autoRollback"]; ok {
		autoRollbackNode = ar
		before := len(l.ds)
		l.decodeAs(ar, reflect.TypeOf(false), "autoRollback")
		if len(l.ds) == before {
			_ = ar.Decode(&autoRollback)
		}
	}
	if sla, ok := fields["sla"]; ok && sla.Tag != "!!null" {
		l.slaSpec(sla, "sla")
	}

	hasScaleDown := false
	steps, hasSteps := fields["steps"]
	switch {
	case !hasSteps || steps.Tag == "!!null" || (steps.Kind == yaml.SequenceNode && len(steps.Content) == 0):
		l.errorAt(doc, "no-steps", "steps", "a disruption plan requires at least one step")
	case steps.Kind != yaml.SequenceNode:
		l.errorAt(steps, "wrong-type", "steps", "steps is "+nodeKind(steps)+"; write a list of steps")
	default:
		for i, step := range steps.Content {
			if l.disruptionStep(step, fmt.Sprintf("steps[%d]", i)) {
				hasScaleDown = true
			}
		}
	}

	if hasScaleDown && !autoRollback && autoRollbackNode != nil {
		l.warnAt(autoRollbackNode, "scaledown-without-rollback", "autoRollback",
			"SCALE_DOWN step with autoRollback: false leaves the broker scaled down after the plan completes")
	}
	return l.sorted()
}

func (l *fileLinter) checkDisruptionTopScalars(doc *yaml.Node, fields map[string]*yaml.Node) {
	if name, ok := fields["name"]; !ok || name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		l.errorAt(doc, "missing-field", "name", "disruption plan name is required")
	}
	if desc, ok := fields["description"]; ok && desc.Kind != yaml.ScalarNode {
		l.errorAt(desc, "wrong-type", "description", "description is "+nodeKind(desc)+"; write text")
	}
	if cat, ok := fields["category"]; ok {
		if cat.Kind != yaml.ScalarNode || !slices.Contains(validPlaybookCategories, cat.Value) {
			msg := fmt.Sprintf("unknown playbook category %s; must be one of %s", nodeKind(cat), strings.Join(validPlaybookCategories, ", "))
			if guess := compliance.Closest(cat.Value, validPlaybookCategories); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(cat, "wrong-value", "category", msg)
		}
	}
	if tt, ok := fields["testType"]; ok && tt.Value != "" {
		l.checkTestType(tt, "testType")
	}
	if bd, ok := fields["baselineDurationSec"]; ok {
		l.checkIntBounds(bd, "baselineDurationSec", 0, 86400)
	}
	if topic, ok := fields["isrTrackingTopic"]; ok {
		if topic.Kind != yaml.ScalarNode {
			l.errorAt(topic, "wrong-type", "isrTrackingTopic", "isrTrackingTopic is "+nodeKind(topic)+"; write a topic name")
		} else if topic.Value != "" && (topic.Value == "." || topic.Value == ".." || !mcpKafkaTopicRE.MatchString(topic.Value)) {
			l.errorAt(topic, "wrong-value", "isrTrackingTopic", fmt.Sprintf("isrTrackingTopic %q is not a valid Kafka topic name", topic.Value))
		}
	}
	if grp, ok := fields["lagTrackingGroupId"]; ok {
		if grp.Kind != yaml.ScalarNode {
			l.errorAt(grp, "wrong-type", "lagTrackingGroupId", "lagTrackingGroupId is "+nodeKind(grp)+"; write a group ID")
		} else if grp.Value != "" && (strings.TrimSpace(grp.Value) == "" || !mcpScnGroupRE.MatchString(grp.Value)) {
			l.errorAt(grp, "wrong-value", "lagTrackingGroupId", fmt.Sprintf("lagTrackingGroupId %q is not a valid consumer group ID", grp.Value))
		}
	}
	if ip, ok := fields["isrPollIntervalMs"]; ok {
		l.checkIntBounds(ip, "isrPollIntervalMs", 100, 600000)
	}
	if lp, ok := fields["lagPollIntervalMs"]; ok {
		l.checkIntBounds(lp, "lagPollIntervalMs", 100, 600000)
	}
	if mb, ok := fields["maxAffectedBrokers"]; ok {
		l.checkIntBounds(mb, "maxAffectedBrokers", -1, 1000)
		var v int
		if mb.Kind == yaml.ScalarNode && mb.Decode(&v) == nil && v == 0 {
			l.errorAt(mb, "wrong-value", "maxAffectedBrokers", "maxAffectedBrokers cannot be 0 (use -1 for no cap, or >= 1)")
		}
	}
}

func (l *fileLinter) disruptionStep(n *yaml.Node, path string) (isScaleDown bool) {
	fields := l.mapping(n, path, disruptionStepKeys, disruptionStepHints)
	if fields == nil {
		return false
	}
	if name, ok := fields["name"]; !ok || name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		l.errorAt(n, "missing-field", childPath(path, "name"), "step name is required")
	}
	if ss, ok := fields["steadyStateSec"]; ok {
		l.checkIntBounds(ss, childPath(path, "steadyStateSec"), 0, 86400)
	}
	if ow, ok := fields["observationWindowSec"]; ok {
		l.checkIntBounds(ow, childPath(path, "observationWindowSec"), 1, 86400)
	}
	if rr, ok := fields["requireRecovery"]; ok {
		l.decodeAs(rr, reflect.TypeOf(false), childPath(path, "requireRecovery"))
	}
	if fs, ok := fields["faultSpec"]; ok && fs.Tag != "!!null" {
		dt := l.faultSpec(fs, childPath(path, "faultSpec"), false)
		return dt == "SCALE_DOWN"
	}
	l.errorAt(n, "missing-field", childPath(path, "faultSpec"), "step faultSpec is required")
	return false
}

func (l *fileLinter) faultSpec(n *yaml.Node, path string, inResilience bool) (disruptionType string) {
	fields := l.mapping(n, path, faultSpecKeys, nil)
	if fields == nil {
		return ""
	}
	expName := ""
	if exp, ok := fields["experimentName"]; ok {
		if exp.Kind != yaml.ScalarNode || strings.TrimSpace(exp.Value) == "" {
			l.errorAt(exp, "wrong-value", childPath(path, "experimentName"), "experimentName must be a non-empty string")
		} else {
			expName = exp.Value
		}
	} else {
		l.errorAt(n, "missing-field", childPath(path, "experimentName"), "experimentName is required")
	}

	if dt, ok := fields["disruptionType"]; ok && dt.Value != "" {
		upper := strings.ToUpper(dt.Value)
		if dt.Kind != yaml.ScalarNode || !slices.Contains(validDisruptionTypes, upper) {
			msg := fmt.Sprintf("there is no disruptionType %s; must be one of %s", nodeKind(dt), strings.Join(validDisruptionTypes, ", "))
			if guess := compliance.Closest(upper, validDisruptionTypes); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(dt, "unknown-type", childPath(path, "disruptionType"), msg)
		} else {
			disruptionType = upper
		}
	}

	if ns, ok := fields["targetNamespace"]; ok && ns.Kind != yaml.ScalarNode {
		l.errorAt(ns, "wrong-type", childPath(path, "targetNamespace"), "targetNamespace must be a string")
	}
	if ta, ok := fields["targetAll"]; ok {
		l.decodeAs(ta, reflect.TypeOf(false), childPath(path, "targetAll"))
	}

	if tl, ok := fields["targetLabel"]; ok {
		p := childPath(path, "targetLabel")
		if tl.Kind != yaml.ScalarNode {
			l.errorAt(tl, "wrong-type", p, "targetLabel must be a string")
		} else if err := validateLabelSelector(tl.Value); err != nil {
			l.errorAt(tl, "invalid-label-selector", p, err.Error())
		}
	}

	for key, bounds := range faultSpecIntBounds {
		if v, ok := fields[key]; ok {
			l.checkIntBounds(v, childPath(path, key), bounds[0], bounds[1])
		}
	}

	// Cross-field check on chaosDurationSec vs disruptionType / experimentName
	if durNode, ok := fields["chaosDurationSec"]; ok {
		var dur int
		if durNode.Kind == yaml.ScalarNode && durNode.Decode(&dur) == nil && dur <= 0 {
			p := childPath(path, "chaosDurationSec")
			switch {
			case slices.Contains(durationRequiredTypes, disruptionType) || (disruptionType == "" && expName == "node-drain"):
				name := disruptionType
				if name == "" {
					name = expName
				}
				l.errorAt(durNode, "fault-requires-duration", p, fmt.Sprintf("%s requires chaosDurationSec > 0", name))
			case disruptionType == "ROLLING_RESTART" || disruptionType == "SCALE_DOWN":
				l.warnAt(durNode, "zero-chaos-duration", p, fmt.Sprintf("chaosDurationSec is 0 on %s", disruptionType))
			}
		}
	}

	if env, ok := fields["envOverrides"]; ok && env.Tag != "!!null" {
		l.faultSpecEnvOverrides(env, childPath(path, "envOverrides"))
	}
	l.faultSpecTargetAndProbes(fields, path, inResilience)
	return disruptionType
}

func (l *fileLinter) faultSpecEnvOverrides(env *yaml.Node, p string) {
	if env.Kind != yaml.MappingNode {
		l.errorAt(env, "wrong-type", p, "envOverrides is "+nodeKind(env)+"; write a mapping")
		return
	}
	for i := 0; i+1 < len(env.Content); i += 2 {
		k, v := env.Content[i], env.Content[i+1]
		kp := childPath(p, k.Value)
		if !slices.Contains(allowedEnvOverrideKeys, k.Value) {
			msg := fmt.Sprintf("envOverrides key %q is not allowed; only TARGET_NODE and NODE_LABEL are permitted", k.Value)
			if guess := compliance.Closest(k.Value, allowedEnvOverrideKeys); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(k, "invalid-env-override", kp, msg)
			continue
		}
		if v.Kind != yaml.ScalarNode {
			l.errorAt(v, "wrong-type", kp, "envOverrides value must be a string")
			continue
		}
		if strings.TrimSpace(v.Value) == "" {
			l.warnAt(v, "empty-env-override", kp, fmt.Sprintf("envOverrides.%s is empty", k.Value))
		} else if k.Value == "NODE_LABEL" {
			if err := validateLabelSelector(v.Value); err != nil {
				l.errorAt(v, "invalid-label-selector", kp, err.Error())
			}
		}
	}
}

func (l *fileLinter) faultSpecTargetAndProbes(fields map[string]*yaml.Node, path string, inResilience bool) {
	if tt, ok := fields["targetTopic"]; ok {
		p := childPath(path, "targetTopic")
		if tt.Kind != yaml.ScalarNode {
			l.errorAt(tt, "wrong-type", p, "targetTopic must be a string")
		} else if tt.Value != "" {
			if tt.Value == "." || tt.Value == ".." || !mcpKafkaTopicRE.MatchString(tt.Value) {
				l.errorAt(tt, "wrong-value", p, fmt.Sprintf("targetTopic %q is not a valid Kafka topic name", tt.Value))
			} else if inResilience {
				l.warnAt(tt, "unused-field", p, "kates resilience run ignores targetTopic; only kates disruption run resolves partition leaders")
			}
		}
	}
	if tp, ok := fields["targetPartition"]; ok && inResilience {
		l.warnAt(tp, "unused-field", childPath(path, "targetPartition"),
			"kates resilience run ignores targetPartition; only kates disruption run resolves partition leaders")
	}

	if pr, ok := fields["probes"]; ok && pr.Tag != "!!null" {
		p := childPath(path, "probes")
		if pr.Kind != yaml.SequenceNode {
			l.errorAt(pr, "wrong-type", p, "probes is "+nodeKind(pr)+"; write a list of probes")
		} else {
			for i, item := range pr.Content {
				l.probeSpec(item, fmt.Sprintf("%s[%d]", p, i), true)
			}
		}
	}
}

func (l *fileLinter) probeSpec(n *yaml.Node, path string, inFaultSpec bool) {
	fields := l.mapping(n, path, probeSpecKeys, nil)
	if fields == nil {
		return
	}
	if name, ok := fields["name"]; !ok || name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		l.errorAt(n, "missing-field", childPath(path, "name"), "probe name is required")
	}
	probeType := "cmdProbe"
	if pt, ok := fields["type"]; ok && pt.Value != "" {
		if pt.Kind != yaml.ScalarNode || !slices.Contains(validProbeTypes, pt.Value) {
			msg := fmt.Sprintf("unknown probe type %s; must be one of %s", nodeKind(pt), strings.Join(validProbeTypes, ", "))
			if guess := compliance.Closest(pt.Value, validProbeTypes); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(pt, "unknown-probe-type", childPath(path, "type"), msg)
			probeType = ""
		} else {
			probeType = pt.Value
		}
	}
	if inFaultSpec && probeType == "cmdProbe" {
		target := n
		if pt, ok := fields["type"]; ok {
			target = pt
		}
		l.errorAt(target, "cmdprobe-in-faultspec", childPath(path, "type"),
			"FaultSpec.probes only supports kafkaProbe and k8sProbe; cmdProbe is rejected here")
	}

	if mode, ok := fields["mode"]; ok && mode.Value != "" {
		if mode.Kind != yaml.ScalarNode || !slices.Contains(validProbeModes, mode.Value) {
			msg := fmt.Sprintf("unknown probe mode %s; must be one of %s", nodeKind(mode), strings.Join(validProbeModes, ", "))
			if guess := compliance.Closest(mode.Value, validProbeModes); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(mode, "unknown-probe-mode", childPath(path, "mode"), msg)
		}
	}

	comparator := "equal"
	if comp, ok := fields["comparator"]; ok && comp.Value != "" {
		if comp.Kind != yaml.ScalarNode || !slices.Contains(validProbeComparators, comp.Value) {
			l.errorAt(comp, "unknown-comparator", childPath(path, "comparator"),
				fmt.Sprintf("unknown comparator %s; must be one of %s", nodeKind(comp), strings.Join(validProbeComparators, ", ")))
			comparator = ""
		} else {
			comparator = comp.Value
		}
	}

	l.probeCommandAndOutput(n, fields, path, probeType, comparator)

	if iv, ok := fields["intervalSec"]; ok {
		l.checkIntBounds(iv, childPath(path, "intervalSec"), 1, 3600)
	}
	if to, ok := fields["timeoutSec"]; ok {
		l.checkIntBounds(to, childPath(path, "timeoutSec"), 1, 3600)
	}
}

func (l *fileLinter) probeCommandAndOutput(n *yaml.Node, fields map[string]*yaml.Node, path, probeType, comparator string) {
	if cmdNode, ok := fields["command"]; ok {
		p := childPath(path, "command")
		if cmdNode.Kind != yaml.ScalarNode || strings.TrimSpace(cmdNode.Value) == "" {
			l.errorAt(cmdNode, "missing-field", p, "probe command must be a non-empty string")
		} else if probeType == "kafkaProbe" {
			if err := validateKafkaProbeCommand(cmdNode.Value); err != nil {
				l.errorAt(cmdNode, "invalid-kafka-probe-command", p, err.Error())
			}
		}
	} else {
		l.errorAt(n, "missing-field", childPath(path, "command"), "probe command is required")
	}

	if expNode, ok := fields["expectedOutput"]; ok {
		p := childPath(path, "expectedOutput")
		if expNode.Kind != yaml.ScalarNode {
			l.errorAt(expNode, "wrong-type", p, "expectedOutput must be a scalar string or number")
		} else if slices.Contains(numericComparators, comparator) {
			if _, err := strconv.ParseFloat(strings.TrimSpace(expNode.Value), 64); err != nil {
				l.errorAt(expNode, "invalid-probe", p,
					fmt.Sprintf("numeric comparator %q requires a numeric expectedOutput, got %q", comparator, expNode.Value))
			}
		}
	} else {
		l.errorAt(n, "missing-field", childPath(path, "expectedOutput"), "probe expectedOutput is required")
	}
}

func (l *fileLinter) checkIntBounds(n *yaml.Node, path string, minVal, maxVal int) {
	before := len(l.ds)
	l.decodeAs(n, reflect.TypeOf(int(0)), path)
	if len(l.ds) > before {
		return
	}
	var v int
	if n.Decode(&v) == nil && (v < minVal || v > maxVal) {
		l.errorAt(n, "wrong-value", path, fmt.Sprintf("%s is %d; must be between %d and %d", path, v, minVal, maxVal))
	}
}

// validateKafkaProbeCommand mirrors KafkaProbeChecks.java.
func validateKafkaProbeCommand(cmd string) error {
	trimmed := strings.TrimSpace(cmd)
	switch {
	case trimmed == "under-replicated-partitions" || trimmed == "unavailable-partitions":
		return nil
	case trimmed == "produce" || strings.HasPrefix(trimmed, "produce "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "produce"))
		if arg == "" || strings.ContainsAny(arg, " \t") || arg == "." || arg == ".." || !mcpKafkaTopicRE.MatchString(arg) {
			return fmt.Errorf("kafkaProbe 'produce <topic>' requires a single valid Kafka topic name, got %q", cmd)
		}
		return nil
	case trimmed == "consumer-lag" || strings.HasPrefix(trimmed, "consumer-lag "):
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "consumer-lag"))
		if strings.ContainsAny(arg, " \t") {
			return fmt.Errorf("kafkaProbe 'consumer-lag [<group>]' takes at most one group ID, got %q", cmd)
		}
		return nil
	default:
		return fmt.Errorf("invalid kafkaProbe command %q; must be under-replicated-partitions, unavailable-partitions, 'produce <topic>', or 'consumer-lag [<group>]'", cmd)
	}
}

var (
	k8sLabelKeyRE = regexp.MustCompile(`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)
	k8sLabelValRE = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$`)
	k8sSetExprRE  = regexp.MustCompile(`^([^\s()]+)\s+(in|notin)\s*\(([^)]*)\)$`)
)

// validateLabelSelector mirrors ParsedLabelSelector.java.
func validateLabelSelector(sel string) error {
	trimmed := strings.TrimSpace(sel)
	if trimmed == "" {
		return fmt.Errorf("label selector cannot be empty")
	}
	if strings.ContainsAny(trimmed, "<>") {
		return fmt.Errorf("label selector %q cannot contain '<' or '>'", sel)
	}
	clauses, err := splitSelectorClauses(trimmed)
	if err != nil {
		return err
	}
	for _, c := range clauses {
		c = strings.TrimSpace(c)
		if c == "" {
			return fmt.Errorf("label selector %q has an empty clause", sel)
		}
		if m := k8sSetExprRE.FindStringSubmatch(c); m != nil {
			key, vals := m[1], m[3]
			if !k8sLabelKeyRE.MatchString(key) {
				return fmt.Errorf("invalid label key %q in selector %q", key, sel)
			}
			parts := strings.Split(vals, ",")
			hasVal := false
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					return fmt.Errorf("empty value in set selector %q", c)
				}
				if !k8sLabelValRE.MatchString(p) {
					return fmt.Errorf("invalid label value %q in selector %q", p, sel)
				}
				hasVal = true
			}
			if !hasVal {
				return fmt.Errorf("set selector %q lists no values", c)
			}
			continue
		}
		if strings.ContainsAny(c, "()") {
			return fmt.Errorf("malformed set-based label selector %q", c)
		}
		var key, val string
		hasEq := false
		if idx := strings.Index(c, "!="); idx >= 0 {
			key, val, hasEq = strings.TrimSpace(c[:idx]), strings.TrimSpace(c[idx+2:]), true
		} else if idx := strings.Index(c, "=="); idx >= 0 {
			key, val, hasEq = strings.TrimSpace(c[:idx]), strings.TrimSpace(c[idx+2:]), true
		} else if idx := strings.IndexByte(c, '='); idx >= 0 {
			key, val, hasEq = strings.TrimSpace(c[:idx]), strings.TrimSpace(c[idx+1:]), true
		} else {
			key = strings.TrimSpace(strings.TrimPrefix(c, "!"))
		}
		if !k8sLabelKeyRE.MatchString(key) {
			return fmt.Errorf("invalid label key %q in selector %q", key, sel)
		}
		if hasEq && (strings.Contains(val, "=") || !k8sLabelValRE.MatchString(val)) {
			return fmt.Errorf("invalid label value %q in selector %q", val, sel)
		}
	}
	return nil
}

func splitSelectorClauses(s string) ([]string, error) {
	var clauses []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced ')' in label selector %q", s)
			}
		case ',':
			if depth == 0 {
				clauses = append(clauses, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed '(' in label selector %q", s)
	}
	clauses = append(clauses, s[start:])
	return clauses, nil
}

func init() {
	resilienceCmd.AddCommand(resilienceLintCmd)
	disruptionCmd.AddCommand(disruptionLintCmd)
}
