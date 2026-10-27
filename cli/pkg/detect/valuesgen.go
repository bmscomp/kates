package detect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// Component distribution percentages of total Kafka budget
	controllerCPUShare = 0.15
	controllerMemShare = 0.10
	brokerCPUShare     = 0.70
	brokerMemShare     = 0.75
	// remaining 15% CPU + 15% mem for operators, exporters, cruise control

	// Resource bounds
	minControllerCPU = 250 // millicores
	maxControllerCPU = 4000
	minControllerMem = 1 // GiB
	maxControllerMem = 4
	minBrokerCPU     = 250
	maxBrokerCPU     = 8000
	minBrokerMem     = 1
	maxBrokerMem     = 16

	// Storage bounds (GiB)
	minControllerStorage = 1
	maxControllerStorage = 50
	minBrokerStorage     = 10
	maxBrokerStorage     = 2048
)

// ValuesGenerator builds a GeneratedValues from a DetectReport.
type ValuesGenerator struct {
	Report      *DetectReport
	ClusterName string
	ReservePct  float64 // 0.0-1.0, default 0.30 = 30% reserved
	Cap         CapacityBudget
}

// NewValuesGenerator creates a generator and computes the capacity budget.
func NewValuesGenerator(report *DetectReport, clusterName string) *ValuesGenerator {
	return NewValuesGeneratorWithReserve(report, clusterName, 0.30)
}

// NewValuesGeneratorWithReserve creates a generator with a custom reserve percentage.
func NewValuesGeneratorWithReserve(report *DetectReport, clusterName string, reserve float64) *ValuesGenerator {
	g := &ValuesGenerator{
		Report:      report,
		ClusterName: clusterName,
		ReservePct:  reserve,
	}
	g.Cap = g.computeCapacityBudget()
	return g
}

// ── Capacity Budget Computation ──────────────────────────────────────────────

func (g *ValuesGenerator) computeCapacityBudget() CapacityBudget {
	cb := CapacityBudget{
		ReservePct: g.ReservePct,
	}

	// Step 1: Total allocatable capacity
	for _, n := range g.Report.Nodes {
		cb.TotalCPU += n.CPU
		cb.TotalMem += n.MemoryGi
	}

	// Step 2: Existing workload consumption
	cb.UsedCPU = g.Report.Workload.TotalCPURequests
	cb.UsedMem = g.Report.Workload.TotalMemRequests

	// Step 3: Available = total - used
	cb.AvailableCPU = cb.TotalCPU - cb.UsedCPU
	cb.AvailableMem = cb.TotalMem - cb.UsedMem
	if cb.AvailableCPU < 0 {
		cb.AvailableCPU = 0
	}
	if cb.AvailableMem < 0 {
		cb.AvailableMem = 0
	}

	// Utilization percentage
	if cb.TotalCPU > 0 {
		cb.UtilizationPct = float64(cb.UsedCPU) / float64(cb.TotalCPU)
	}

	// Step 4: Apply reserve — Kafka gets (1 - reserve) of available
	usable := 1.0 - g.ReservePct
	cb.KafkaCPU = int(float64(cb.AvailableCPU) * usable)
	cb.KafkaMem = int(float64(cb.AvailableMem) * usable)

	// Step 5: Per-zone analysis — find weakest zone
	zones := g.Report.Zones
	if len(zones) > 0 {
		cb.WeakestZoneCPU = int(^uint(0) >> 1) // MaxInt
		cb.WeakestZoneMem = int(^uint(0) >> 1)
		for _, z := range zones {
			availableCPU, availableMem := g.availableInZone(z)
			zoneCPU := int(float64(availableCPU) * usable)
			zoneMem := int(float64(availableMem) * usable)
			if zoneCPU < cb.WeakestZoneCPU {
				cb.WeakestZoneCPU = zoneCPU
				cb.WeakestZone = z.Name
			}
			if zoneMem < cb.WeakestZoneMem {
				cb.WeakestZoneMem = zoneMem
			}
			if zoneCPU > cb.StrongestZoneCPU {
				cb.StrongestZoneCPU = zoneCPU
			}
			if zoneMem > cb.StrongestZoneMem {
				cb.StrongestZoneMem = zoneMem
			}
		}
	}

	// Step 6: Determine replicas. ControllerReplicas is the number of
	// controllers the pools Generate emits run in total, which is what the
	// per-controller share below divides by.
	cb.ControllerReplicas = g.controllerCount()
	cb.BrokerReplicas = g.selectBrokerReplicas()

	// Step 7: Distribute across components
	totalBrokers := cb.BrokerReplicas * max(len(zones), 1)
	podCPUCap, podMemCap := g.schedulablePodBudget(zones, usable)

	// Controller distribution
	if cb.ControllerReplicas > 0 {
		cb.ControllerCPU = clamp(
			int(float64(cb.KafkaCPU)*controllerCPUShare)/cb.ControllerReplicas,
			minControllerCPU, maxControllerCPU,
		)
		cb.ControllerCPU = capRequest(cb.ControllerCPU, podCPUCap)
		cb.ControllerMem = clamp(
			int(float64(cb.KafkaMem)*controllerMemShare)/cb.ControllerReplicas,
			minControllerMem, maxControllerMem,
		)
		cb.ControllerMem = capRequest(cb.ControllerMem, podMemCap)
	}

	// Broker distribution
	if totalBrokers > 0 {
		cb.BrokerCPU = clamp(
			int(float64(cb.KafkaCPU)*brokerCPUShare)/totalBrokers,
			minBrokerCPU, maxBrokerCPU,
		)
		cb.BrokerCPU = capRequest(cb.BrokerCPU, podCPUCap)
		cb.BrokerMem = clamp(
			int(float64(cb.KafkaMem)*brokerMemShare)/totalBrokers,
			minBrokerMem, maxBrokerMem,
		)
		cb.BrokerMem = capRequest(cb.BrokerMem, podMemCap)
	}

	// Step 8: Storage budget
	cb.BrokerStorage, cb.ControllerStorage = g.computeStorageBudget(totalBrokers, cb.ControllerReplicas)

	// Step 9: Derive profile label
	cb.Profile = g.deriveProfile(cb)

	return cb
}

