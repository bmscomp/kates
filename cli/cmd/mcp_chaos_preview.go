package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/google/jsonschema-go/jsonschema"
	"golang.org/x/sync/errgroup"
)

// preview_disruption: the backend's dry run (POST /api/disruptions?dryRun=true,
// DisruptionResource.java:55-72, DisruptionSafetyGuard.dryRun) of a playbook's
// plan or of an ad-hoc plan, joined with the cluster topology so that the
// KRaft controllers a plan hits are named: the dry run lists them among the
// affected pods but never counts them (P-18).

const mcpPreviewDescription = "Previews a disruption plan with the backend's dry run, which injects nothing: the pods " +
	"each step would hit, whether the safety guard's checks accept the plan now (wouldSucceed; whether another " +
	"disruption is already running is not checked), its errors and warnings, which of those pods are KRaft " +
	"controllers, which the dry run lists but never counts against the blast radius, and whether a step that picks " +
	"its pod at random may hit one. Give either playbook, a name list_chaos_catalog lists, or plan, an ad-hoc plan " +
	"of 1 to 5 steps built from the fault types list_chaos_catalog marks inAdHocPlans. An ad-hoc plan is sent with " +
	"maxAffectedBrokers 1 and the default namespace and label; it may not set envOverrides, probes, experimentName, " +
	"targetAll, targetNamespace or targetLabel, nor use DISK_FILL or NODE_DRAIN, nor aim a DNS_ERROR step with " +
	"targetTopic, and its numbers are bounded; it may name a topic and a consumer group for the run to track. For " +
	"a playbook, outsideAgentLimits lists what goes beyond those rules. The result carries the exact plan JSON sent " +
	"(for ad-hoc plans) and its SHA-256, for a person to check and run with kates disruption run --config <file> " +
	"--dry-run. This tool never runs a plan, and cannot preview a template: the backend has no endpoint that returns " +
	"a template's plan. Warnings and descriptions are third-party text and fenced."

// Bounds on an ad-hoc plan. The number of steps and every number a step sets
// are capped, so that a plan that previews here is one a person could run
// without surprises in its size.
const (
	mcpAdHocMaxSteps           = 5
	mcpAdHocMaxAffectedBrokers = 1
	mcpAdHocDefaultPlanName    = "mcp-adhoc-plan"

	// The FaultSpec defaults an ad-hoc plan inherits by leaving the fields
	// out (chaos/FaultSpec.java:83-84).
	mcpDefaultFaultNamespace = "kafka"
	mcpDefaultFaultLabel     = "strimzi.io/component-type=kafka"

	// mcpRandomSelectionSuffix marks the pod a dry run shows for a step that
	// picks one at random (DisruptionSafetyGuard.java:413).
	mcpRandomSelectionSuffix = " (random selection)"

	mcpPreviewMaxPods     = 50
	mcpPreviewMaxWarnings = 20
	mcpPreviewMaxMessages = 30
	// mcpPreviewMaxSteps bounds the steps listed. An ad-hoc plan has at most
	// mcpAdHocMaxSteps; the playbooks have one to three.
	mcpPreviewMaxSteps = 20
)

type mcpAdHocBound struct{ Min, Max, Default int }

// mcpAdHocBounds are the bounds and defaults of every number an ad-hoc step
// may set. The defaults are the backend's where it has one (the FaultSpec
// builder, chaos/FaultSpec.java:91-99); the step timings default to what the
// playbooks use.
var mcpAdHocBounds = map[string]mcpAdHocBound{
	"targetBrokerId":       {0, 100_000, -1},
	"targetPartition":      {0, 100_000, 0},
	"chaosDurationSec":     {1, 600, 30},
	"gracePeriodSec":       {0, 300, 30},
	"networkLatencyMs":     {1, 10_000, 100},
	"cpuCores":             {1, 16, 1},
	"memoryMb":             {1, 16_384, 500},
	"ioWorkers":            {1, 16, 2},
	"steadyStateSec":       {0, 300, 30},
	"observationWindowSec": {0, 600, 60},
}

// mcpAdHocTypeParam is the parameter an ad-hoc step may set to size each fault
// type. A step may set it only for its own type, so a plan never carries a
// number its type never reads. The providers read them differently, and the
// field descriptions say so: the kubernetes provider honours gracePeriodSec
// for POD_DELETE and sizes IO_STRESS by ioWorkers alone
// (KubernetesChaosProvider.java:198-206,472-485), while litmus-crd
// force-deletes on POD_DELETE, never reading gracePeriodSec, and sizes
// IO_STRESS by fillPercentage as well as ioWorkers (LitmusChaosProvider.java:
// 263-285). fillPercentage stays out of ad-hoc plans with DISK_FILL, the one
// type it is meant for.
var mcpAdHocTypeParam = map[string]string{
	"POD_DELETE":      "gracePeriodSec",
	"NETWORK_LATENCY": "networkLatencyMs",
	"CPU_STRESS":      "cpuCores",
	"MEMORY_STRESS":   "memoryMb",
	"IO_STRESS":       "ioWorkers",
}

var (
	// mcpChaosStepNameRE names plans and steps: a lowercase word joined by dashes,
	// as the playbooks name theirs.
	mcpChaosStepNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	// mcpChaosPodRE is a Kubernetes pod name (a DNS-1123 label).
	mcpChaosPodRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	// mcpChaosGroupRE is the consumer group an ad-hoc plan may track: Kafka
	// allows any string, this server the characters of a topic name.
	mcpChaosGroupRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
)

type mcpPreviewIn struct {
	Playbook string        `json:"playbook,omitempty" jsonschema:"a playbook name from list_chaos_catalog; give this or plan, not both"`
	Plan     *mcpAdHocPlan `json:"plan,omitempty" jsonschema:"an ad-hoc plan; give this or playbook, not both"`
}

