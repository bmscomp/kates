package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/kubectl"
)

// doctor dns --fix used to patch searches: ["<kafka domain>"], which replaced
// the list and dropped every search domain the resource already had.
func TestPatchDeploymentDNS_AppendsToCurrentSearches(t *testing.T) {
	origOut, origRun := dnsKubectlOutputFn, dnsKubectlRunFn
	t.Cleanup(func() { dnsKubectlOutputFn, dnsKubectlRunFn = origOut, origRun })

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
			var gotArgs []string
			dnsKubectlOutputFn = func(_ context.Context, _ *kubectl.Client, args ...string) ([]byte, error) {
				if args[0] != "get" || args[1] != "deployment" || args[2] != "kates" {
					t.Errorf("read %v, want get deployment kates", args)
				}
				return []byte(tt.current), nil
			}
			dnsKubectlRunFn = func(_ context.Context, _ *kubectl.Client, args ...string) (string, error) {
				gotArgs = args
				return "", nil
			}

			if err := patchDeploymentDNS(context.Background(), kubectl.New(""), "kates", "kates", "kafka.svc.cluster.local"); err != nil {
				t.Fatal(err)
			}
			if len(gotArgs) == 0 || gotArgs[0] != "patch" {
				t.Fatalf("kubectl %v, want a patch", gotArgs)
			}
			patch := gotArgs[len(gotArgs)-1]
			want := `{"spec":{"template":{"spec":{"dnsConfig":{"searches":` + tt.want + `}}}}}`
			if patch != want {
				t.Errorf("patch = %s\nwant    %s", patch, want)
			}
		})
	}
}

func TestPatchKafkaConnectDNS_AppendsToCurrentSearches(t *testing.T) {
	origOut, origRun := dnsKubectlOutputFn, dnsKubectlRunFn
	t.Cleanup(func() { dnsKubectlOutputFn, dnsKubectlRunFn = origOut, origRun })

	dnsKubectlOutputFn = func(_ context.Context, _ *kubectl.Client, args ...string) ([]byte, error) {
		if !strings.Contains(strings.Join(args, " "), "spec.template.pod.dnsConfig.searches") {
			t.Errorf("read %v, want the KafkaConnect pod template's searches", args)
		}
		return []byte(`["schema.svc.cluster.local"]`), nil
	}
	var patch string
	dnsKubectlRunFn = func(_ context.Context, _ *kubectl.Client, args ...string) (string, error) {
		patch = args[len(args)-1]
		return "", nil
	}

	if err := patchKafkaConnectDNS(context.Background(), kubectl.New(""), "connect-cluster", "connect", "kafka.svc.cluster.local"); err != nil {
		t.Fatal(err)
	}
	want := `{"spec":{"template":{"pod":{"dnsConfig":{"searches":["schema.svc.cluster.local","kafka.svc.cluster.local"]}}}}}`
	if patch != want {
		t.Errorf("patch = %s\nwant    %s", patch, want)
	}
}

// A read that fails stops the patch: patching blind is what dropped entries.
func TestPatchDeploymentDNS_ReadFailureStopsThePatch(t *testing.T) {
	origOut, origRun := dnsKubectlOutputFn, dnsKubectlRunFn
	t.Cleanup(func() { dnsKubectlOutputFn, dnsKubectlRunFn = origOut, origRun })

	dnsKubectlOutputFn = func(context.Context, *kubectl.Client, ...string) ([]byte, error) {
		return nil, errors.New("forbidden")
	}
	patched := false
	dnsKubectlRunFn = func(context.Context, *kubectl.Client, ...string) (string, error) {
		patched = true
		return "", nil
	}
	err := patchDeploymentDNS(context.Background(), kubectl.New(""), "kates", "kates", "kafka.svc.cluster.local")
	if err == nil || patched {
		t.Errorf("err = %v, patched = %t; want an error and no patch", err, patched)
	}
}
