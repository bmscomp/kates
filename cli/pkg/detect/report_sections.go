package detect

import (
	"fmt"
	"strconv"
	"strings"
)

// One builder per report section. Each reads the DetectReport and returns
// the section's content; none of them knows how it will be drawn.

func clusterSection(r *DetectReport) Section {
	provider := orUnknown(r.Provider)
	if r.ProviderBasis != "" {
		provider += " (" + r.ProviderBasis + ")"
	}
	return Section{
		ID: "cluster", Title: "Cluster Identity",
		Facts: []Fact{
			{Label: "Context", Value: r.Context},
			{Label: "Server", Value: r.Server},
			{Label: "Provider", Value: provider},
			{Label: "Kubernetes", Value: r.K8sVersion},
			{Label: "Helm", Value: r.HelmVersion},
		},
	}
}

func nodesSection(r *DetectReport) Section {
	s := Section{ID: "nodes", Title: "Nodes", Status: StatusOK}
	if len(r.Nodes) == 0 {
		s.Status = StatusFail
		s.Paragraphs = []string{"No nodes found."}
		return s
	}
	s.Paragraphs = []string{fmt.Sprintf("%d node(s).", len(r.Nodes))}
	t := Table{Headers: []string{"Name", "Zone", "Roles", "CPU", "Memory", "Runtime", "Kubelet"}}
	for _, n := range r.Nodes {
		t.Rows = append(t.Rows, cells(n.Name, n.Zone, n.Roles, fmt.Sprintf("%dm", n.CPU),
			fmt.Sprintf("%dGi", n.MemoryGi), n.Runtime, n.Kubelet))
	}
	s.Tables = []Table{t}
	return s
}

func zonesSection(r *DetectReport) Section {
	s := Section{ID: "zones", Title: "Zones", Status: okOr(len(r.Zones) >= 3, StatusFail)}
	if len(r.Zones) == 0 {
		s.Paragraphs = []string{"No zone labels found on the nodes: a 3-AZ deployment needs topology.kubernetes.io/zone."}
		return s
	}
	s.Paragraphs = []string{fmt.Sprintf("%d zone(s); a 3-AZ deployment needs at least 3.", len(r.Zones))}
	cb := r.Capacity
	t := Table{Headers: []string{"Zone", "Nodes", "CPU", "Memory"}}
	if cb.TotalCPU > 0 {
		t.Headers = append(t.Headers, "Kafka CPU", "Kafka memory")
	}
	for _, z := range r.Zones {
		row := cells(z.Name, strconv.Itoa(z.Nodes), fmt.Sprintf("%dm", z.CPUAllocatable), fmt.Sprintf("%dGi", z.MemAllocatableGi))
		if cb.TotalCPU > 0 {
			keep := 1.0 - cb.ReservePct
			row = append(row, cells(fmt.Sprintf("%dm", int(float64(z.CPUAllocatable)*keep)),
				fmt.Sprintf("%dGi", int(float64(z.MemAllocatableGi)*keep)))...)
		}
		t.Rows = append(t.Rows, row)
	}
	s.Tables = []Table{t}
	if cb.WeakestZone != "" && (cb.WeakestZoneCPU < cb.StrongestZoneCPU || cb.WeakestZoneMem < cb.StrongestZoneMem) {
		s.Notes = append(s.Notes, fmt.Sprintf("Bottleneck zone: %s (%dm CPU, %dGi memory available).",
			cb.WeakestZone, cb.WeakestZoneCPU, cb.WeakestZoneMem))
	}
	return s
}

