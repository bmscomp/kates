package detect

import (
	"fmt"
	"regexp"
	"strings"
)

// SizingProfile determines resource allocation based on cluster capacity.
type SizingProfile struct {
	Name              string
	BrokerReplicas    int
	ControllerReplicas int
	BrokerStorage     string
	ControllerStorage string
	BrokerMemReq      string
	BrokerCPUReq      string
}

var (
	profileProduction = SizingProfile{"production", 3, 3, "200Gi", "20Gi", "4Gi", "1000m"}
	profileStaging    = SizingProfile{"staging", 1, 3, "100Gi", "10Gi", "2Gi", "500m"}
	profileDev        = SizingProfile{"development", 1, 1, "50Gi", "5Gi", "1Gi", "500m"}
	profileMinimal    = SizingProfile{"minimal", 1, 1, "10Gi", "1Gi", "512Mi", "250m"}
)

// ValuesGenerator builds a GeneratedValues from a DetectReport.
type ValuesGenerator struct {
	Report      *DetectReport
	ClusterName string
	Profile     SizingProfile
}

// NewValuesGenerator creates a generator and selects the sizing profile.
func NewValuesGenerator(report *DetectReport, clusterName string) *ValuesGenerator {
	g := &ValuesGenerator{
		Report:      report,
		ClusterName: clusterName,
	}
	g.Profile = g.selectProfile()
	return g
}

func (g *ValuesGenerator) selectProfile() SizingProfile {
	totalCPU := 0
	totalMem := 0
	for _, n := range g.Report.Nodes {
		totalCPU += n.CPU
		totalMem += n.MemoryGi
	}
	cpuCores := totalCPU / 1000 // convert millicores to cores

	if cpuCores >= 24 && totalMem >= 48 {
		return profileProduction
	}
	if cpuCores >= 12 && totalMem >= 24 {
		return profileStaging
	}
	if cpuCores >= 4 && totalMem >= 8 {
		return profileDev
	}
	return profileMinimal
}