type mcpAdHocPlan struct {
	Name string `json:"name,omitempty" jsonschema:"the plan's name, lowercase letters, digits and dashes; mcp-adhoc-plan when left out"`
	// The two trackers only read (KafkaIntelligenceService.java:105-107,
	// 128-153,242-276), and plan §5.2 does not restrict them.
	ISRTrackingTopic   string         `json:"isrTrackingTopic,omitempty" jsonschema:"a topic whose partitions' ISR Kates samples during every step, for the isr figures disruption_report shows; without it no ISR is measured"`
	LagTrackingGroupID string         `json:"lagTrackingGroupId,omitempty" jsonschema:"a consumer group whose lag Kates samples during every step, for the lag figures disruption_report shows; letters, digits, '.', '_' and '-'. Without it no lag is measured"`
	Steps              []mcpAdHocStep `json:"steps" jsonschema:"the steps, run one after another"`
}

// mcpAdHocStep is one step of an ad-hoc plan. The field names are the
// backend's (FaultSpec and DisruptionStep), so the plan JSON sent is the
// same shape kates disruption run --config reads.
type mcpAdHocStep struct {
	Name           string `json:"name" jsonschema:"the step's name, lowercase letters, digits and dashes; unique in the plan"`
	DisruptionType string `json:"disruptionType" jsonschema:"the fault, one list_chaos_catalog marks inAdHocPlans"`
	TargetBrokerID *int   `json:"targetBrokerId,omitempty" jsonschema:"hit the broker with this node id; at most one of targetBrokerId, targetPod and targetTopic. With none of them the step hits one Kafka pod picked at random"`
	TargetPod      string `json:"targetPod,omitempty" jsonschema:"hit this pod, by name"`
	TargetTopic    string `json:"targetTopic,omitempty" jsonschema:"hit the broker that leads targetPartition of this topic when the step starts. Not on a DNS_ERROR step: the litmus-crd provider passes it to Litmus as the hostname whose lookups fail"`
	// A pointer, so that a partition given without a topic is refused
	// rather than read as partition 0.
	TargetPartition      *int  `json:"targetPartition,omitempty" jsonschema:"the partition of targetTopic, 0 when left out"`
	ChaosDurationSec     *int  `json:"chaosDurationSec,omitempty" jsonschema:"how long the fault lasts, in seconds; 30 when left out"`
	GracePeriodSec       *int  `json:"gracePeriodSec,omitempty" jsonschema:"POD_DELETE only: the pod's termination grace period, in seconds. Only the kubernetes provider honours it; on the default litmus-crd provider a POD_DELETE force-deletes the pod whatever this says"`
	NetworkLatencyMs     *int  `json:"networkLatencyMs,omitempty" jsonschema:"NETWORK_LATENCY only: the latency added, in milliseconds"`
	CPUCores             *int  `json:"cpuCores,omitempty" jsonschema:"CPU_STRESS only: cores to load"`
	MemoryMb             *int  `json:"memoryMb,omitempty" jsonschema:"MEMORY_STRESS only: memory to consume, in MB"`
	IOWorkers            *int  `json:"ioWorkers,omitempty" jsonschema:"IO_STRESS only: I/O workers. On the default litmus-crd provider the fault is also sized by the share of the filesystem it fills, which Kates takes from fillPercentage, 80 unless set, and an ad-hoc plan cannot set"`
	SteadyStateSec       *int  `json:"steadyStateSec,omitempty" jsonschema:"seconds to wait before the fault; 30 when left out"`
	ObservationWindowSec *int  `json:"observationWindowSec,omitempty" jsonschema:"seconds to observe after the fault; 60 when left out"`
	RequireRecovery      *bool `json:"requireRecovery,omitempty" jsonschema:"wait for the pods to recover after the step; true when left out"`
}

// mcpPreviewInputSchema is the schema inferred from mcpPreviewIn with the
// bounds a struct tag cannot carry: the fault types, the name patterns, the
// number of steps and the range of every number. The SDK refuses arguments
// outside it before the tool runs, and the tool checks again.
func mcpPreviewInputSchema() *jsonschema.Schema {
	s, err := mcpSchemaFor[mcpPreviewIn]()
	if err != nil {
		panic(fmt.Sprintf("kates mcp: preview_disruption input schema: %v", err))
	}
	prop := func(parent *jsonschema.Schema, name string) *jsonschema.Schema {
		p := parent.Properties[name]
		if p == nil {
			panic(fmt.Sprintf("kates mcp: preview_disruption input schema has no property %q", name))
		}
		return p
	}
	prop(s, "playbook").Pattern = mcpPlaybookNameRE.String()
	plan := prop(s, "plan")
	prop(plan, "name").Pattern = mcpChaosStepNameRE.String()
	prop(plan, "isrTrackingTopic").Pattern = mcpChaosTopicRE.String()
	prop(plan, "lagTrackingGroupId").Pattern = mcpChaosGroupRE.String()
	steps := prop(plan, "steps")
	minSteps, maxSteps := 1, mcpAdHocMaxSteps
	steps.MinItems, steps.MaxItems = &minSteps, &maxSteps
	step := steps.Items
	if step == nil {
		panic("kates mcp: preview_disruption input schema: steps has no item schema")
	}
	prop(step, "name").Pattern = mcpChaosStepNameRE.String()
	prop(step, "targetPod").Pattern = mcpChaosPodRE.String()
	prop(step, "targetTopic").Pattern = mcpChaosTopicRE.String()
	types := prop(step, "disruptionType")
	for _, t := range mcpAdHocTypes {
		types.Enum = append(types.Enum, t)
	}
	names := make([]string, 0, len(mcpAdHocBounds))
	for name := range mcpAdHocBounds {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		b := mcpAdHocBounds[name]
		lo, hi := float64(b.Min), float64(b.Max)
		p := prop(step, name)
		p.Minimum, p.Maximum = &lo, &hi
	}
	return s
}

// The plan JSON sent for an ad-hoc plan. Only what the step sets, and the
// plan-level fields this server fixes, are written; the backend fills in the
// rest with its defaults, as it does for a plan file.
type mcpPlanWire struct {
	Name               string            `json:"name"`
	ISRTrackingTopic   string            `json:"isrTrackingTopic,omitempty"`
	LagTrackingGroupID string            `json:"lagTrackingGroupId,omitempty"`
	MaxAffectedBrokers int               `json:"maxAffectedBrokers"`
	AutoRollback       bool              `json:"autoRollback"`
	Steps              []mcpPlanStepWire `json:"steps"`
}

