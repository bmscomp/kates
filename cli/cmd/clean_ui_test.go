package cmd

import (
	"strings"
	"testing"
)

// --ui-ns: a Kafka UI deployed into its own namespace is uninstalled and its
// namespace deleted. clean had no such flag, so it was left behind.
func TestClean_UninstallsKafkaUIFromItsNamespace(t *testing.T) {
	f := newCleanFixture(t)
	prevUI := cleanKafkaUINS
	t.Cleanup(func() { cleanKafkaUINS = prevUI })
	cleanTopology, cleanKafkaUINS, cleanForce = "isolated", "team-ui", true
	f.helmList = `[{"name":"kafka-ui","namespace":"team-ui"}]`
	f.namespaces = "default team-ui"

	commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	if !f.ran("helm uninstall kafka-ui -n team-ui") {
		t.Errorf("did not uninstall the Kafka UI from --ui-ns:\n%s", strings.Join(f.calls, "\n"))
	}
	if !f.ran("delete namespace team-ui") {
		t.Errorf("did not delete the Kafka UI's namespace:\n%s", strings.Join(f.calls, "\n"))
	}
}