func budgetSection(r *DetectReport) Section {
	b := r.Budget
	s := Section{ID: "budget", Title: "Resource Budget", Status: okOr(b.Sufficient, StatusFail)}
	s.Meters = []Meter{
		{Label: "CPU", Detail: fmt.Sprintf("need %dm of %dm", b.NeedCPU, b.TotalCPU), Percent: percent(b.NeedCPU, b.TotalCPU)},
		{Label: "Memory", Detail: fmt.Sprintf("need %dGi of %dGi", b.NeedMem, b.TotalMem), Percent: percent(b.NeedMem, b.TotalMem)},
	}
	// The analyzer budgets three brokers whatever the zone count; the row
	// says so rather than claiming one per zone.
	s.Tables = []Table{{
		Headers: []string{"Component", "CPU", "Memory"},
		Rows: [][]Cell{
			cells("Controllers", fmt.Sprintf("%dm", b.CtrlCPU), fmt.Sprintf("%dGi", b.CtrlMem)),
			cells("Brokers (3)", fmt.Sprintf("%dm", b.BrokerCPU*3), fmt.Sprintf("%dGi", b.BrokerMem*3)),
			cells("Operators + exporter", fmt.Sprintf("%dm", b.OtherCPU), fmt.Sprintf("%dGi", b.OtherMem)),
			cells("Total required", fmt.Sprintf("%dm", b.NeedCPU), fmt.Sprintf("%dGi", b.NeedMem)),
			cells("Cluster allocatable", fmt.Sprintf("%dm", b.TotalCPU), fmt.Sprintf("%dGi", b.TotalMem)),
		},
	}}
	sufficient := Fact{Label: "Resources", Value: "sufficient", Status: StatusOK}
	if !b.Sufficient {
		sufficient = Fact{Label: "Resources", Value: fmt.Sprintf("insufficient (need %dm CPU, %dGi memory)", b.NeedCPU, b.NeedMem), Status: StatusFail}
	}
	s.Facts = []Fact{
		sufficient,
		{Label: "Recommended profile", Value: strings.ToUpper(orUnknown(b.RecommendedProfile)), Status: okOr(b.RecommendedProfile != "insufficient", StatusFail)},
		{Label: "Profile", Value: profileDescription(b.RecommendedProfile)},
	}
	s.Facts = append(s.Facts, capacityFacts(r.Capacity)...)
	return s
}

// capacityFacts is the capacity analysis kates detect runs before it
// generates values: what is in use, and what is left for Kafka.
func capacityFacts(cb CapacityBudget) []Fact {
	if cb.TotalCPU == 0 {
		return nil
	}
	keep := int((1.0 - cb.ReservePct) * 100)
	return []Fact{
		{Label: "Cluster total", Value: fmt.Sprintf("%dm CPU, %dGi memory", cb.TotalCPU, cb.TotalMem)},
		{Label: "In use", Value: fmt.Sprintf("%dm CPU (%d%%), %dGi memory", cb.UsedCPU, int(cb.UtilizationPct*100), cb.UsedMem)},
		{Label: "Available", Value: fmt.Sprintf("%dm CPU, %dGi memory", cb.AvailableCPU, cb.AvailableMem)},
		{Label: "Kafka budget", Value: fmt.Sprintf("%dm CPU, %dGi memory (%d%% of available)", cb.KafkaCPU, cb.KafkaMem, keep)},
		{Label: "Per broker", Value: fmt.Sprintf("%dm CPU, %s memory, %s storage (%s profile)", cb.BrokerCPU, formatMem(cb.BrokerMem), cb.BrokerStorage, cb.Profile)},
	}
}

func percent(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return min(float64(part)/float64(whole)*100, 100)
}

func storageSection(r *DetectReport) Section {
	s := Section{ID: "storage", Title: "Storage", Status: okOr(len(r.Storage) > 0, StatusFail)}
	if len(r.Storage) == 0 {
		s.Paragraphs = []string{"No StorageClass found: Kafka PVCs will not bind."}
		return s
	}
	probed := false
	for _, sc := range r.Storage {
		probed = probed || sc.ProbedIOPS > 0 || sc.ProbeLatencyMs > 0
	}
	t := Table{Headers: []string{"Name", "Provisioner", "Binding", "Reclaim", "Default", "Expand"}}
	if probed {
		t.Headers = append(t.Headers, "IOPS", "Latency")
	}
	for _, sc := range r.Storage {
		row := cells(sc.Name, sc.Provisioner, sc.BindingMode, sc.ReclaimPolicy, yesNo(sc.IsDefault), yesNo(sc.AllowExpansion))
		if probed {
			iops, lat := "—", "—"
			// IOPS is measured by --bench-storage, else estimated from the
			// provisioner; the cell says which, as the IOPS check does.
			if sc.ProbedIOPS > 0 {
				iops = strconv.Itoa(sc.ProbedIOPS) + map[bool]string{true: " (measured)", false: " (estimated)"}[sc.IOPSMeasured]
			}
			if sc.ProbeLatencyMs > 0 {
				lat = fmt.Sprintf("%.2fms", sc.ProbeLatencyMs)
			}
			row = append(row, cells(iops, lat)...)
		}
		t.Rows = append(t.Rows, row)
	}
	s.Tables = []Table{t}
	a := r.StorageAudit
	if a.PVCount > 0 {
		s.Facts = append(s.Facts, Fact{Label: "PersistentVolumes", Value: fmt.Sprintf("%d total (%d bound, %s capacity)", a.PVCount, a.PVBoundCount, a.PVTotalCapacity)})
	}
	if len(a.CSIDrivers) > 0 {
		s.Facts = append(s.Facts, Fact{Label: "CSI drivers", Value: strings.Join(a.CSIDrivers, ", ")})
	}
	return s
}

