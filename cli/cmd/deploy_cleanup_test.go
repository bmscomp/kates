package cmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// A clusterrole/kates owned by another namespace is deleted only when the
// release named in its annotations is gone. It used to go whenever the
// namespace differed, which took a second tenant's ClusterRole with it.
func TestCleanupStaleClusterResource_DeletesOnlyWhenTheOwningReleaseIsGone(t *testing.T) {
	origOut, origComb, prevDL := runExecOutputFn, runExecCombinedFn, dl
	t.Cleanup(func() { runExecOutputFn, runExecCombinedFn, dl = origOut, origComb, prevDL })
	dl = nil // no dashboard: notes go to stdout

	for _, tc := range []struct {
		name       string
		annotation string
		helmOut    string
		helmErr    error
		wantDelete bool
		wantSay    string
	}{
		{"release gone", "old-ns\tkates", "Error: release: not found", errors.New("exit status 1"), true, "release kates is gone"},
		{"release live", "tenant-b\tkates", "NAME: kates\nSTATUS: deployed", nil, false, "release kates in \"tenant-b\" still owns it"},
		{"cannot check", "tenant-b\tkates", "Error: Kubernetes cluster unreachable", errors.New("exit status 1"), false, "could not be checked"},
		{"ours already", "new-ns\tkates", "", nil, false, ""},
		{"no owner", "", "", nil, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				return []byte(tc.annotation), nil
			}
			runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if name == "helm" {
					return []byte(tc.helmOut), tc.helmErr
				}
				return nil, nil
			}
			out := captureStdout(t, func() { cleanupStaleClusterResource(context.Background(), "clusterrole", "kates", "new-ns") })

			deleted := slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "kubectl delete clusterrole kates") })
			if deleted != tc.wantDelete {
				t.Errorf("deleted=%v, want %v; calls %q", deleted, tc.wantDelete, calls)
			}
			if tc.wantSay == "" {
				if len(calls) != 0 || out != "" {
					t.Errorf("nothing to do, yet calls %q and output %q", calls, out)
				}
				return
			}
			if !strings.Contains(out, tc.wantSay) {
				t.Errorf("output %q does not say %q", out, tc.wantSay)
			}
			if asked := slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "helm status kates -n ") }); !asked {
				t.Errorf("the owning release was not checked: %q", calls)
			}
		})
	}
}
