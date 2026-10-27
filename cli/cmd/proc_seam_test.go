package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/proc"
)

// stubRunner puts a scripted runner behind newKubectl and newHelm for the
// test, so a command reaches no cluster, and returns it for scripting and for
// reading back the calls it saw.
func stubRunner(t *testing.T) *proc.Fake {
	t.Helper()
	f := &proc.Fake{}
	orig := commandRunner
	commandRunner = f
	t.Cleanup(func() { commandRunner = orig })
	return f
}

func TestNewKubectlAndNewHelmRunThroughTheSeam(t *testing.T) {
	f := stubRunner(t)
	ctx := context.Background()
	_, _ = newKubectl("kafka").Run(ctx, "get", "pods")
	_, _ = newKubectl("").Run(ctx, "get", "nodes")
	_, _ = newHelm("kates").Run(ctx, "list")
	want := []string{"kubectl get pods -n kafka", "kubectl get nodes", "helm -n kates list"}
	if got := f.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// Every connect command carries the namespace the group resolved, once, and
// nothing else names it.
func TestConnectCommandsCarryTheNamespace(t *testing.T) {
	origNS := connectNamespace
	t.Cleanup(func() { connectNamespace = origNS })
	connectNamespace = "ns-x"

	f := stubRunner(t)
	f.On("kubectl get kafkaconnect -o", "my-connect")
	f.On("kubectl get kafkaconnector src -o json", `{"metadata":{"name":"src"},"status":{"connectorStatus":{"tasks":[{"id":0,"state":"RUNNING"}]}}}`)

	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"status", func() error { return connectStatusCmd.RunE(connectStatusCmd, nil) }},
		{"tasks", func() error { return connectTasksCmd.RunE(connectTasksCmd, []string{"src"}) }},
		{"restart", func() error { return connectRestartCmd.RunE(connectRestartCmd, []string{"src"}) }},
		{"pause", func() error { return connectPauseCmd.RunE(connectPauseCmd, []string{"src"}) }},
		{"delete", func() error { return connectDeleteCmd.RunE(connectDeleteCmd, []string{"src"}) }},
		{"scale", func() error { return connectScaleCmd.RunE(connectScaleCmd, []string{"3"}) }},
	} {
		if _, _, err := commandOutput(t, step.run); err != nil {
			t.Errorf("connect %s: %v", step.name, err)
		}
	}

	want := []string{
		"kubectl get kafkaconnect -n ns-x",
		"kubectl get kafkaconnector src -o json -n ns-x",
		"kubectl annotate kafkaconnector src strimzi.io/restart=true --overwrite -n ns-x",
		`kubectl patch kafkaconnector src --type=merge -p {"spec":{"state":"paused"}} -n ns-x`,
		"kubectl delete kafkaconnector src -n ns-x",
		"kubectl get kafkaconnect -o jsonpath={.items[0].metadata.name} -n ns-x",
		`kubectl patch kafkaconnect my-connect --type=merge -p {"spec":{"replicas":3}} -n ns-x`,
	}
	if got := f.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("kubectl calls:\n got %q\nwant %q", got, want)
	}
	for _, line := range f.Lines() {
		if strings.Count(line, "-n ns-x") != 1 {
			t.Errorf("%q names the namespace %d times", line, strings.Count(line, "-n ns-x"))
		}
	}
}

// A connect command's failure says what kubectl said.
func TestConnectStatusReportsKubectlsError(t *testing.T) {
	origNS := connectNamespace
	t.Cleanup(func() { connectNamespace = origNS })
	connectNamespace = "ns-x"
	stubRunner(t).Fail("kubectl", "Unable to connect to the server: dial tcp: i/o timeout")

	_, _, err := commandOutput(t, func() error { return connectStatusCmd.RunE(connectStatusCmd, nil) })
	if err == nil || !strings.Contains(err.Error(), "Unable to connect") {
		t.Errorf("err = %v, want kubectl's stderr", err)
	}
}

func TestDetectConnectNamespace(t *testing.T) {
	t.Setenv("KATES_CONNECT_NS", "")
	t.Setenv("KATES_KAFKA_NS", "")
	f := stubRunner(t).On("kubectl get kafkaconnect -A", " found-ns \n")
	if got := detectConnectNamespace(context.Background()); got != "found-ns" {
		t.Errorf("detectConnectNamespace = %q, want the namespace the cluster named", got)
	}
	if want := []string{"kubectl get kafkaconnect -A -o jsonpath={.items[0].metadata.namespace}"}; !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	stubRunner(t).Fail("kubectl", "Unable to connect to the server")
	t.Setenv("KATES_KAFKA_NS", "legacy")
	if got := detectConnectNamespace(context.Background()); got != "legacy" {
		t.Errorf("with no cluster, detectConnectNamespace = %q, want KATES_KAFKA_NS", got)
	}
	t.Setenv("KATES_KAFKA_NS", "")
	if got := detectConnectNamespace(context.Background()); got != "kafka" {
		t.Errorf("with nothing, detectConnectNamespace = %q, want kafka", got)
	}
}
