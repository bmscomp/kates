package cmd

import "testing"

// The probes name the services in the namespaces the flags give, in the
// cluster's DNS domain, and run from the app namespace.
func TestNetworkProbeTargets(t *testing.T) {
	origKafka, origConnect, origApp, origDb, origName := doctorKafkaNS, doctorConnectNS, doctorAppNS, doctorDbNS, deployKafkaName
	t.Cleanup(func() {
		doctorKafkaNS, doctorConnectNS, doctorAppNS, doctorDbNS, deployKafkaName = origKafka, origConnect, origApp, origDb, origName
	})
	doctorKafkaNS, doctorConnectNS, doctorAppNS, doctorDbNS, deployKafkaName = "k", "c", "a", "d", "prod"

	dns, tcp := networkProbeTargets("corp.internal")
	if len(dns) != 3 || len(tcp) != 5 {
		t.Fatalf("%d DNS and %d TCP probes, want 3 and 5", len(dns), len(tcp))
	}
	if dns[0].host != "prod-kafka-bootstrap.k.svc.corp.internal" || dns[0].desc != "Kafka Bootstrap" {
		t.Errorf("first DNS probe = %+v", dns[0])
	}
	if last := tcp[4]; last.host != "postgresql.d.svc.corp.internal" || last.port != "5432" {
		t.Errorf("last TCP probe = %+v", last)
	}
	for _, p := range append(dns, tcp...) {
		if p.ns != "a" {
			t.Errorf("probe %s runs in %q, want the app namespace", p.desc, p.ns)
		}
	}
}

func TestNetworkSummary(t *testing.T) {
	line, warnings, failed := networkSummary([]networkCheck{{Status: "pass"}, {Status: "warn"}, {Status: "fail"}, {Status: "pass"}})
	if line != "📊 Summary: 2/4 checks passed, 1 warning(s), 1 failed" || warnings != 1 || failed != 1 {
		t.Fatalf("got %q, %d, %d", line, warnings, failed)
	}
	if line, _, _ := networkSummary([]networkCheck{{Status: "pass"}}); line != "📊 Summary: 1/1 checks passed" {
		t.Fatalf("all passed: got %q", line)
	}
}