func (g *ValuesGenerator) selectControllerReplicas() int {
	cpuCores := 0
	totalMem := 0
	for _, n := range g.Report.Nodes {
		cpuCores += n.CPU
		totalMem += n.MemoryGi
	}
	cpuCores /= 1000
	if cpuCores >= 12 && totalMem >= 24 {
		return 3
	}
	return 1
}

// controllerCount is how many controllers buildControllerPools emits in all:
// one per zone with three or more zones, three (one pinned, two floating)
// with one zone, and otherwise a single pool sized by the cluster.
func (g *ValuesGenerator) controllerCount() int {
	switch n := len(g.Report.Zones); {
	case n >= 3:
		return n
	case n == 1:
		return 3
	default:
		return g.selectControllerReplicas()
	}
}

func (g *ValuesGenerator) selectBrokerReplicas() int {
	// Kates currently provisions 3 Broker Pools (one for each availability zone).
	// To deploy a standard 3-broker topology, we set the replicas per pool to 1.
	return 1
}

func (g *ValuesGenerator) availableInZone(zone ZoneInfo) (cpuMilli int, memGi int) {
	for _, pressure := range g.Report.Workload.PerZone {
		if pressure.Name == zone.Name {
			return max(pressure.CPUAvailable, 0), max(pressure.MemAvailable, 0)
		}
	}
	return max(zone.CPUAllocatable, 0), max(zone.MemAllocatableGi, 0)
}

func (g *ValuesGenerator) schedulablePodBudget(zones []ZoneInfo, usable float64) (cpuMilli int, memGi int) {
	if len(g.Report.Nodes) == 0 {
		return 0, 0
	}

	if len(zones) > 0 {
		selectedZones := zones
		if len(selectedZones) > 3 {
			selectedZones = selectedZones[:3]
		}
		cpuMilli = int(^uint(0) >> 1)
		memGi = int(^uint(0) >> 1)
		for _, zone := range selectedZones {
			bestCPU, bestMem, found := g.bestNodeBudgetInZone(zone.Name, usable)
			if !found {
				bestCPU, bestMem = g.availableInZone(zone)
				bestCPU = int(float64(bestCPU) * usable)
				bestMem = int(float64(bestMem) * usable)
			}
			if bestCPU < cpuMilli {
				cpuMilli = bestCPU
			}
			if bestMem < memGi {
				memGi = bestMem
			}
		}
		if cpuMilli != int(^uint(0)>>1) || memGi != int(^uint(0)>>1) {
			return max(cpuMilli, 0), max(memGi, 0)
		}
	}

	for _, node := range g.Report.Nodes {
		availableCPU, availableMem := g.availableOnNode(node)
		nodeCPU := int(float64(availableCPU) * usable)
		nodeMem := int(float64(availableMem) * usable)
		if nodeCPU > cpuMilli {
			cpuMilli = nodeCPU
		}
		if nodeMem > memGi {
			memGi = nodeMem
		}
	}
	return max(cpuMilli, 0), max(memGi, 0)
}