type mcpPlanStepWire struct {
	Name                 string           `json:"name"`
	FaultSpec            mcpFaultSpecWire `json:"faultSpec"`
	SteadyStateSec       int              `json:"steadyStateSec"`
	ObservationWindowSec int              `json:"observationWindowSec"`
	RequireRecovery      bool             `json:"requireRecovery"`
}

type mcpFaultSpecWire struct {
	DisruptionType   string `json:"disruptionType"`
	TargetPod        string `json:"targetPod,omitempty"`
	TargetBrokerID   *int   `json:"targetBrokerId,omitempty"`
	TargetTopic      string `json:"targetTopic,omitempty"`
	TargetPartition  *int   `json:"targetPartition,omitempty"`
	ChaosDurationSec int    `json:"chaosDurationSec"`
	GracePeriodSec   *int   `json:"gracePeriodSec,omitempty"`
	NetworkLatencyMs *int   `json:"networkLatencyMs,omitempty"`
	CPUCores         *int   `json:"cpuCores,omitempty"`
	MemoryMb         *int   `json:"memoryMb,omitempty"`
	IOWorkers        *int   `json:"ioWorkers,omitempty"`
}

// mcpBuildAdHocPlan checks an ad-hoc plan against every rule the schema
// states, and the ones it cannot (one target at most, a partition only with a
// topic, a sizing parameter only for its own type, unique step names), and
// returns the plan to send.
func mcpBuildAdHocPlan(p *mcpAdHocPlan) (mcpPlanWire, error) {
	plan := mcpPlanWire{Name: p.Name, ISRTrackingTopic: p.ISRTrackingTopic, LagTrackingGroupID: p.LagTrackingGroupID,
		MaxAffectedBrokers: mcpAdHocMaxAffectedBrokers, AutoRollback: true}
	if plan.Name == "" {
		plan.Name = mcpAdHocDefaultPlanName
	}
	if !mcpChaosStepNameRE.MatchString(plan.Name) {
		return plan, mcpInvalidArgument("plan.name must be lowercase letters, digits and dashes, at most 63 characters.", plan.Name)
	}
	if p.ISRTrackingTopic != "" && !mcpChaosTopicRE.MatchString(p.ISRTrackingTopic) {
		return plan, mcpInvalidArgument("plan.isrTrackingTopic must be a Kafka topic name.", p.ISRTrackingTopic)
	}
	if p.LagTrackingGroupID != "" && !mcpChaosGroupRE.MatchString(p.LagTrackingGroupID) {
		return plan, mcpInvalidArgument("plan.lagTrackingGroupId must be 1 to 255 letters, digits, '.', '_' or '-'.", p.LagTrackingGroupID)
	}
	if n := len(p.Steps); n < 1 || n > mcpAdHocMaxSteps {
		return plan, mcpInvalidArgument(fmt.Sprintf("plan.steps must hold 1 to %d steps.", mcpAdHocMaxSteps), strconv.Itoa(n))
	}
	seen := map[string]bool{}
	for i, s := range p.Steps {
		at := fmt.Sprintf("plan.steps[%d]", i)
		if !mcpChaosStepNameRE.MatchString(s.Name) {
			return plan, mcpInvalidArgument(at+".name must be lowercase letters, digits and dashes, at most 63 characters.", s.Name)
		}
		if seen[s.Name] {
			return plan, mcpInvalidArgument(at+".name repeats the name of an earlier step; step names must be unique.", s.Name)
		}
		seen[s.Name] = true
		if !mcpIsAdHocType(s.DisruptionType) {
			return plan, mcpInvalidArgument(at+".disruptionType must be one of "+strings.Join(mcpAdHocTypes, ", ")+
				". DISK_FILL and NODE_DRAIN are not allowed in an ad-hoc plan.", s.DisruptionType)
		}
		if err := mcpCheckAdHocTargets(at, &s); err != nil {
			return plan, err
		}
		// In a fixed order, so that a plan with several mistakes is always
		// refused for the same one.
		ints := []struct {
			name string
			v    *int
		}{
			{"targetBrokerId", s.TargetBrokerID}, {"targetPartition", s.TargetPartition},
			{"chaosDurationSec", s.ChaosDurationSec}, {"gracePeriodSec", s.GracePeriodSec},
			{"networkLatencyMs", s.NetworkLatencyMs}, {"cpuCores", s.CPUCores}, {"memoryMb", s.MemoryMb},
			{"ioWorkers", s.IOWorkers}, {"steadyStateSec", s.SteadyStateSec}, {"observationWindowSec", s.ObservationWindowSec},
		}
		for _, f := range ints {
			if f.v == nil {
				continue
			}
			if b := mcpAdHocBounds[f.name]; *f.v < b.Min || *f.v > b.Max {
				return plan, mcpInvalidArgument(fmt.Sprintf("%s.%s must be from %d to %d.", at, f.name, b.Min, b.Max), strconv.Itoa(*f.v))
			}
			for typ, param := range mcpAdHocTypeParam {
				if param == f.name && s.DisruptionType != typ {
					return plan, mcpInvalidArgument(fmt.Sprintf("%s.%s applies only to %s steps.", at, f.name, typ), s.DisruptionType)
				}
			}
		}

		requireRecovery := true
		if s.RequireRecovery != nil {
			requireRecovery = *s.RequireRecovery
		}
		plan.Steps = append(plan.Steps, mcpPlanStepWire{
			Name: s.Name,
			FaultSpec: mcpFaultSpecWire{
				DisruptionType:   s.DisruptionType,
				TargetPod:        s.TargetPod,
				TargetBrokerID:   s.TargetBrokerID,
				TargetTopic:      s.TargetTopic,
				TargetPartition:  s.TargetPartition,
				ChaosDurationSec: mcpAdHocIntOr(s.ChaosDurationSec, "chaosDurationSec"),
				GracePeriodSec:   s.GracePeriodSec,
				NetworkLatencyMs: s.NetworkLatencyMs,
				CPUCores:         s.CPUCores,
				MemoryMb:         s.MemoryMb,
				IOWorkers:        s.IOWorkers,
			},
			SteadyStateSec:       mcpAdHocIntOr(s.SteadyStateSec, "steadyStateSec"),
			ObservationWindowSec: mcpAdHocIntOr(s.ObservationWindowSec, "observationWindowSec"),
			RequireRecovery:      requireRecovery,
		})
	}
	return plan, nil
}