func strimziSection(r *DetectReport) Section {
	st := r.Strimzi
	s := Section{ID: "strimzi", Title: "Strimzi Operator", Status: StatusWarn}
	switch {
	case st.Running:
		s.Status = okOr(st.Health.Status == "Healthy", StatusWarn)
		s.Facts = []Fact{
			{Label: "Status", Value: "running in " + st.Namespace, Status: StatusOK},
			{Label: "Image", Value: st.Image},
			{Label: "Replicas", Value: fmt.Sprintf("%d/%d ready", st.ReadyReplicas, st.TotalReplicas)},
			{Label: "Health", Value: orUnknown(st.Health.Status), Status: s.Status},
			{Label: "Operator pods", Value: map[bool]string{true: "ready", false: "not ready"}[st.Health.PodsReady], Status: okOr(st.Health.PodsReady, StatusFail)},
		}
		s.Code = st.Health.WarningLogs
	case st.CRDsPresent:
		s.Facts = []Fact{{Label: "Status", Value: "CRDs present, operator not running", Status: StatusWarn}}
	default:
		s.Facts = []Fact{{Label: "Status", Value: "not installed (kates installs the operator)", Status: StatusWarn}}
	}
	if len(st.Health.MissingCRDs) > 0 {
		s.Facts = append(s.Facts, Fact{Label: "Missing CRDs", Value: strings.Join(st.Health.MissingCRDs, ", "), Status: StatusWarn})
	}
	if st.CapacityStatus != "" {
		s.Facts = append(s.Facts, Fact{Label: "Kafka capacity", Value: st.CapacityStatus, Status: okOr(st.CapacityStatus == "Sufficient", StatusWarn)})
	}
	if len(s.Code) > 0 {
		s.Notes = []string{"Warning and error lines from the operator log in the last 15 minutes."}
	}
	return s
}