func (g *ValuesGenerator) bestNodeBudgetInZone(zone string, usable float64) (cpuMilli int, memGi int, found bool) {
	for _, node := range g.Report.Nodes {
		if node.Zone != zone {
			continue
		}
		found = true
		availableCPU, availableMem := g.availableOnNode(node)
		nodeCPU := int(float64(availableCPU) * usable)
		nodeMem := int(float64(availableMem) * usable)
		if nodeCPU > cpuMilli {
			cpuMilli = nodeCPU
		}
		if nodeMem > memGi {
			memGi = nodeMem
		}
	}
	return cpuMilli, memGi, found
}

func (g *ValuesGenerator) availableOnNode(node NodeInfo) (cpuMilli int, memGi int) {
	cpuMilli = max(node.CPU, 0)
	memGi = max(node.MemoryGi, 0)
	for _, pressure := range g.Report.Workload.PerNode {
		if pressure.Name != node.Name {
			continue
		}
		if pressure.CPUAvailable > 0 || pressure.CPURequested > 0 || pressure.CPUAllocatable > 0 {
			cpuMilli = pressure.CPUAvailable
			if cpuMilli == 0 {
				cpuMilli = node.CPU - pressure.CPURequested
			}
		}
		if pressure.MemAvailable > 0 || pressure.MemRequested > 0 || pressure.MemAllocatable > 0 {
			memGi = pressure.MemAvailable
			if memGi == 0 {
				memGi = node.MemoryGi - pressure.MemRequested
			}
		}
		return max(cpuMilli, 0), max(memGi, 0)
	}
	return cpuMilli, memGi
}

func capRequest(value, podCap int) int {
	if podCap > 0 && value > podCap {
		return podCap
	}
	return value
}

func (g *ValuesGenerator) computeStorageBudget(totalBrokers, totalControllers int) (brokerSt, ctrlSt string) {
	usable := 1.0 - g.ReservePct

	// Try to derive from PV inventory
	audit := g.Report.StorageAudit
	if audit.PVCount > 0 && audit.PVTotalCapacity != "" {
		// The collector writes the total as "<n>Gi" (getStorageAudit).
		totalGi := 0
		if digits, ok := strings.CutSuffix(audit.PVTotalCapacity, "Gi"); ok {
			if n, err := strconv.Atoi(digits); err == nil {
				totalGi = n
			}
		}
		if totalGi > 0 {
			kafkaStorageGi := int(float64(totalGi) * usable)
			if totalBrokers > 0 {
				perBroker := clamp(kafkaStorageGi*80/100/totalBrokers, minBrokerStorage, maxBrokerStorage)
				brokerSt = fmt.Sprintf("%dGi", perBroker)
			}
			if totalControllers > 0 {
				perCtrl := clamp(kafkaStorageGi*20/100/totalControllers, minControllerStorage, maxControllerStorage)
				ctrlSt = fmt.Sprintf("%dGi", perCtrl)
			}
			if brokerSt != "" && ctrlSt != "" {
				return
			}
		}
	}

	// Fallback to profile-based defaults
	profile := g.deriveProfileFromResources()
	switch profile {
	case "production":
		return "200Gi", "20Gi"
	case "staging":
		return "100Gi", "10Gi"
	case "development":
		return "50Gi", "5Gi"
	default:
		return "10Gi", "1Gi"
	}
}