// mcpCheckAdHocTargets checks how an ad-hoc step aims: at most one target,
// each well formed, a partition only with a topic, and no topic on a
// DNS_ERROR step.
func mcpCheckAdHocTargets(at string, s *mcpAdHocStep) error {
	targets := 0
	if s.TargetBrokerID != nil {
		targets++
	}
	if s.TargetPod != "" {
		targets++
		if !mcpChaosPodRE.MatchString(s.TargetPod) {
			return mcpInvalidArgument(at+".targetPod must be a Kubernetes pod name.", s.TargetPod)
		}
	}
	if s.TargetTopic != "" {
		targets++
		if !mcpChaosTopicRE.MatchString(s.TargetTopic) {
			return mcpInvalidArgument(at+".targetTopic must be a Kafka topic name.", s.TargetTopic)
		}
	}
	if targets > 1 {
		return mcpInvalidArgument(at+" sets more than one of targetBrokerId, targetPod and targetTopic; give at most one.", "")
	}
	// LitmusChaosProvider.java:278-282 makes a DNS_ERROR step's topic the
	// hostname whose lookups fail, so the fault would break only a hostname
	// named after the topic.
	if s.DisruptionType == "DNS_ERROR" && s.TargetTopic != "" {
		return mcpInvalidArgument(at+".targetTopic cannot aim a DNS_ERROR step: the litmus-crd provider reads it as the hostname to break. Use targetBrokerId.", s.TargetTopic)
	}
	if s.TargetPartition != nil && s.TargetTopic == "" {
		return mcpInvalidArgument(at+".targetPartition needs targetTopic.", strconv.Itoa(*s.TargetPartition))
	}
	return nil
}

func mcpAdHocIntOr(v *int, name string) int {
	if v != nil {
		return *v
	}
	return mcpAdHocBounds[name].Default
}

// mcpPlanView is the part of a plan (a playbook's, or the ad-hoc one sent)
// the preview reads to describe each step's targeting and what goes beyond
// the ad-hoc rules. The plan itself goes to the dry run as raw JSON.
type mcpPlanView struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	MaxAffectedBrokers *int   `json:"maxAffectedBrokers"`
	Steps              []struct {
		Name      string            `json:"name"`
		FaultSpec *mcpFaultSpecView `json:"faultSpec"`
	} `json:"steps"`
}

type mcpFaultSpecView struct {
	ExperimentName  string            `json:"experimentName"`
	DisruptionType  string            `json:"disruptionType"`
	TargetNamespace string            `json:"targetNamespace"`
	TargetLabel     string            `json:"targetLabel"`
	TargetPod       string            `json:"targetPod"`
	TargetAll       bool              `json:"targetAll"`
	TargetBrokerID  *int              `json:"targetBrokerId"`
	TargetTopic     string            `json:"targetTopic"`
	EnvOverrides    map[string]string `json:"envOverrides"`
	Probes          []json.RawMessage `json:"probes"`
}

type mcpPreviewOut struct {
	Source                  string                 `json:"source" jsonschema:"playbook or plan: what was previewed"`
	Playbook                string                 `json:"playbook,omitempty"`
	PlanName                string                 `json:"planName"`
	Description             mcpUntrusted           `json:"description,omitempty" jsonschema:"the playbook's description"`
	MaxAffectedBrokers      *int                   `json:"maxAffectedBrokers,omitempty" jsonschema:"the plan's limit on brokers hit; 0 or less means no limit"`
	PlanJSON                string                 `json:"planJson,omitempty" jsonschema:"ad-hoc plans only: the plan exactly as sent to the dry run, to save as a file for kates disruption run --config; kates://playbooks/{name} has a playbook's"`
	PlanSHA256              string                 `json:"planSha256" jsonschema:"SHA-256, in hex, of the plan JSON exactly as sent to the dry run"`
	WouldSucceed            bool                   `json:"wouldSucceed" jsonschema:"whether the safety guard's checks accept the plan now; they do not include the lease that refuses a plan while another disruption runs, and the caveats say what else they miss"`
	TotalBrokers            int                    `json:"totalBrokers" jsonschema:"brokers the dry run found; KRaft controllers are not counted"`
	Errors                  []mcpUntrusted         `json:"errors" jsonschema:"why the guard would refuse the plan"`
	Warnings                []mcpUntrusted         `json:"warnings"`
	Steps                   []mcpPreviewStep       `json:"steps"`
	KRaftControllers        mcpPreviewControllers  `json:"kraftControllers"`
	OutsideAgentLimits      []mcpAgentLimitFinding `json:"outsideAgentLimits" jsonschema:"what in the plan an ad-hoc plan here could not contain: a refused fault type, a step without disruptionType, targetAll, envOverrides, probes, a targetNamespace or targetLabel other than the defaults, targetTopic on a DNS_ERROR step, a maxAffectedBrokers other than 1, and experimentName on a step without disruptionType, where it picks the Litmus experiment; field plan when the plan could not be fully read, so it may hold more. Always empty for an ad-hoc plan. Read outsideAgentLimitsCount: a large result lists fewer"`
	OutsideAgentLimitsCount int                    `json:"outsideAgentLimitsCount" jsonschema:"how many findings outsideAgentLimits would list uncut; 0 only when the plan is within the ad-hoc rules"`
}

