package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// A lab's cutover is checked before it is applied: the mirror Ready and
// both connectors RUNNING (a source connector already STOPPED is a lab cut
// over already), every source's brokers ready (the proof produces to them),
// and the target Ready. cutover --dry-run prints the checks and what the
// cutover would change, and writes nothing.

// cutoverCheck is one pre-check of a lab's cutover.
type cutoverCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// cutoverPreChecks runs the RUNNING pre-checks of a lab's cutover. They
// only read.
func cutoverPreChecks(ctx context.Context, d *labDiscovery) []cutoverCheck {
	var checks []cutoverCheck
	add := func(name string, ok bool, detail string) {
		checks = append(checks, cutoverCheck{Name: name, OK: ok, Detail: detail})
	}
	if d.Mirror == nil {
		add("mirror", false, "no KafkaMirrorMaker2 carries the lab's label")
	} else if ms, err := readMirrorStatus(ctx, d.MirrorNamespace, d.MirrorCR); err != nil {
		add("mirror", false, fmt.Sprintf("cannot read %s: %v", d.MirrorCR, err))
	} else {
		detail := d.MirrorCR + " Ready"
		if !ms.Ready {
			detail = d.MirrorCR + " not Ready"
			if ms.Reason != "" {
				detail += " — " + ms.Reason + ": " + ms.Message
			}
		}
		add("mirror", ms.Ready, detail)
		src, ok := ms.Connector(migrate.SourceConnectorSuffix)
		switch {
		case !ok:
			add("source connector", false, "not listed on the CR")
		case src.State == migrate.StateStopped:
			add("source connector", false, "already STOPPED — the lab is cut over; kates migrate rollback reverses it")
		default:
			add("source connector", src.Running(), connectorDetail(src))
		}
		chk, ok := ms.Connector(migrate.CheckpointConnectorSuffix)
		if !ok {
			add("checkpoint connector", false, "not listed on the CR")
		} else {
			add("checkpoint connector", chk.Running(), connectorDetail(chk))
		}
	}
	if len(d.Sources) == 0 {
		add("source", false, "no source carries the lab's label")
	}
	for _, src := range d.Sources {
		name := "source"
		if len(d.Sources) > 1 && src.Alias != "" {
			name += " " + src.Alias
		}
		ok, detail := sourceRunning(ctx, src)
		add(name, ok, detail)
	}
	if d.TargetCluster == "" {
		add("target", false, "the mirror's spec names no target cluster")
	} else {
		out, err := defaultRunner.Run(ctx, "kubectl", "-n", d.TargetNamespace, "get", "kafka", d.TargetCluster, "-o", "jsonpath={.spec.kafka.version} {.status.conditions[?(@.type==\"Ready\")].status}")
		fields := strings.Fields(out)
		switch {
		case err != nil:
			add("target", false, fmt.Sprintf("cannot read Kafka %s in %s: %v", d.TargetCluster, d.TargetNamespace, err))
		case len(fields) > 1 && fields[1] == "True":
			add("target", true, fmt.Sprintf("%s in %s Ready (Kafka %s)", d.TargetCluster, d.TargetNamespace, fields[0]))
		default:
			add("target", false, fmt.Sprintf("%s in %s not Ready", d.TargetCluster, d.TargetNamespace))
		}
	}
	return checks
}

// connectorDetail renders a connector's state and its tasks'.
func connectorDetail(c migrate.ConnectorStatus) string {
	detail := c.State
	if len(c.Tasks) > 0 {
		detail += " (tasks " + strings.Join(c.Tasks, ",") + ")"
	}
	return detail
}

// sourceRunning says a source's brokers are all ready: the StatefulSet's
// ready replicas for the legacy chart, the Kafka CR's Ready condition for a
// Strimzi source.
func sourceRunning(ctx context.Context, src *labSource) (bool, string) {
	if src.Resource == nil || src.Namespace == "" {
		return false, "its workload is not found under the lab's label"
	}
	if src.Provider == kafkaversion.ProviderStrimzi {
		out, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "get", "kafka", src.Resource.Name, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
		if err != nil {
			return false, fmt.Sprintf("cannot read Kafka %s in %s: %v", src.Resource.Name, src.Namespace, err)
		}
		if strings.TrimSpace(out) != "True" {
			return false, fmt.Sprintf("Kafka %s in %s not Ready", src.Resource.Name, src.Namespace)
		}
		return true, fmt.Sprintf("Kafka %s in %s Ready", src.Resource.Name, src.Namespace)
	}
	out, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "get", "statefulset", src.Resource.Name, "-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}")
	if err != nil {
		return false, fmt.Sprintf("cannot read StatefulSet %s in %s: %v", src.Resource.Name, src.Namespace, err)
	}
	ready, want, _ := strings.Cut(strings.TrimSpace(out), "/")
	r, _ := strconv.Atoi(ready)
	w, _ := strconv.Atoi(want)
	detail := fmt.Sprintf("%s in %s: %d/%d brokers ready", src.Resource.Name, src.Namespace, r, w)
	return w > 0 && r == w, detail
}