func (g *ValuesGenerator) deriveProfile(cb CapacityBudget) string {
	if cb.BrokerCPU >= 1000 && cb.BrokerMem >= 4 {
		return "production"
	}
	if cb.BrokerCPU >= 500 && cb.BrokerMem >= 2 {
		return "staging"
	}
	if cb.BrokerCPU > 250 && cb.BrokerMem > 1 {
		return "development"
	}
	return "minimal"
}

func (g *ValuesGenerator) deriveProfileFromResources() string {
	totalCPU := 0
	totalMem := 0
	for _, n := range g.Report.Nodes {
		totalCPU += n.CPU
		totalMem += n.MemoryGi
	}
	cpuCores := totalCPU / 1000
	if cpuCores >= 24 && totalMem >= 48 {
		return "production"
	}
	if cpuCores >= 12 && totalMem >= 24 {
		return "staging"
	}
	if cpuCores >= 4 && totalMem >= 8 {
		return "development"
	}
	return "minimal"
}

// ── Generate ─────────────────────────────────────────────────────────────────

// Generate produces the complete GeneratedValues using capacity-aware distribution.
func (g *ValuesGenerator) Generate() *GeneratedValues {
	topologyKey := "topology.kubernetes.io/zone"
	zoneScheduling := len(g.Report.Zones) > 0
	if !zoneScheduling {
		topologyKey = "kubernetes.io/hostname"
	}

	cb := g.Cap
	sameFor := func(cpuMilli, memGi int) GenResources {
		r := GenResourceValues{Memory: formatMem(memGi), CPU: fmt.Sprintf("%dm", cpuMilli)}
		return GenResources{Requests: r, Limits: r}
	}
	scheduling := GenScheduling{
		Spread: GenSpread{
			Enabled:           zoneScheduling,
			TopologyKey:       topologyKey,
			WhenUnsatisfiable: "ScheduleAnyway",
		},
		AntiAffinity: GenAntiAffinity{
			Enabled:     zoneScheduling,
			TopologyKey: "kubernetes.io/hostname",
		},
	}
	if zoneScheduling {
		scheduling.ZoneKey = topologyKey
	}

	return &GeneratedValues{
		Global: GenGlobal{
			ClusterDomain: g.Report.Network.ClusterDomain,
			StorageClass:  g.selectDefaultSC(),
		},
		ClusterName: g.ClusterName,

		NodePools: GenNodePools{
			Defaults: GenPoolDefaults{Scheduling: scheduling},
			RoleDefaults: GenRoleDefaults{
				Controller: GenRoleDefault{Resources: sameFor(cb.ControllerCPU, cb.ControllerMem)},
				Broker:     GenRoleDefault{Resources: sameFor(cb.BrokerCPU, cb.BrokerMem)},
			},
			Pools: append(g.buildControllerPools(), g.buildBrokerPools()...),
		},
		Kafka:         g.buildKafka(topologyKey),
		Monitoring:    g.buildMonitoring(),
		Alerts:        g.buildAlerts(),
		NetworkPolicy: g.buildNetworkPolicy(),
		Topics:        GenFeature{Enabled: true},
		CruiseControl: GenFeature{Enabled: false},
		KafkaExporter: GenFeature{Enabled: false},
		Rebalance:     GenFeature{Enabled: true},
		RBAC:          GenRBAC{Create: true},
		EntityOperator: map[string]interface{}{
			"topicOperator": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"memory": "256Mi", "cpu": "100m"},
					"limits":   map[string]interface{}{"memory": "256Mi", "cpu": "500m"},
				},
				"jvmOptions": map[string]interface{}{
					"-Xms": "128m",
					"-Xmx": "256m",
				},
				"reconciliationIntervalMs": 60000,
			},
			"userOperator": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"memory": "256Mi", "cpu": "100m"},
					"limits":   map[string]interface{}{"memory": "256Mi", "cpu": "500m"},
				},
				"jvmOptions": map[string]interface{}{
					"-Xms": "128m",
					"-Xmx": "256m",
				},
			},
		},
	}
}

// ── Node pools ───────────────────────────────────────────────────────────────

// pool is one nodePools.pools entry: a zone-pinned pool takes its zone's
// storage class, a floating one (zone "") the default class.
func (g *ValuesGenerator) pool(name, role, zone string, replicas int, size string) GenPool {
	class := g.selectDefaultSC()
	if zone != "" {
		class = g.matchStorageClass(zone)
	}
	return GenPool{
		Name:     name,
		Roles:    []string{role},
		Zone:     zone,
		Replicas: replicas,
		Storage:  GenPoolStorage{Volumes: []GenVolume{{ID: 0, Size: size, Class: class}}},
	}
}

