package cmd

import "testing"

// saveComponentFlags restores the deployWith* flags the wizard writes when
// the test ends.
func saveComponentFlags(t *testing.T) {
	t.Helper()
	strimzi, connect, ui, mm2 := deployWithStrimzi, deployWithKafkaConnect, deployWithKafkaUI, deployWithMirrorMaker2
	chaos, mon, cert, kyv := deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithKyverno
	scrape, adminless := deployAdminlessScrape, deployAdminless
	t.Cleanup(func() {
		deployWithStrimzi, deployWithKafkaConnect, deployWithKafkaUI, deployWithMirrorMaker2 = strimzi, connect, ui, mm2
		deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithKyverno = chaos, mon, cert, kyv
		deployAdminlessScrape, deployAdminless = scrape, adminless
	})
}

// The components picked in the wizard are exactly the ones on: a default
// left unticked is turned off.
func TestApplyPickedComponentsTurnsOnExactlyThePicked(t *testing.T) {
	saveComponentFlags(t)
	deployAdminless = false
	deployWithStrimzi, deployWithChaos, deployWithMonitoring, deployWithCertManager = true, true, true, true

	applyPickedComponents([]string{"kafka-ui", "kyverno", "monitoring"})

	on := map[string]bool{
		"strimzi": deployWithStrimzi, "kafka-connect": deployWithKafkaConnect, "kafka-ui": deployWithKafkaUI,
		"mirror-maker2": deployWithMirrorMaker2, "chaos": deployWithChaos, "monitoring": deployWithMonitoring,
		"cert-manager": deployWithCertManager, "kyverno": deployWithKyverno, "scrape": deployAdminlessScrape,
	}
	want := map[string]bool{"kafka-ui": true, "kyverno": true, "monitoring": true}
	for name, got := range on {
		if got != want[name] {
			t.Errorf("%s = %v, want %v", name, got, want[name])
		}
	}
}

// Admin-less, monitoring is the scrape of the cluster's Prometheus, never the
// monitoring stack.
func TestApplyPickedComponentsAdminlessMonitoringIsTheScrape(t *testing.T) {
	saveComponentFlags(t)
	deployAdminless = true

	applyPickedComponents([]string{"monitoring"})

	if deployWithMonitoring || !deployAdminlessScrape {
		t.Fatalf("monitoring=%v scrape=%v, want the scrape only", deployWithMonitoring, deployAdminlessScrape)
	}
}

// Each picked component with a namespace of its own gets an input; Kafka
// Connect gets two (Connect and its database), and admin-less monitoring
// none, since it installs into the Kafka namespace.
func TestComponentNamespaceFields(t *testing.T) {
	saveComponentFlags(t)
	for _, tc := range []struct {
		name      string
		adminless bool
		picked    []string
		want      int
	}{
		{"none", false, nil, 0},
		{"every component", false, []string{"monitoring", "chaos", "kafka-connect", "mirror-maker2", "kafka-ui", "strimzi", "kyverno"}, 6},
		{"admin-less monitoring", true, []string{"monitoring"}, 0},
		{"connect", false, []string{"kafka-connect"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deployAdminless = tc.adminless
			if got := len(componentNamespaceFields(tc.picked)); got != tc.want {
				t.Fatalf("%d namespace fields, want %d", got, tc.want)
			}
		})
	}
}