type mcpPreviewStep struct {
	Name                  string         `json:"name"`
	DisruptionType        string         `json:"disruptionType" jsonschema:"unknown when the step has none"`
	Targeting             string         `json:"targeting" jsonschema:"how the step picks its pods: named-pod, all-matching (targetAll, or any ROLLING_RESTART), partition-leader, broker-id, random, scale-down (the broker each selected node pool loses), or unknown"`
	AffectedPods          []string       `json:"affectedPods" jsonschema:"the pods the dry run says the step hits"`
	RandomPick            bool           `json:"randomPick" jsonschema:"the step picks one pod at random when it runs; affectedPods then shows the first matching broker, not the pod it will hit"`
	ResolvedLeaderID      *int           `json:"resolvedLeaderId,omitempty" jsonschema:"for a leader-aware step, the partition's leader now; the step looks it up again when it starts, and it may have moved"`
	NamedPodInTopology    *bool          `json:"namedPodInTopology,omitempty" jsonschema:"for a named-pod step, whether the pod is one the topology lists for a KafkaNodePool (<cluster>-<pool>-<node id>). The dry run counts any other named pod as a broker without checking that it exists or runs Kafka, so false means the step may hit a pod that is not a Kafka node at all. Absent for other steps and when the topology is not known"`
	KRaftControllers      []string       `json:"kraftControllers" jsonschema:"the affected pods that are KRaft controllers, dedicated or combined with a broker, at most 50. For a random pick this covers only the pod the dry run shows; mayHitController says whether the pick may be a controller"`
	KRaftControllerCount  int            `json:"kraftControllerCount" jsonschema:"how many of the affected pods are KRaft controllers"`
	MayHitController      bool           `json:"mayHitController" jsonschema:"a random pick on a cluster with KRaft controllers: the pod it picks when it runs may be a controller (the default label matches them), whatever affectedPods and kraftControllers show"`
	MayLoseQuorumMajority bool           `json:"mayLoseQuorumMajority" jsonschema:"the step takes down, restarts or isolates enough controllers at once that fewer than a majority of the voters would remain; for a random pick that may hit a controller, whether losing that one would"`
	Warnings              []mcpUntrusted `json:"warnings"`
}

type mcpPreviewControllers struct {
	Known     bool         `json:"known" jsonschema:"false when the cluster topology could not tell controllers from brokers; the steps' kraftControllers, mayHitController, mayLoseQuorumMajority and namedPodInTopology then say nothing"`
	ErrorCode mcpErrorCode `json:"errorCode,omitempty" jsonschema:"why the topology could not be read"`
	Reason    string       `json:"reason,omitempty"`
	Voters    int          `json:"voters" jsonschema:"KRaft controller pods the topology lists, dedicated or combined with a broker"`
	Touched   []string     `json:"touched" jsonschema:"controller pods some step hits, at most 50; a random pick adds one only when the dry run shows a controller"`
	// TouchedCount keeps the number when Touched is cut.
	TouchedCount int  `json:"touchedCount" jsonschema:"how many controller pods the steps hit together"`
	MayTouchMore bool `json:"mayTouchMore" jsonschema:"some random-pick step may hit a controller that touched does not name (see the steps' mayHitController)"`
}

type mcpAgentLimitFinding struct {
	Step  string `json:"step,omitempty" jsonschema:"the step; empty for the plan itself"`
	Field string `json:"field"`
	Value string `json:"value,omitempty"`
}

func mcpPreviewDisruption(ctx context.Context, call *mcpCall, in mcpPreviewIn) (mcpPreviewOut, error) {
	out, adHoc, err := mcpPreviewArgs(in)
	if err != nil {
		return out, err
	}
	r, err := mcpRunPreview(ctx, call, in, adHoc)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(r.sent)
	out.PlanSHA256 = hex.EncodeToString(sum[:])
	if in.Plan != nil {
		out.PlanJSON = string(r.sent)
	}

	// The dry run read the plan, so it is well-formed JSON; a field of a type
	// this view does not expect leaves only that field unset, and Unmarshal
	// fills in the rest. The view is kept, and outsideAgentLimits says it is
	// incomplete, so that a plan never looks within the rules because its
	// check could not run.
	var view mcpPlanView
	viewErr := json.Unmarshal(r.sent, &view)
	if viewErr != nil {
		call.deps.logger.Warn("preview_disruption: plan not fully readable for its step view", "error", viewErr)
	}
	out.PlanName = mcpSanitizeLine(view.Name, 128)
	if in.Plan == nil {
		out.Description = call.FenceN(view.Description, 500)
	}
	out.MaxAffectedBrokers = view.MaxAffectedBrokers
	out.WouldSucceed = r.dry.WouldSucceed
	out.TotalBrokers = r.dry.TotalBrokers
	out.Errors = mcpChaosFenceAll(call, r.dry.Errors, mcpPreviewMaxMessages)
	out.Warnings = mcpChaosFenceAll(call, r.dry.Warnings, mcpPreviewMaxMessages)

	ctrl := mcpKRaftControllersFrom(call, r.topo, r.topoErr)
	out.Steps, out.KRaftControllers = mcpPreviewSteps(call, r.dry.Steps, &view, &ctrl)
	findings := mcpAgentLimitFindings(&view)
	if viewErr != nil {
		findings = append([]mcpAgentLimitFinding{{Field: "plan", Value: "not fully readable"}}, findings...)
	}
	out.OutsideAgentLimitsCount = len(findings)
	out.OutsideAgentLimits = mcpCap(call, findings, mcpPreviewMaxMessages)
	mcpFitPreview(call, &out)
	return out, nil
}

// mcpPreviewArgs checks the arguments and, for an ad-hoc plan, builds the
// plan JSON to send.
func mcpPreviewArgs(in mcpPreviewIn) (mcpPreviewOut, []byte, error) {
	var out mcpPreviewOut
	switch {
	case in.Playbook == "" && in.Plan == nil:
		return out, nil, mcpInvalidArgument("Give playbook, a name from list_chaos_catalog, or plan, an ad-hoc plan.", "")
	case in.Playbook != "" && in.Plan != nil:
		return out, nil, mcpInvalidArgument("Give playbook or plan, not both.", "")
	case in.Plan == nil:
		out.Source, out.Playbook = "playbook", in.Playbook
		return out, nil, mcpValidPlaybookName("playbook", in.Playbook)
	}
	plan, err := mcpBuildAdHocPlan(in.Plan)
	if err != nil {
		return out, nil, err
	}
	out.Source = "plan"
	adHoc, err := json.Marshal(plan)
	return out, adHoc, err
}

