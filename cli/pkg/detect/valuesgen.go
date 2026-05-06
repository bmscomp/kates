package detect

import (
	"fmt"
	"regexp"
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
	minControllerCPU = 250  // millicores
	maxControllerCPU = 4000
	minControllerMem = 1    // GiB
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
			zoneCPU := int(float64(z.CPUAllocatable) * usable)
			zoneMem := int(float64(z.MemAllocatableGi) * usable)
			// Subtract per-zone workload pressure if available
			for _, zp := range g.Report.Workload.PerZone {
				if zp.Name == z.Name {
					zoneCPU -= zp.CPUAvailable // already net
					zoneMem -= zp.MemAvailable
				}
			}
			if zoneCPU < cb.WeakestZoneCPU {
				cb.WeakestZoneCPU = zoneCPU
				cb.WeakestZone = z.Name
			}
			if zoneMem < cb.WeakestZoneMem {
				cb.WeakestZoneMem = zoneMem
			}
		}
	}

	// Step 6: Determine replicas
	cb.ControllerReplicas = g.selectControllerReplicas()
	cb.BrokerReplicas = g.selectBrokerReplicas()

	// Step 7: Distribute across components
	totalBrokers := cb.BrokerReplicas * max(len(zones), 1)

	// Controller distribution
	if cb.ControllerReplicas > 0 {
		cb.ControllerCPU = clamp(
			int(float64(cb.KafkaCPU)*controllerCPUShare)/cb.ControllerReplicas,
			minControllerCPU, maxControllerCPU,
		)
		cb.ControllerMem = clamp(
			int(float64(cb.KafkaMem)*controllerMemShare)/cb.ControllerReplicas,
			minControllerMem, maxControllerMem,
		)
	}

	// Broker distribution
	if totalBrokers > 0 {
		cb.BrokerCPU = clamp(
			int(float64(cb.KafkaCPU)*brokerCPUShare)/totalBrokers,
			minBrokerCPU, maxBrokerCPU,
		)
		cb.BrokerMem = clamp(
			int(float64(cb.KafkaMem)*brokerMemShare)/totalBrokers,
			minBrokerMem, maxBrokerMem,
		)
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

func (g *ValuesGenerator) selectBrokerReplicas() int {
	// Kates currently provisions 3 Broker Pools (one for each availability zone).
	// To deploy a standard 3-broker topology, we set the replicas per pool to 1.
	return 1
}

func (g *ValuesGenerator) computeStorageBudget(totalBrokers, totalControllers int) (brokerSt, ctrlSt string) {
	usable := 1.0 - g.ReservePct

	// Try to derive from PV inventory
	audit := g.Report.StorageAudit
	if audit.PVCount > 0 && audit.PVTotalCapacity != "" {
		totalGi := 0
		cap := audit.PVTotalCapacity
		if strings.HasSuffix(cap, "Gi") {
			totalGi, _ = fmt.Sscan(strings.TrimSuffix(cap, "Gi"))
			// fmt.Sscan returns count not value, use Atoi
		}
		if totalGi == 0 {
			// parse manually
			fmt.Sscanf(cap, "%dGi", &totalGi)
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

	return &GeneratedValues{
		ClusterName: g.ClusterName,
		StrimziOp:   g.buildStrimziOp(),
		CRDUpgrade:  g.buildCRDUpgrade(),
		ControllerPools: g.buildControllerPools(),
		ControllerDefaults: GenControllerDefaults{
			Resources: GenResources{
				Requests: GenResourceValues{
					Memory: formatMem(cb.ControllerMem),
					CPU:    fmt.Sprintf("%dm", cb.ControllerCPU),
				},
				Limits: GenResourceValues{
					Memory: formatMem(cb.ControllerMem),
					CPU:    fmt.Sprintf("%dm", cb.ControllerCPU),
				},
			},
			TopologyTSC: GenTopologyConstraints{
				Enabled:           zoneScheduling,
				TopologyKey:       topologyKey,
				WhenUnsatisfiable: "ScheduleAnyway",
			},
			AntiAffinity: GenAntiAffinity{
				Enabled:     zoneScheduling,
				TopologyKey: "kubernetes.io/hostname",
			},
		},
		BrokerPools: g.buildBrokerPools(),
		BrokerDefaults: GenBrokerDefaults{
			Resources: GenResources{
				Requests: GenResourceValues{
					Memory: formatMem(cb.BrokerMem),
					CPU:    fmt.Sprintf("%dm", cb.BrokerCPU),
				},
				Limits: GenResourceValues{
					Memory: formatMem(cb.BrokerMem),
					CPU:    fmt.Sprintf("%dm", cb.BrokerCPU),
				},
			},
			TopologyTSC: GenTopologyConstraints{
				Enabled:           zoneScheduling,
				TopologyKey:       topologyKey,
				WhenUnsatisfiable: "ScheduleAnyway",
			},
			AntiAffinity: GenAntiAffinity{
				Enabled:     zoneScheduling,
				TopologyKey: "kubernetes.io/hostname",
			},
		},
		Kafka:       g.buildKafka(topologyKey),
		Dashboards:  g.buildDashboards(),
		PodMonitors: g.buildPodMonitors(),
		Alerts:      g.buildAlerts(),
		NetPolicies: g.buildNetworkPolicies(),
		Topics:        GenFeature{Enabled: true},
		CruiseControl: GenFeature{Enabled: true},
		KafkaExporter: GenFeature{Enabled: true},
		DrainCleaner:  GenFeature{Enabled: true},
		Rebalance:     GenFeature{Enabled: true},
		KafkaConnect:  GenFeature{Enabled: true},
		RBAC:          GenFeature{Create: true},
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
		Users: GenUsers{
			Enabled: true,
			Items: []GenUser{
				{
					Name: "kates-backend",
					Authentication: GenUserAuth{
						Type: "scram-sha-512",
					},
					Authorization: &GenUserAuthz{
						Type: "simple",
						Acls: []GenAcl{
							{
								Resource: GenAclResource{
									Type: "cluster",
								},
								Operations: []string{"All"},
							},
							{
								Resource: GenAclResource{
									Type:        "topic",
									Name:        "*",
									PatternType: "literal",
								},
								Operations: []string{"All"},
							},
							{
								Resource: GenAclResource{
									Type:        "group",
									Name:        "*",
									PatternType: "literal",
								},
								Operations: []string{"All"},
							},
						},
					},
				},
				{
					Name: "kafka-ui",
					Authentication: GenUserAuth{
						Type: "scram-sha-512",
					},
				},
			},
		},
	}
}

// ── Broker Pools ─────────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildBrokerPools() []GenBrokerPool {
	zones := g.Report.Zones
	cb := g.Cap

	switch {
	case len(zones) >= 3:
		pools := make([]GenBrokerPool, 3)
		for i := 0; i < 3; i++ {
			zoneName := zones[i].Name
			pools[i] = GenBrokerPool{
				Name:         "brokers-" + sanitizeK8sName(zoneName),
				Zone:         zoneName,
				Replicas:     cb.BrokerReplicas,
				StorageSize:  cb.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			}
		}
		return pools

	case len(zones) == 2:
		pools := make([]GenBrokerPool, 3)
		for i := 0; i < 2; i++ {
			zoneName := zones[i].Name
			pools[i] = GenBrokerPool{
				Name:         "brokers-" + sanitizeK8sName(zoneName),
				Zone:         zoneName,
				Replicas:     cb.BrokerReplicas,
				StorageSize:  cb.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			}
		}
		pools[2] = GenBrokerPool{
			Name:         "brokers-float",
			Zone:         "",
			Replicas:     cb.BrokerReplicas,
			StorageSize:  cb.BrokerStorage,
			StorageClass: g.selectDefaultSC(),
		}
		return pools

	case len(zones) == 1:
		zoneName := zones[0].Name
		return []GenBrokerPool{
			{
				Name: "brokers-" + sanitizeK8sName(zoneName), Zone: zoneName,
				Replicas: cb.BrokerReplicas, StorageSize: cb.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			},
			{
				Name: "brokers-float-2", Zone: "",
				Replicas: cb.BrokerReplicas, StorageSize: cb.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
			{
				Name: "brokers-float-3", Zone: "",
				Replicas: cb.BrokerReplicas, StorageSize: cb.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}

	default:
		return []GenBrokerPool{
			{
				Name: "brokers", Zone: "",
				Replicas: cb.BrokerReplicas * 3, StorageSize: cb.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}
	}
}

// ── Controller Pools ────────────────────────────────────────────────────────

// buildControllerPools creates one controller pool per detected zone,
// each pinned to its zone's storage class via node affinity.
// This mirrors the broker pool pattern and ensures each controller gets
// a PV provisioned in its own zone.
func (g *ValuesGenerator) buildControllerPools() []GenControllerPool {
	cb := g.Cap
	zones := g.Report.Zones

	switch {
	case len(zones) >= 3:
		pools := make([]GenControllerPool, 0, len(zones))
		for _, z := range zones {
			pools = append(pools, GenControllerPool{
				Name:         "controllers-" + sanitizeK8sName(z.Name),
				Zone:         z.Name,
				Replicas:     1,
				StorageSize:  cb.ControllerStorage,
				StorageClass: g.matchStorageClass(z.Name),
			})
		}
		return pools

	case len(zones) == 1:
		zoneName := zones[0].Name
		return []GenControllerPool{
			{
				Name: "controllers-" + sanitizeK8sName(zoneName), Zone: zoneName,
				Replicas: 1, StorageSize: cb.ControllerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			},
			{
				Name: "controllers-float-2", Zone: "",
				Replicas: 1, StorageSize: cb.ControllerStorage,
				StorageClass: g.selectDefaultSC(),
			},
			{
				Name: "controllers-float-3", Zone: "",
				Replicas: 1, StorageSize: cb.ControllerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}

	default:
		return []GenControllerPool{
			{
				Name: "controllers", Zone: "",
				Replicas: cb.ControllerReplicas, StorageSize: cb.ControllerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}
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

// ── Strimzi ──────────────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildStrimziOp() GenStrimziOp {
	return GenStrimziOp{Enabled: !g.Report.Strimzi.Running}
}

func (g *ValuesGenerator) buildCRDUpgrade() GenCRDUpgrade {
	return GenCRDUpgrade{Enabled: !g.Report.Strimzi.Running && !g.Report.Strimzi.CRDsPresent}
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

func (g *ValuesGenerator) buildDashboards() GenDashboards {
	return GenDashboards{Enabled: g.Report.Monitoring.GrafanaDeployed, Namespace: "monitoring"}
}

func (g *ValuesGenerator) buildPodMonitors() GenPodMonitors {
	pm := GenPodMonitors{Enabled: g.Report.Monitoring.PodMonitorCRD}
	if pm.Enabled && g.Report.Monitoring.ReleaseLabel != "" {
		pm.Labels = map[string]string{"release": g.Report.Monitoring.ReleaseLabel}
	}
	return pm
}

func (g *ValuesGenerator) buildAlerts() GenAlerts {
	a := GenAlerts{Enabled: g.Report.Monitoring.PodMonitorCRD && g.Report.Monitoring.PrometheusRuleCRD}
	if a.Enabled && g.Report.Monitoring.ReleaseLabel != "" {
		a.Labels = map[string]string{"release": g.Report.Monitoring.ReleaseLabel}
	}
	return a
}

// ── Network Policies ─────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildNetworkPolicies() GenNetPolicies {
	cniSupported := g.Report.Network.CNI != "" && g.Report.Network.CNI != "unknown"
	if !cniSupported {
		provider := strings.ToLower(g.Report.Provider)
		if provider == "eks" || provider == "gke" || provider == "aks" {
			cniSupported = true
		}
	}
	if !cniSupported {
		return GenNetPolicies{Enabled: false}
	}

	selector := map[string]string{
		"app.kubernetes.io/part-of": fmt.Sprintf("strimzi-%s", g.ClusterName),
	}
	return GenNetPolicies{
		Enabled: true, DefaultDeny: true, DefaultSelector: selector,
		AllowDNS: true, AllowDNSSelector: selector,
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

