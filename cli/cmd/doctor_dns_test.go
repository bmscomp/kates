package cmd

import (
	"context"
	"strings"
	"testing"
)

// doctor dns --fix used to patch searches: ["<kafka domain>"], which replaced
// the list and dropped every search domain the resource already had.
func TestPatchDeploymentDNS_AppendsToCurrentSearches(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current string // what kubectl prints for the jsonpath
		want    string // the searches list in the patch
	}{
		{"keeps existing entries", `["db.svc.cluster.local","corp.example.com"]`,
			`["db.svc.cluster.local","corp.example.com","kafka.svc.cluster.local"]`},
		{"no dnsConfig yet", "", `["kafka.svc.cluster.local"]`},
		{"already present", `["kafka.svc.cluster.local"]`, `["kafka.svc.cluster.local"]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := stubRunner(t).On("kubectl get deployment kates", tt.current)

			if err := patchDeploymentDNS(context.Background(), newKubectl(""), "kates", "kates", "kafka.svc.cluster.local"); err != nil {
				t.Fatal(err)
			}
			calls := f.Calls()
			if len(calls) != 2 || calls[1].Args[0] != "patch" {
				t.Fatalf("kubectl %q, want a read then a patch", f.Lines())
			}
			patch := calls[1].Args[len(calls[1].Args)-1]
			want := `{"spec":{"template":{"spec":{"dnsConfig":{"searches":` + tt.want + `}}}}}`
			if patch != want {
				t.Errorf("patch = %s\nwant    %s", patch, want)
			}
		})
	}
}

func TestPatchKafkaConnectDNS_AppendsToCurrentSearches(t *testing.T) {
	f := stubRunner(t).On("kubectl get kafkaconnect", `["schema.svc.cluster.local"]`)

	if err := patchKafkaConnectDNS(context.Background(), newKubectl(""), "connect-cluster", "connect", "kafka.svc.cluster.local"); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("kubectl %q, want a read then a patch", f.Lines())
	}
	if !strings.Contains(calls[0].String(), "spec.template.pod.dnsConfig.searches") {
		t.Errorf("read %q, want the KafkaConnect pod template's searches", calls[0])
	}
	patch := calls[1].Args[len(calls[1].Args)-1]
	want := `{"spec":{"template":{"pod":{"dnsConfig":{"searches":["schema.svc.cluster.local","kafka.svc.cluster.local"]}}}}}`
	if patch != want {
		t.Errorf("patch = %s\nwant    %s", patch, want)
	}
}

// A read that fails stops the patch: patching blind is what dropped entries.
func TestPatchDeploymentDNS_ReadFailureStopsThePatch(t *testing.T) {
	f := stubRunner(t).Fail("kubectl get", "Error from server (Forbidden): forbidden")
	err := patchDeploymentDNS(context.Background(), newKubectl(""), "kates", "kates", "kafka.svc.cluster.local")
	patched := false
	for _, c := range f.Calls() {
		if c.Args[0] == "patch" {
			patched = true
		}
	}
	if err == nil || patched {
		t.Errorf("err = %v, patched = %t; want an error and no patch", err, patched)
	}
}

// The checks report a missing domain, and treat a resource that is not there
// as nothing to fix.
func TestCheckDNS(t *testing.T) {
	ctx := context.Background()
	stubRunner(t).
		On("kubectl get deployment kates", `["kafka.svc.cluster.local"]`).
		On("kubectl get kafkaconnect connect-cluster", ``).
		Fail("kubectl get deployment missing", `Error from server (NotFound): deployments.apps "missing" not found`)
	kc := newKubectl("")

	if checkDeploymentDNS(ctx, kc, "kates", "kates", "kafka.svc.cluster.local") {
		t.Error("a deployment that has the search domain was reported as needing it")
	}
	if !checkDeploymentDNS(ctx, kc, "kates", "kates", "kafka-prod.svc.cluster.local") {
		t.Error("a deployment without the search domain was not reported")
	}
	if checkDeploymentDNS(ctx, kc, "missing", "kates", "kafka.svc.cluster.local") {
		t.Error("a deployment that does not exist was reported as needing a patch")
	}
	if !checkKafkaConnectDNS(ctx, kc, "connect-cluster", "connect", "kafka.svc.cluster.local") {
		t.Error("a KafkaConnect with no dnsConfig was not reported")
	}
}