// buildBrokerPools emits three broker pools: one per zone with three or
// more zones, pinned pools plus floating ones with fewer, and a single
// three-broker pool without zones.
func (g *ValuesGenerator) buildBrokerPools() []GenPool {
	zones := g.Report.Zones
	cb := g.Cap
	brokers := func(zone, suffix string) GenPool {
		name := "brokers-" + suffix
		if zone != "" {
			name = "brokers-" + sanitizeK8sName(zone)
		}
		return g.pool(name, "broker", zone, cb.BrokerReplicas, cb.BrokerStorage)
	}

	switch {
	case len(zones) >= 3:
		pools := make([]GenPool, 3)
		for i := 0; i < 3; i++ {
			pools[i] = brokers(zones[i].Name, "")
		}
		return pools

	case len(zones) == 2:
		return []GenPool{brokers(zones[0].Name, ""), brokers(zones[1].Name, ""), brokers("", "float")}

	case len(zones) == 1:
		return []GenPool{brokers(zones[0].Name, ""), brokers("", "float-2"), brokers("", "float-3")}

	default:
		return []GenPool{g.pool("brokers", "broker", "", cb.BrokerReplicas*3, cb.BrokerStorage)}
	}
}

// buildControllerPools creates one controller pool per detected zone, each
// pinned to its zone's storage class, so every controller gets a PV
// provisioned in its own zone; the count agrees with controllerCount.
func (g *ValuesGenerator) buildControllerPools() []GenPool {
	cb := g.Cap
	zones := g.Report.Zones
	controllers := func(zone, suffix string) GenPool {
		name := "controllers-" + suffix
		if zone != "" {
			name = "controllers-" + sanitizeK8sName(zone)
		}
		return g.pool(name, "controller", zone, 1, cb.ControllerStorage)
	}

	switch {
	case len(zones) >= 3:
		pools := make([]GenPool, 0, len(zones))
		for _, z := range zones {
			pools = append(pools, controllers(z.Name, ""))
		}
		return pools

	case len(zones) == 1:
		return []GenPool{controllers(zones[0].Name, ""), controllers("", "float-2"), controllers("", "float-3")}

	default:
		return []GenPool{g.pool("controllers", "controller", "", cb.ControllerReplicas, cb.ControllerStorage)}
	}
}

// ── StorageClass Matching ────────────────────────────────────────────────────

func (g *ValuesGenerator) matchStorageClass(zone string) string {
	zoneLower := strings.ToLower(zone)
	for _, sc := range g.Report.Storage {
		if strings.Contains(strings.ToLower(sc.Name), zoneLower) {
			return sc.Name
		}
	}
	return g.selectDefaultSC()
}

func (g *ValuesGenerator) selectDefaultSC() string {
	for _, sc := range g.Report.Storage {
		if sc.IsDefault {
			return sc.Name
		}
	}

	provider := strings.ToLower(g.Report.Provider)
	for _, sc := range g.Report.Storage {
		scLower := strings.ToLower(sc.Name)
		switch provider {
		case "eks":
			if strings.Contains(scLower, "gp3") || strings.Contains(scLower, "gp2") {
				return sc.Name
			}
		case "gke":
			if strings.Contains(scLower, "standard-rwo") || strings.Contains(scLower, "premium-rwo") {
				return sc.Name
			}
		case "aks":
			if strings.Contains(scLower, "managed-csi") || strings.Contains(scLower, "managed-premium") {
				return sc.Name
			}
		case "kind":
			if strings.Contains(scLower, "local-storage") || strings.Contains(scLower, "standard") {
				return sc.Name
			}
		}
	}

	if len(g.Report.Storage) > 0 {
		return g.Report.Storage[0].Name
	}
	return "standard"
}