// Generate produces the complete GeneratedValues.
func (g *ValuesGenerator) Generate() *GeneratedValues {
	topologyKey := "topology.kubernetes.io/zone"
	zoneScheduling := len(g.Report.Zones) > 0
	if !zoneScheduling {
		topologyKey = "kubernetes.io/hostname"
	}

	return &GeneratedValues{
		ClusterName: g.ClusterName,
		StrimziOp:   g.buildStrimziOp(),
		CRDUpgrade:  g.buildCRDUpgrade(),
		Controllers: GenControllers{
			Replicas: g.Profile.ControllerReplicas,
			Storage: GenStorage{
				Size:  g.Profile.ControllerStorage,
				Class: g.selectDefaultSC(),
			},
			TopologyTSC: GenTopologyConstraints{
				Enabled:     zoneScheduling,
				TopologyKey: topologyKey,
			},
			AntiAffinity: GenAntiAffinity{
				Enabled:     zoneScheduling,
				TopologyKey: topologyKey,
			},
		},
		BrokerPools: g.buildBrokerPools(),
		BrokerDefaults: GenBrokerDefaults{
			Resources: GenResources{
				Requests: GenResourceValues{
					Memory: g.Profile.BrokerMemReq,
					CPU:    g.Profile.BrokerCPUReq,
				},
			},
			TopologyTSC: GenTopologyConstraints{
				Enabled:     zoneScheduling,
				TopologyKey: topologyKey,
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
	}
}

// ── Broker Pools ─────────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildBrokerPools() []GenBrokerPool {
	zones := g.Report.Zones

	switch {
	case len(zones) >= 3:
		// Use top 3 zones, name pools after zone names
		pools := make([]GenBrokerPool, 3)
		for i := 0; i < 3; i++ {
			zoneName := zones[i].Name
			pools[i] = GenBrokerPool{
				Name:         "brokers-" + sanitizeK8sName(zoneName),
				Zone:         zoneName,
				Replicas:     g.Profile.BrokerReplicas,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			}
		}
		return pools

	case len(zones) == 2:
		// 2 pinned + 1 floating
		pools := make([]GenBrokerPool, 3)
		for i := 0; i < 2; i++ {
			zoneName := zones[i].Name
			pools[i] = GenBrokerPool{
				Name:         "brokers-" + sanitizeK8sName(zoneName),
				Zone:         zoneName,
				Replicas:     g.Profile.BrokerReplicas,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			}
		}
		pools[2] = GenBrokerPool{
			Name:         "brokers-float",
			Zone:         "",
			Replicas:     g.Profile.BrokerReplicas,
			StorageSize:  g.Profile.BrokerStorage,
			StorageClass: g.selectDefaultSC(),
		}
		return pools

	case len(zones) == 1:
		// 1 pinned + 2 floating
		zoneName := zones[0].Name
		return []GenBrokerPool{
			{
				Name:         "brokers-" + sanitizeK8sName(zoneName),
				Zone:         zoneName,
				Replicas:     g.Profile.BrokerReplicas,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.matchStorageClass(zoneName),
			},
			{
				Name:         "brokers-float-2",
				Zone:         "",
				Replicas:     g.Profile.BrokerReplicas,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
			{
				Name:         "brokers-float-3",
				Zone:         "",
				Replicas:     g.Profile.BrokerReplicas,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}

	default:
		// No zones — single pool
		return []GenBrokerPool{
			{
				Name:         "brokers",
				Zone:         "",
				Replicas:     g.Profile.BrokerReplicas * 3,
				StorageSize:  g.Profile.BrokerStorage,
				StorageClass: g.selectDefaultSC(),
			},
		}
	}
}

// ── StorageClass Matching ────────────────────────────────────────────────────

// matchStorageClass finds a StorageClass whose name contains the zone name.
// Falls back to the cluster default SC, then to provider-specific preference.
func (g *ValuesGenerator) matchStorageClass(zone string) string {
	zoneLower := strings.ToLower(zone)

	// 1. Try exact zone-name match in SC names
	for _, sc := range g.Report.Storage {
		scLower := strings.ToLower(sc.Name)
		if strings.Contains(scLower, zoneLower) {
			return sc.Name
		}
	}

	// 2. Fall back to default SC
	return g.selectDefaultSC()
}

// selectDefaultSC picks the best default StorageClass.
func (g *ValuesGenerator) selectDefaultSC() string {
	// 1. Use the annotated default
	for _, sc := range g.Report.Storage {
		if sc.IsDefault {
			return sc.Name
		}
	}

	// 2. Provider-specific preference
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

	// 3. Use first available or fallback
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

	// Provider-specific external listener
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
					Annotations: map[string]string{
						"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
					},
				},
			},
		}
	case "gke":
		return &GenListener{
			Name: "external", Port: 9094, Type: "loadbalancer", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
			Configuration: &GenListenerConfig{
				Bootstrap: GenBootstrapConfig{
					Annotations: map[string]string{
						"cloud.google.com/l4-rbs": "enabled",
					},
				},
			},
		}
	case "aks":
		return &GenListener{
			Name: "external", Port: 9094, Type: "loadbalancer", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
			Configuration: &GenListenerConfig{
				Bootstrap: GenBootstrapConfig{
					Annotations: map[string]string{
						"service.beta.kubernetes.io/azure-load-balancer-internal": "true",
					},
				},
			},
		}
	case "kind":
		return nil // no external listener for kind
	default:
		return &GenListener{
			Name: "external", Port: 9094, Type: "nodeport", TLS: true,
			Authentication: &GenAuth{Type: "scram-sha-512"},
		}
	}
}

// ── Monitoring ───────────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildDashboards() GenDashboards {
	return GenDashboards{
		Enabled:   g.Report.Monitoring.GrafanaDeployed,
		Namespace: "monitoring",
	}
}

func (g *ValuesGenerator) buildPodMonitors() GenPodMonitors {
	pm := GenPodMonitors{Enabled: g.Report.Monitoring.PodMonitorCRD}
	if pm.Enabled && g.Report.Monitoring.ReleaseLabel != "" {
		pm.Labels = map[string]string{"release": g.Report.Monitoring.ReleaseLabel}
	}
	return pm
}

func (g *ValuesGenerator) buildAlerts() GenAlerts {
	a := GenAlerts{
		Enabled: g.Report.Monitoring.PodMonitorCRD && g.Report.Monitoring.PrometheusRuleCRD,
	}
	if a.Enabled && g.Report.Monitoring.ReleaseLabel != "" {
		a.Labels = map[string]string{"release": g.Report.Monitoring.ReleaseLabel}
	}
	return a
}

// ── Network Policies ─────────────────────────────────────────────────────────

func (g *ValuesGenerator) buildNetworkPolicies() GenNetPolicies {
	cniSupported := g.Report.Network.CNI != "" && g.Report.Network.CNI != "unknown"
	// Cloud providers always support network policies
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
		Enabled:          true,
		DefaultDeny:      true,
		DefaultSelector:  selector,
		AllowDNS:         true,
		AllowDNSSelector: selector,
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

var k8sNameRegex = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizeK8sName converts a zone name to a valid Kubernetes resource name component.
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