// mcpPreviewReads is what a preview read: the plan exactly as sent, the dry
// run's answer, and the cluster topology or why it could not be read.
type mcpPreviewReads struct {
	sent    []byte
	dry     *client.DryRunResult
	topo    *client.ClusterTopology
	topoErr error
}

// mcpRunPreview fetches the playbook's plan, or checks the ad-hoc plan's
// types against the backend's, and sends the plan to the dry run; beside
// that it reads the topology. Failing to read the topology costs only the
// controller names, so its error is recorded, not returned.
func mcpRunPreview(ctx context.Context, call *mcpCall, in mcpPreviewIn, adHoc []byte) (mcpPreviewReads, error) {
	var r mcpPreviewReads
	g, gctx := errgroup.WithContext(ctx)
	call.Go(g, func() error {
		r.topo, r.topoErr = call.Client().ClusterTopology(gctx)
		return nil
	})
	call.Go(g, func() error {
		raw := adHoc
		if in.Plan == nil {
			var err error
			if raw, err = call.Client().PlaybookPlan(gctx, in.Playbook); err != nil {
				return err
			}
		} else if err := mcpCheckTypesKnown(gctx, call, in.Plan); err != nil {
			return err
		}
		// What postJSON puts on the wire is json.Marshal of the plan, which
		// compacts it and escapes <, > and &; the hash is of those bytes.
		var err error
		if r.sent, err = json.Marshal(json.RawMessage(raw)); err != nil {
			return err
		}
		r.dry, err = call.Client().RunDryRun(gctx, json.RawMessage(r.sent))
		return err
	})
	if err := g.Wait(); err != nil {
		return r, err
	}
	if r.dry == nil {
		return r, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty dry-run result.", Retryable: true}
	}
	// "unknown" is what the topology reports when Kafka does not answer
	// (ClusterTopologyService.java:397-409), not another cluster.
	if r.topoErr == nil && r.topo != nil && r.topo.Cluster != nil && r.topo.Cluster.ClusterID != "unknown" {
		if err := call.CheckCluster(r.topo.Cluster.ClusterID); err != nil {
			return r, err
		}
	}
	return r, nil
}

// mcpPreviewSteps describes each step the dry run returned, joined by
// position with the plan's own steps (the dry run walks them in order,
// DisruptionSafetyGuard.java:207-263), and gathers the controllers they hit.
func mcpPreviewSteps(call *mcpCall, dry []client.StepPreview, view *mcpPlanView, ctrl *mcpKRaftControllerIndex) ([]mcpPreviewStep, mcpPreviewControllers) {
	summary := ctrl.summary()
	touched := map[string]bool{}
	steps := make([]mcpPreviewStep, 0, len(dry))
	for i, ds := range dry {
		var fs *mcpFaultSpecView
		if i < len(view.Steps) {
			fs = view.Steps[i].FaultSpec
		}
		step, controllers := mcpPreviewStepFrom(call, ds, fs, ctrl)
		for _, c := range controllers {
			touched[c] = true
		}
		summary.MayTouchMore = summary.MayTouchMore || step.MayHitController
		if mcpInOtherNamespace(ds, fs, ctrl) {
			call.Caveat(mcpCaveatDryRunOtherNamespace)
		}
		steps = append(steps, step)
	}
	if ctrl.known {
		all := mcpChaosSortedKeys(touched)
		summary.TouchedCount = len(all)
		summary.Touched = mcpCap(call, all, mcpPreviewMaxPods)
	}
	return mcpCap(call, steps, mcpPreviewMaxSteps), summary
}

// mcpDryRunNamespaceRE finds the namespace the dry run names when a step hits
// nothing there (DisruptionSafetyGuard.java:221-225): the one it reads Kafka
// pods from, kates.chaos.kafka.namespace. The warning ends with it, after the
// step's label, so the last match is the one.
var mcpDryRunNamespaceRE = regexp.MustCompile(`no broker(?: pod)? in namespace '([^']*)'`)

// mcpInOtherNamespace reports whether a step the dry run saw hit nothing is
// in a namespace other than the one the dry run reads Kafka pods from, so
// that it hits nothing there but acts in its own namespace when it runs. A
// step that leaves targetNamespace out is in kafka, the FaultSpec default. A
// named pod is previewed whatever its namespace (DisruptionSafetyGuard.java:
// 389-395). The dry run names its namespace in its warning; when that text
// cannot be read, the topology's namespace (kates.topology.kafka-namespace,
// set apart from the dry run's but "kafka" by default for both) stands in.
func mcpInOtherNamespace(ds client.StepPreview, fs *mcpFaultSpecView, ctrl *mcpKRaftControllerIndex) bool {
	if fs == nil || fs.TargetPod != "" || len(ds.AffectedPods) > 0 {
		return false
	}
	ns := fs.TargetNamespace
	if ns == "" {
		ns = mcpDefaultFaultNamespace
	}
	for _, w := range ds.Warnings {
		if m := mcpDryRunNamespaceRE.FindAllStringSubmatch(w, -1); m != nil {
			return ns != m[len(m)-1][1]
		}
	}
	return ns != ctrl.namespace
}

// mcpFitPreview leaves detail out of a preview too large for one result. A
// playbook's size is the backend's to choose, and a preview cannot be paged:
// warnings and the description go first, then the lists are halved. The
// verdict, the errors, the counts and the first finding outside the agent
// limits stay longest: outsideAgentLimits is cut only after the steps, and
// never below one entry, so a plan outside the rules never reads as within
// them.
func mcpFitPreview(call *mcpCall, out *mcpPreviewOut) {
	mcpChaosShrinkToFit(call, out,
		func() bool {
			return mcpChaosHalveEach(out.Steps, func(s *mcpPreviewStep) *[]mcpUntrusted { return &s.Warnings }, 0)
		},
		func() bool { return mcpChaosHalve(&out.Warnings, 0) },
		func() bool {
			cleared := out.Description != ""
			out.Description = ""
			return cleared
		},
		func() bool {
			return mcpChaosHalveEach(out.Steps, func(s *mcpPreviewStep) *[]string { return &s.AffectedPods }, 1)
		},
		func() bool {
			return mcpChaosHalveEach(out.Steps, func(s *mcpPreviewStep) *[]string { return &s.KRaftControllers }, 1)
		},
		func() bool { return mcpChaosHalve(&out.KRaftControllers.Touched, 1) },
		func() bool { return mcpChaosHalve(&out.Steps, 1) },
		func() bool { return mcpChaosHalve(&out.OutsideAgentLimits, 1) },
		func() bool { return mcpChaosHalve(&out.Errors, 1) },
	)
}

