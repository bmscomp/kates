package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/internal/proc"
)

// A kubectl run that exits non-zero is a probe that did not run, whatever it
// printed. Its output used to count as a result, so an image pull error was
// reported as "Could not resolve hostname".
func TestRunEphemeralPod_NonZeroExitIsAnError(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, tt := range []struct {
		name    string
		out     string
		err     error
		wantErr bool
		want    string // in the output or the error
	}{
		{"zero exit", "Server: 10.96.0.10\nDNS_OK\n", nil, false, "DNS_OK"},
		{"non-zero exit with output", "Error from server (Forbidden): pods is forbidden\n", exit, true, "pods is forbidden"},
		{"non-zero exit with a fail marker", "nslookup: can't resolve\nDNS_FAIL\n", exit, true, "DNS_FAIL"},
		{"non-zero exit without output", "", exit, true, "exit status 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stubRunner(t).Handle("kubectl", func(c proc.Call) ([]byte, error) {
				if c.Args[0] != "run" || !strings.Contains(strings.Join(c.Args, " "), "-n kates") {
					t.Errorf("kubectl %v, want run ... -n kates", c.Args)
				}
				return []byte(tt.out), tt.err
			})
			out, err := runEphemeralPod(context.Background(), "kates-doctor-test", "kates", "true")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error: %t", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, exit) || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("err = %v, want it to wrap the exit and name %q", err, tt.want)
				}
				if out != "" {
					t.Errorf("out = %q, want none on a failed run", out)
				}
			} else if !strings.Contains(out, tt.want) {
				t.Errorf("out = %q, want %q", out, tt.want)
			}
		})
	}
}

// The DNS and TCP checks report a failed run as a failed check, so one probe
// that cannot start does not end the whole doctor.
func TestRunDNSCheck_FailedRunIsAFailedCheck(t *testing.T) {
	stubRunner(t).Handle("kubectl", func(proc.Call) ([]byte, error) {
		return []byte("error: timed out waiting for the condition"), errors.New("exit status 1")
	})

	c := runDNSCheck(context.Background(), "kafka.kafka.svc.cluster.local", "kates")
	if c.Status != "fail" || !strings.Contains(c.Detail, "Pod execution failed") || !strings.Contains(c.Detail, "timed out") {
		t.Errorf("check = %+v, want a fail naming the pod failure", c)
	}
}

// Each probe gets its own deadline, so the parent's remaining time does not
// shrink it below doctorProbeTimeout and a slow probe does not use it all up.
func TestRunEphemeralPod_OwnDeadline(t *testing.T) {
	origTimeout := doctorProbeTimeout
	t.Cleanup(func() { doctorProbeTimeout = origTimeout })
	doctorProbeTimeout = 5 * time.Second

	var deadline time.Time
	stubRunner(t).Handle("kubectl", func(c proc.Call) ([]byte, error) {
		deadline, _ = c.Ctx.Deadline()
		return []byte("TCP_OK"), nil
	})
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := runEphemeralPod(parent, "p", "kates", "true"); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(deadline); left > 5*time.Second || left < 4*time.Second {
		t.Errorf("probe deadline in %s, want about %s", left, doctorProbeTimeout)
	}
}

// The kubectl run line: the probe's namespace once, before the container's
// command line.
func TestRunEphemeralPod_CommandLine(t *testing.T) {
	f := stubRunner(t).On("kubectl run", "DNS_OK")
	if _, err := runEphemeralPod(context.Background(), "pod-a", "kates", "nslookup x && echo DNS_OK || echo DNS_FAIL"); err != nil {
		t.Fatal(err)
	}
	want := `kubectl run --rm -i --restart=Never --image=busybox:1.36 pod-a -n kates -- sh -c nslookup x && echo DNS_OK || echo DNS_FAIL`
	if got := f.Lines()[0]; got != want {
		t.Errorf("kubectl run line = %q\nwant %q", got, want)
	}
}

func TestRunNetPolAudit(t *testing.T) {
	ctx := context.Background()
	stubRunner(t).
		On("kubectl get networkpolicy deny-all", `{"spec":{"podSelector":{}}}`).
		On("kubectl get networkpolicy by-name", `{"spec":{"ingress":[{"from":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kafka"}}}]}]}}`).
		On("kubectl get networkpolicy -o json -n kafka", `{"items":[{"metadata":{"name":"deny-all","namespace":"kafka"}},{"metadata":{"name":"by-name","namespace":"kafka"}}]}`).
		On("kubectl get networkpolicy -o json -n empty", `{"items":[]}`).
		Fail("kubectl", "Unable to connect to the server")

	got := runNetPolAudit(ctx, "kafka")
	if len(got) != 2 || got[0].Status != "pass" || got[0].Name != "kafka/deny-all" || got[1].Status != "warn" || !strings.Contains(got[1].Detail, "kubernetes.io/metadata.name") {
		t.Errorf("audit of kafka = %+v", got)
	}
	if got := runNetPolAudit(ctx, "empty"); len(got) != 1 || got[0].Status != "pass" || got[0].Detail != "No NetworkPolicies found" {
		t.Errorf("audit of an empty namespace = %+v", got)
	}
	if got := runNetPolAudit(ctx, "gone"); len(got) != 1 || got[0].Status != "pass" || !strings.Contains(got[0].Detail, "namespace does not exist") {
		t.Errorf("audit of an unreachable namespace = %+v", got)
	}
}

func TestDetectClusterDomain(t *testing.T) {
	ctx := context.Background()
	f := stubRunner(t).On("kubectl run", "nameserver 10.96.0.10\nsearch default.svc.cluster.example svc.cluster.example cluster.example\noptions ndots:5\n")
	if got := detectClusterDomain(ctx); got != "cluster.example" {
		t.Errorf("detectClusterDomain = %q, want cluster.example", got)
	}
	if line := f.Lines()[0]; !strings.HasPrefix(line, "kubectl run --rm -i --restart=Never --image=busybox:1.36 kates-doctor-") || !strings.HasSuffix(line, " -n default -- cat /etc/resolv.conf") {
		t.Errorf("kubectl run line = %q", line)
	}
	stubRunner(t).Fail("kubectl", "Unable to connect to the server")
	if got := detectClusterDomain(ctx); got != "cluster.local" {
		t.Errorf("with no cluster, detectClusterDomain = %q, want the default", got)
	}
}