// failedChecks lists the checks that did not pass, one line each.
func failedChecks(checks []cutoverCheck) []string {
	var out []string
	for _, c := range checks {
		if !c.OK {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}

// printCutoverChecks prints the pre-checks as a table (stderr lines under
// -o json, where the document carries them).
func printCutoverChecks(checks []cutoverCheck) {
	if outputMode == "json" {
		for _, c := range checks {
			if !c.OK {
				migrateWarn(c.Name + ": " + c.Detail)
			}
		}
		return
	}
	rows := make([][]string, 0, len(checks))
	for _, c := range checks {
		state := "ok"
		if !c.OK {
			state = "FAIL"
		}
		rows = append(rows, []string{c.Name, state, c.Detail})
	}
	output.Table([]string{"PRE-CHECK", "", "DETAIL"}, rows)
}

// cutoverPlan is what cutover --dry-run prints: the release it upgrades,
// the helm command, the values the overlay changes, the connector states
// before and after, what happens to clients, the proof it would run, and
// the pre-checks.
type cutoverPlan struct {
	Lab        string            `json:"lab"`
	DryRun     bool              `json:"dryRun"`
	Release    string            `json:"release"`
	Namespace  string            `json:"namespace"`
	Helm       []string          `json:"helm"`
	Overlay    string            `json:"overlay"`
	Values     map[string]string `json:"values"`
	Connectors map[string]string `json:"connectors"`
	Clients    []string          `json:"clients"`
	Proof      string            `json:"proof"`
	Checks     []cutoverCheck    `json:"checks"`
	Ready      bool              `json:"ready"`
}

// planCutover builds the dry-run document of a lab's cutover. It reads the
// overlay from the charts; it runs nothing.
func planCutover(r *labRun, checks []cutoverCheck) *cutoverPlan {
	l := r.lab
	p := &cutoverPlan{
		Lab: l.Name, DryRun: true, Release: l.MirrorRelease, Namespace: l.MirrorNamespace,
		Helm:    append([]string{"helm"}, helmCutoverArgs(l.MirrorRelease, l.MirrorNamespace, r.timeout)...),
		Overlay: chartRootPath(migrate.CutoverValues()),
		Connectors: map[string]string{
			migrate.SourceConnectorSuffix:     migrate.StateRunning + " → " + migrate.StateStopped + " (data replication ends)",
			migrate.CheckpointConnectorSuffix: migrate.StateRunning + " (unchanged: offset translation continues)",
		},
		Checks: checks,
		Ready:  len(failedChecks(checks)) == 0,
	}
	if raw, err := os.ReadFile(p.Overlay); err == nil {
		var doc map[string]any
		if yaml.Unmarshal(raw, &doc) == nil {
			p.Values = map[string]string{}
			flattenValues("", doc, p.Values)
		}
	}
	target := targetBootstrapFor(l.TargetCluster, l.TargetNamespace)
	p.Clients = []string{
		fmt.Sprintf("no client is repointed by kates: producers and consumers move to %s themselves", target),
		fmt.Sprintf("consumer group %s keeps its translated position on the target (the checkpoint connector keeps running)", l.ConsumerGroup),
	}
	var pods []string
	for _, src := range l.Sources() {
		pods = append(pods, clientPodName(l.Name, sourceClientSide(l, src)))
	}
	pods = append(pods, clientPodName(l.Name, "tgt"))
	p.Proof = fmt.Sprintf("client pods %s produce %d more record(s) to each source topic after the cutover settles; the target's end offsets of %s must not move. The pods are removed afterwards",
		strings.Join(pods, ", "), migrateCutoverExtra, strings.Join(l.ReplicatedTopics(), ", "))
	return p
}

// flattenValues renders a values document as dotted keys.
func flattenValues(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenValues(key, child, out)
		}
	default:
		out[prefix] = fmt.Sprint(t)
	}
}

// printCutoverPlan prints the dry-run document.
func printCutoverPlan(p *cutoverPlan) {
	if outputMode == "json" {
		output.JSON(p)
		return
	}
	output.Header("Cutover of lab " + p.Lab + " — dry run, nothing is changed")
	output.KeyValue("release", p.Release+" in "+p.Namespace)
	output.KeyValue("would run", strings.Join(p.Helm, " "))
	if len(p.Values) > 0 {
		keys := make([]string, 0, len(p.Values))
		for k := range p.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		output.SubHeader("values the overlay sets (" + p.Overlay + ")")
		for _, k := range keys {
			output.KeyValue(k, p.Values[k])
		}
	}
	output.SubHeader("connectors")
	output.KeyValue("source", p.Connectors[migrate.SourceConnectorSuffix])
	output.KeyValue("checkpoint", p.Connectors[migrate.CheckpointConnectorSuffix])
	output.SubHeader("clients")
	for _, c := range p.Clients {
		output.Hint(c)
	}
	output.SubHeader("proof")
	output.Hint(p.Proof)
	fmt.Fprintln(output.Out)
	printCutoverChecks(p.Checks)
}