// mcpCheckTypesKnown refuses an ad-hoc step whose type the backend does not
// list: a backend older than this server would reject it, or read it as
// something else.
func mcpCheckTypesKnown(ctx context.Context, call *mcpCall, plan *mcpAdHocPlan) error {
	types, err := call.Client().DisruptionTypes(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, t := range types {
		known[t.Name] = true
	}
	for i, s := range plan.Steps {
		if !known[s.DisruptionType] {
			return mcpInvalidArgument(fmt.Sprintf("plan.steps[%d].disruptionType is not a fault type this Kates backend lists.", i), s.DisruptionType)
		}
	}
	return nil
}

// mcpPreviewStepFrom describes one step, and returns every controller among
// its affected pods, which the step lists only up to a cap.
func mcpPreviewStepFrom(call *mcpCall, ds client.StepPreview, fs *mcpFaultSpecView, ctrl *mcpKRaftControllerIndex) (mcpPreviewStep, []string) {
	step := mcpPreviewStep{
		Name:             mcpSanitizeLine(ds.Name, 128),
		DisruptionType:   mcpSanitizeLine(ds.DisruptionType, 32),
		Targeting:        mcpTargetingOf(fs),
		ResolvedLeaderID: ds.ResolvedLeaderId,
		KRaftControllers: []string{},
	}
	pods := make([]string, 0, len(ds.AffectedPods))
	for _, p := range ds.AffectedPods {
		name, random := strings.CutSuffix(p, mcpRandomSelectionSuffix)
		step.RandomPick = step.RandomPick || random
		pods = append(pods, mcpSanitizeLine(name, 253))
	}
	// The dry run marks a random pick only when some pod matches; a step
	// whose leader lookup failed and that names no broker falls back to one
	// too (DisruptionSafetyGuard.java:92-111).
	noBroker := fs == nil || fs.TargetBrokerID == nil || *fs.TargetBrokerID < 0
	if step.Targeting == "random" || (step.Targeting == "partition-leader" && ds.ResolvedLeaderId == nil && noBroker) {
		step.RandomPick = true
	}
	if step.RandomPick {
		call.Caveat(mcpCaveatDryRunRandomPick)
	}
	step.AffectedPods = mcpCap(call, pods, mcpPreviewMaxPods)
	var controllers []string
	if ctrl.known {
		for _, p := range pods {
			if ctrl.isController(p) {
				controllers = append(controllers, p)
			}
		}
		step.KRaftControllerCount = len(controllers)
		step.KRaftControllers = mcpCap(call, controllers, mcpPreviewMaxPods)
		// A random pick is uniform over every pod the label matches,
		// controllers included (PodTargets.java:101-104), while the dry run
		// shows the first matching broker (DisruptionSafetyGuard.java:
		// 406-415). When some pod matched, assume the worst: it hits one
		// controller.
		hit := len(controllers)
		if step.RandomPick && ctrl.voters > 0 && len(pods) > 0 {
			step.MayHitController = true
			hit = max(hit, 1)
		}
		step.MayLoseQuorumMajority = ctrl.losesMajority(ds.DisruptionType, hit)
		if step.Targeting == "named-pod" {
			known := ctrl.isKafkaNodePod(fs.TargetPod)
			step.NamedPodInTopology = &known
		}
	}
	step.Warnings = mcpChaosFenceAll(call, ds.Warnings, mcpPreviewMaxWarnings)
	return step, controllers
}

// mcpTargetingOf names how a step picks its pods, in the precedence
// PodTargets.mode applies (chaos/PodTargets.java:40-51) after the orchestrator
// has turned a targetTopic into the partition leader's broker id
// (DisruptionOrchestrator.java:253-261). SCALE_DOWN picks node pools instead
// (DisruptionSafetyGuard.java:381-387).
func mcpTargetingOf(fs *mcpFaultSpecView) string {
	switch {
	case fs == nil:
		return "unknown"
	case fs.DisruptionType == "SCALE_DOWN":
		return "scale-down"
	case fs.TargetPod != "":
		return "named-pod"
	case fs.TargetAll || fs.DisruptionType == "ROLLING_RESTART":
		return "all-matching"
	case fs.TargetTopic != "":
		return "partition-leader"
	case fs.TargetBrokerID != nil && *fs.TargetBrokerID >= 0:
		return "broker-id"
	}
	return "random"
}

// mcpAgentLimitFindings lists what in a plan an ad-hoc plan could not hold.
// For the ad-hoc plan itself the list is empty by construction.
//
// experimentName is flagged only on a step without disruptionType, a narrower
// reading of plan §5.2 than its text ("no experimentName in the resolved
// plan"). Every playbook sets experimentName on every step
// (kates/src/main/resources/playbooks/*.yaml), so the literal rule would flag
// them all, while the Litmus provider maps every type but two to its own
// experiment and routes those two, ROLLING_RESTART and SCALE_DOWN, to the
// kubernetes provider: experimentName picks the experiment only for a step
// without a type (LitmusChaosProvider.java:25-37,60-63,188-215), and
// otherwise only names the ChaosEngine. The plan's wording is a follow-up.
func mcpAgentLimitFindings(v *mcpPlanView) []mcpAgentLimitFinding {
	findings := []mcpAgentLimitFinding{}
	add := func(step, field, value string) {
		findings = append(findings, mcpAgentLimitFinding{Step: mcpSanitizeLine(step, 128), Field: field, Value: mcpSanitizeLine(value, 253)})
	}
	switch m := v.MaxAffectedBrokers; {
	case m == nil || *m <= 0:
		add("", "maxAffectedBrokers", "no limit")
	case *m != mcpAdHocMaxAffectedBrokers:
		add("", "maxAffectedBrokers", strconv.Itoa(*m))
	}
	for _, s := range v.Steps {
		fs := s.FaultSpec
		if fs == nil {
			add(s.Name, "faultSpec", "none")
			continue
		}
		switch {
		case fs.DisruptionType == "":
			add(s.Name, "disruptionType", "none")
			if fs.ExperimentName != "" {
				add(s.Name, "experimentName", fs.ExperimentName)
			}
		case !mcpIsAdHocType(fs.DisruptionType):
			add(s.Name, "disruptionType", fs.DisruptionType)
		}
		if fs.TargetAll {
			add(s.Name, "targetAll", "true")
		}
		if fs.TargetNamespace != "" && fs.TargetNamespace != mcpDefaultFaultNamespace {
			add(s.Name, "targetNamespace", fs.TargetNamespace)
		}
		if fs.TargetLabel != "" && fs.TargetLabel != mcpDefaultFaultLabel {
			add(s.Name, "targetLabel", fs.TargetLabel)
		}
		if fs.DisruptionType == "DNS_ERROR" && fs.TargetTopic != "" {
			add(s.Name, "targetTopic", fs.TargetTopic)
		}
		// The count only: override values and probe commands are what the
		// rule keeps out, and have no place in a result.
		if n := len(fs.EnvOverrides); n > 0 {
			add(s.Name, "envOverrides", fmt.Sprintf("%d set", n))
		}
		if n := len(fs.Probes); n > 0 {
			add(s.Name, "probes", fmt.Sprintf("%d set", n))
		}
	}
	return findings
}

// mcpKRaftControllerIndex tells KRaft controllers from brokers among pod
// names, and Kafka node pods from other pods. Strimzi names a node pool's
// pods <cluster>-<pool>-<node id>, and the topology lists each pool with all
// its roles (ClusterTopologyService.java:523-595) and each pod with its pool
// (:599-687).
type mcpKRaftControllerIndex struct {
	known     bool
	code      mcpErrorCode
	reason    string
	voters    int
	prefixes  []string        // "<cluster>-<pool>-" of every pool with the controller role
	pools     []string        // "<cluster>-<pool>-" of every pool
	nodePods  map[string]bool // the pod of every node the topology lists
	namespace string          // the Kafka namespace the topology reads, "kafka" when unknown
}

func mcpKRaftControllersFrom(call *mcpCall, topo *client.ClusterTopology, err error) mcpKRaftControllerIndex {
	x := mcpKRaftControllerIndex{namespace: mcpDefaultFaultNamespace, nodePods: map[string]bool{}}
	switch {
	case err != nil:
		call.deps.logger.Warn("preview_disruption: cluster topology unavailable", "error", err)
		x.code = call.deps.classify(err).Code
		x.reason = "The cluster topology could not be read, so the controllers among the affected pods are not known."
		return x
	case topo == nil || topo.Cluster == nil || topo.Cluster.Name == "":
		x.reason = "The cluster topology does not name the Kafka cluster, so pod names cannot be matched to node pools."
		return x
	}
	if ns := topo.Cluster.Namespace; ns != "" {
		x.namespace = ns
	}
	controllerPools := map[string]bool{}
	replicas := 0
	for _, p := range topo.NodePools {
		prefix := topo.Cluster.Name + "-" + p.Name + "-"
		x.pools = append(x.pools, prefix)
		roles := strings.Split(p.Role, ",")
		for i := range roles {
			roles[i] = strings.TrimSpace(roles[i])
		}
		if slices.Contains(roles, "controller") {
			controllerPools[p.Name] = true
			x.prefixes = append(x.prefixes, prefix)
			replicas += p.Replicas
		}
	}
	if len(topo.NodePools) == 0 {
		x.reason = "The cluster topology lists no KafkaNodePools, so controllers cannot be told from brokers."
		return x
	}
	for _, n := range topo.Nodes {
		if controllerPools[n.Pool] {
			x.voters++
		}
		if n.Pool != "" {
			x.nodePods[topo.Cluster.Name+"-"+n.Pool+"-"+strconv.Itoa(n.ID)] = true
		}
	}
	if x.voters == 0 {
		x.voters = replicas
	}
	x.known = true
	return x
}

func (x *mcpKRaftControllerIndex) summary() mcpPreviewControllers {
	return mcpPreviewControllers{Known: x.known, ErrorCode: x.code, Reason: x.reason, Voters: x.voters, Touched: []string{}}
}

func (x *mcpKRaftControllerIndex) isController(pod string) bool { return mcpPodOfPool(pod, x.prefixes) }

// isKafkaNodePod reports whether pod is a node pool's pod: one the topology
// lists, or, when it lists none, one named after a pool.
func (x *mcpKRaftControllerIndex) isKafkaNodePod(pod string) bool {
	if len(x.nodePods) > 0 {
		return x.nodePods[pod]
	}
	return mcpPodOfPool(pod, x.pools)
}

// mcpPodOfPool reports whether pod is named <prefix><node id> for one of
// prefixes.
func mcpPodOfPool(pod string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if rest, ok := strings.CutPrefix(pod, prefix); ok && rest != "" && strings.Trim(rest, "0123456789") == "" {
			return true
		}
	}
	return false
}

// losesMajority reports whether a step that hits n controllers at once leaves
// fewer than a majority of the voters. Only faults that take a pod down,
// restart it or cut it off count; a rolling restart takes its pods one at a
// time (chaos/PodTargets.java:20-23).
func (x *mcpKRaftControllerIndex) losesMajority(faultType string, n int) bool {
	switch faultType {
	case "POD_KILL", "POD_DELETE", "LEADER_ELECTION", "NETWORK_PARTITION", "SCALE_DOWN", "NODE_DRAIN":
	case "ROLLING_RESTART":
		n = min(n, 1)
	default:
		return false
	}
	return x.voters > 0 && n > 0 && x.voters-n < x.voters/2+1
}

func mcpChaosFenceAll(call *mcpCall, items []string, limit int) []mcpUntrusted {
	out := make([]mcpUntrusted, 0, len(items))
	for _, s := range items {
		if f := call.FenceN(s, 400); f != "" {
			out = append(out, f)
		}
	}
	return mcpCap(call, out, limit)
}

func mcpChaosSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