// ── Listeners ────────────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildKafka(topologyKey string) GenKafka {
	listeners := []GenListener{
		{
			Name: "plain", Port: 9092, Type: "internal", TLS: false,
			Authentication: &GenAuth{Type: "scram-sha-512"},
		},
		{
			Name: "tls", Port: 9093, Type: "internal", TLS: true,
			Authentication: &GenAuth{Type: "tls"},
		},
	}

	ext := g.buildExternalListener()
	if ext != nil {
		listeners = append(listeners, *ext)
	}

	return GenKafka{
		Listeners: listeners,
		Rack:      GenRack{TopologyKey: topologyKey},
	}
}

func (g *ValuesGenerator) buildExternalListener() *GenListener {
	provider := strings.ToLower(g.Report.Provider)
	switch provider {
	case "eks":
		return &GenListener{
			Name: "external", Port: 9094, Type: "loadbalancer", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
			Configuration: &GenListenerConfig{
				Bootstrap: GenBootstrapConfig{
					Annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
				},
			},
		}
	case "gke":
		return &GenListener{
			Name: "external", Port: 9094, Type: "loadbalancer", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
			Configuration: &GenListenerConfig{
				Bootstrap: GenBootstrapConfig{
					Annotations: map[string]string{"cloud.google.com/l4-rbs": "enabled"},
				},
			},
		}
	case "aks":
		return &GenListener{
			Name: "external", Port: 9094, Type: "loadbalancer", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
			Configuration: &GenListenerConfig{
				Bootstrap: GenBootstrapConfig{
					Annotations: map[string]string{"service.beta.kubernetes.io/azure-load-balancer-internal": "true"},
				},
			},
		}
	case "kind":
		return nil
	default:
		return &GenListener{
			Name: "external", Port: 9094, Type: "nodeport", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
		}
	}
}

// ── Monitoring ───────────────────────────────────────────────────────────────
//
// Scrape and rules follow the CRDs the cluster has at detection time: a
// PodMonitor on a cluster without prometheus-operator is a Helm install that
// fails, and a Kafka without one on a cluster that has it is a set of boards
// with nothing to show. Both were hard-coded false for a while, to keep a
// `kates auto` cluster light — the weight is Cruise Control and the Kafka
// Exporter (still off here), not a scrape config. `kates deploy` runs
// detection before it installs monitoring, so it does not rely on this and
// says what it wants with --set.

func (g *ValuesGenerator) buildMonitoring() GenMonitoring {
	return GenMonitoring{PodMonitor: GenPodMonitor{Enabled: g.Report.Monitoring.PodMonitorCRD}}
}

func (g *ValuesGenerator) buildAlerts() GenAlerts {
	return GenAlerts{Enabled: g.Report.Monitoring.PodMonitorCRD && g.Report.Monitoring.PrometheusRuleCRD}
}

// ── Network Policy ───────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildNetworkPolicy() GenNetworkPolicy {
	cniSupported := g.Report.Network.CNI != "" && g.Report.Network.CNI != "unknown"
	if !cniSupported {
		provider := strings.ToLower(g.Report.Provider)
		if provider == "eks" || provider == "gke" || provider == "aks" {
			cniSupported = true
		}
	}
	if !cniSupported {
		return GenNetworkPolicy{Enabled: false}
	}

	// The chart derives this selector from clusterName when it is empty; it
	// is spelled out so a Kyverno policy that refuses an empty podSelector
	// sees it in the values too.
	selector := map[string]string{
		"app.kubernetes.io/part-of": fmt.Sprintf("strimzi-%s", g.ClusterName),
	}
	return GenNetworkPolicy{
		Enabled:     true,
		DefaultDeny: GenSelectorToggle{Enabled: true, Selector: selector},
		DNS:         GenSelectorToggle{Enabled: true, Selector: selector},
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

var k8sNameRegex = regexp.MustCompile(`[^a-z0-9-]`)

func sanitizeK8sName(name string) string {
	lower := strings.ToLower(name)
	sanitized := k8sNameRegex.ReplaceAllString(lower, "-")
	sanitized = strings.Trim(sanitized, "-")
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	if sanitized == "" {
		return "default"
	}
	return sanitized
}

func clamp(val, minV, maxV int) int {
	if val < minV {
		return minV
	}
	if val > maxV {
		return maxV
	}
	return val
}

func formatMem(gi int) string {
	if gi <= 0 {
		return "512Mi"
	}
	return fmt.Sprintf("%dGi", gi)
}