func kafkaSection(r *DetectReport) Section {
	k := r.ExistingKafka
	s := Section{ID: "kafka", Title: "Existing Kafka", Status: StatusOK}
	s.Facts = []Fact{
		{Label: "Kafka clusters", Value: strconv.Itoa(k.KafkaClusters)},
		{Label: "KafkaNodePools", Value: strconv.Itoa(k.KafkaNodePools)},
		{Label: "KafkaTopics", Value: strconv.Itoa(k.KafkaTopics)},
		{Label: "KafkaUsers", Value: strconv.Itoa(k.KafkaUsers)},
		{Label: "PVCs", Value: fmt.Sprintf("%d (%d bound)", k.PVCs, k.BoundPVCs)},
		{Label: "Helm release", Value: orNone(k.HelmRelease)},
	}
	if k.KafkaClusters == 0 {
		s.Paragraphs = []string{"No existing Kafka deployment: clean install."}
		return s
	}
	s.Status = StatusWarn
	s.Paragraphs = []string{"An existing Kafka deployment was found: upgrade mode is recommended."}
	h := k.Health
	if h.Name == "" {
		return s
	}
	s.Facts = append(s.Facts,
		Fact{Label: "Cluster", Value: fmt.Sprintf("%s (Kafka %s)", h.Name, orUnknown(h.Version))},
		Fact{Label: "Replicas", Value: fmt.Sprintf("%d/%d ready", h.ReadyReplicas, h.Replicas), Status: okOr(h.ReadyReplicas >= h.Replicas, StatusWarn)})
	for _, c := range h.Conditions {
		if c.Type == "Ready" {
			s.Facts = append(s.Facts, Fact{Label: "Ready condition", Value: strings.TrimSpace(c.Status + " " + c.Reason), Status: okOr(c.Status == "True", StatusFail)})
		}
	}
	if len(h.Listeners) > 0 {
		t := Table{Headers: []string{"Listener", "Type", "Port", "TLS"}}
		for _, l := range h.Listeners {
			t.Rows = append(t.Rows, cells(l.Name, l.Type, strconv.Itoa(l.Port), yesNo(l.TLS)))
		}
		s.Tables = []Table{t}
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func ecosystemSection(r *DetectReport) Section {
	e := r.Ecosystem
	s := Section{ID: "ecosystem", Title: "Kafka Connect and CDC", Status: StatusNone}
	s.Facts = connectFacts(e.KafkaConnect)
	if kc := e.KafkaConnect; kc.Installed && len(kc.Connectors) > 0 {
		t := Table{Headers: []string{"Connector", "Class", "Tasks max", "Status"}}
		for _, c := range kc.Connectors {
			t.Rows = append(t.Rows, []Cell{cell(c.Name), cell(c.Class), cell(strconv.Itoa(c.TasksMax)),
				{Text: c.Status, Status: okOr(strings.EqualFold(c.Status, "running"), StatusWarn)}})
		}
		s.Tables = []Table{t}
	}
	sr := e.SchemaRegistry
	switch {
	case sr.Installed && sr.Available:
		s.Facts = append(s.Facts, Fact{Label: "Schema Registry", Value: fmt.Sprintf("available (%s in %s)", sr.Name, sr.Namespace), Status: StatusOK})
	case sr.Installed:
		s.Facts = append(s.Facts, Fact{Label: "Schema Registry", Value: fmt.Sprintf("deployed but not ready (%s)", sr.Name), Status: StatusFail})
	default:
		s.Facts = append(s.Facts, Fact{Label: "Schema Registry", Value: "not installed"})
	}
	db := e.Database
	switch {
	case db.Installed && db.Accessible:
		s.Facts = append(s.Facts, Fact{Label: "CDC database", Value: fmt.Sprintf("accessible (%s in %s, port %d)", db.Name, db.Namespace, db.Port), Status: StatusOK})
	case db.Installed:
		s.Facts = append(s.Facts, Fact{Label: "CDC database", Value: fmt.Sprintf("deployed but not ready (%s)", db.Name), Status: StatusFail})
	default:
		s.Facts = append(s.Facts, Fact{Label: "CDC database", Value: "not detected"})
	}
	for _, f := range s.Facts {
		s.Status = worse(s.Status, f.Status)
	}
	return s
}

// connectFacts describes the Kafka Connect cluster, its JVM heap against
// its memory limit included.
func connectFacts(kc KafkaConnectStatus) []Fact {
	if !kc.Installed {
		return []Fact{{Label: "Kafka Connect", Value: "not installed"}}
	}
	facts := []Fact{
		{Label: "Kafka Connect", Value: fmt.Sprintf("%s in %s", kc.Name, kc.Namespace), Status: StatusOK},
		{Label: "Image", Value: kc.Image},
		{Label: "Workers", Value: fmt.Sprintf("%d/%d ready", kc.ReadyReplicas, kc.TotalReplicas), Status: okOr(kc.ReadyReplicas >= kc.TotalReplicas, StatusWarn)},
	}
	if kc.HeapXmx != "" {
		heap := Fact{Label: "JVM heap", Value: fmt.Sprintf("-Xms%s -Xmx%s", kc.HeapXms, kc.HeapXmx)}
		if kc.MemLimitMi > 0 {
			ratio := float64(parseJVMHeapMiB(kc.HeapXmx)) / float64(kc.MemLimitMi) * 100
			heap.Value += fmt.Sprintf(" (%.0f%% of the %dMi limit)", ratio, kc.MemLimitMi)
			heap.Status = StatusOK
			if ratio > 75 {
				heap.Status = StatusFail
			} else if ratio > 50 {
				heap.Status = StatusWarn
			}
		}
		facts = append(facts, heap)
	}
	facts = append(facts,
		Fact{Label: "TLS", Value: map[bool]string{true: "enabled", false: "disabled: Connect to Kafka traffic is plaintext"}[kc.TLSEnabled], Status: okOr(kc.TLSEnabled, StatusWarn)},
		Fact{Label: "PodDisruptionBudget", Value: map[bool]string{true: "configured", false: "not configured: upgrades may cause downtime"}[kc.PDBConfigured], Status: okOr(kc.PDBConfigured, StatusWarn)},
		Fact{Label: "Plugins", Value: strconv.Itoa(len(kc.Plugins))},
		Fact{Label: "Tracing", Value: yesNo(kc.TracingEnabled)},
		Fact{Label: "Rack awareness", Value: yesNo(kc.RackAware)},
		Fact{Label: "Strimzi build", Value: yesNo(kc.HasBuild)},
	)
	return facts
}

func monitoringSection(r *DetectReport) Section {
	m := r.Monitoring
	present := func(b bool) string { return map[bool]string{true: "present", false: "not found"}[b] }
	s := Section{ID: "monitoring", Title: "Monitoring", Facts: []Fact{
		{Label: "PodMonitor CRD", Value: present(m.PodMonitorCRD), Status: okOr(m.PodMonitorCRD, StatusWarn)},
		{Label: "PrometheusRule CRD", Value: present(m.PrometheusRuleCRD), Status: okOr(m.PrometheusRuleCRD, StatusWarn)},
		{Label: "Grafana", Value: present(m.GrafanaDeployed), Status: okOr(m.GrafanaDeployed, StatusWarn)},
		{Label: "Release label", Value: orNone(m.ReleaseLabel)},
	}}
	s.Status = okOr(m.PodMonitorCRD && m.PrometheusRuleCRD, StatusWarn)
	return s
}

func networkSection(r *DetectReport) Section {
	n := r.Network
	s := Section{ID: "network", Title: "Network and DNS", Status: okOr(n.CoreDNSRunning > 0, StatusFail)}
	coredns := Fact{Label: "CoreDNS", Value: fmt.Sprintf("%d replica(s) running", n.CoreDNSRunning), Status: StatusOK}
	if n.CoreDNSRunning == 0 {
		coredns = Fact{Label: "CoreDNS", Value: "not detected", Status: StatusFail}
	}
	s.Facts = []Fact{
		{Label: "CNI", Value: orUnknown(n.CNI)},
		coredns,
		{Label: "Cluster domain", Value: orUnknown(n.ClusterDomain)},
		{Label: "Pod CIDR", Value: orUnknown(n.PodCIDR)},
		{Label: "Service CIDR", Value: orUnknown(n.ServiceCIDR)},
	}
	if len(n.DNSResults) == 0 {
		s.Notes = []string{"DNS probe skipped: run with --bench-dns to measure CoreDNS latency."}
		return s
	}
	t := Table{Headers: []string{"Query", "Queries", "Succeeded", "Success rate", "Avg latency", "Max latency"}}
	for _, d := range n.DNSResults {
		t.Rows = append(t.Rows, []Cell{
			cell(d.QueryType), cell(strconv.Itoa(d.QueriesRun)), cell(strconv.Itoa(d.SuccessCount)),
			{Text: fmt.Sprintf("%.1f%%", d.SuccessRate), Status: okOr(d.SuccessRate >= 99, StatusWarn)},
			cell(fmt.Sprintf("%.2fms", d.AvgLatencyMs)), cell(fmt.Sprintf("%.2fms", d.MaxLatencyMs)),
		})
	}
	s.Tables = []Table{t}
	return s
}

// latencySection is the inter-zone ping matrix the collector measured. A run
// that measured none (read-only, one zone, probe failed) says so instead of
// showing numbers.
func latencySection(r *DetectReport) Section {
	s := Section{ID: "latency", Title: "Inter-Zone Latency"}
	if len(r.Network.LatencyMatrix) == 0 {
		s.Status = StatusSkipped
		s.Paragraphs = []string{"Skipped: the inter-zone latency probe did not run (read-only run, or no zones to probe)."}
		return s
	}
	s.Status = StatusOK
	pairs := map[[2]string]Cell{}
	for _, l := range r.Network.LatencyMatrix {
		c := Cell{Text: "FAIL", Status: StatusFail}
		if l.Success {
			c = Cell{Text: fmt.Sprintf("%.2fms", l.AvgMs), Status: latencyStatus(l)}
		}
		// A failed probe is a warning, as the analyzer counts it; a slow
		// cross-zone route is graded by its latency.
		if l.SourceZone != l.TargetZone {
			s.Status = worse(s.Status, map[bool]Status{true: c.Status, false: StatusWarn}[l.Success])
		}
		pairs[[2]string{l.SourceZone, l.TargetZone}] = c
	}
	s.Tables = []Table{zoneMatrix(pairs, false)}
	s.Notes = []string{"Average round trip of 5 pings; cross-zone above 5ms is a warning, above 15ms fails."}
	return s
}

// latencyStatus grades one measured pair by the analyzer's thresholds.
func latencyStatus(l LatencyResult) Status {
	switch {
	case l.SourceZone == l.TargetZone:
		return StatusNone
	case l.AvgMs > 15:
		return StatusFail
	case l.AvgMs > 5:
		return StatusWarn
	}
	return StatusOK
}

func bandwidthSection(r *DetectReport) Section {
	s := Section{ID: "bandwidth", Title: "Inter-Zone Bandwidth"}
	if len(r.Network.BandwidthMatrix) == 0 {
		s.Status = StatusSkipped
		s.Paragraphs = []string{"Skipped: run with --bench-network to measure throughput between zones."}
		return s
	}
	s.Status = StatusOK
	pairs := map[[2]string]Cell{}
	for _, b := range r.Network.BandwidthMatrix {
		c := Cell{Text: "FAIL", Status: StatusFail}
		if b.Success {
			c = cell(fmt.Sprintf("%.1f Mbps", b.BandwidthMbps))
		} else {
			s.Status = StatusWarn
		}
		pairs[[2]string{b.SourceZone, b.TargetZone}] = c
	}
	s.Tables = []Table{zoneMatrix(pairs, true)}
	return s
}
