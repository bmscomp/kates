package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A kubectl run that exits non-zero is a probe that did not run, whatever it
// printed. Its output used to count as a result, so an image pull error was
// reported as "Could not resolve hostname".
func TestRunEphemeralPod_NonZeroExitIsAnError(t *testing.T) {
	orig := runKubectlCombinedFn
	t.Cleanup(func() { runKubectlCombinedFn = orig })

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
			runKubectlCombinedFn = func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] != "run" || !strings.Contains(strings.Join(args, " "), "-n kates") {
					t.Errorf("kubectl %v, want run ... -n kates", args)
				}
				return []byte(tt.out), tt.err
			}
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
	orig := runKubectlCombinedFn
	t.Cleanup(func() { runKubectlCombinedFn = orig })
	runKubectlCombinedFn = func(context.Context, ...string) ([]byte, error) {
		return []byte("error: timed out waiting for the condition"), errors.New("exit status 1")
	}

	c := runDNSCheck(context.Background(), "kafka.kafka.svc.cluster.local", "kates")
	if c.Status != "fail" || !strings.Contains(c.Detail, "Pod execution failed") || !strings.Contains(c.Detail, "timed out") {
		t.Errorf("check = %+v, want a fail naming the pod failure", c)
	}
}

// Each probe gets its own deadline, so the parent's remaining time does not
// shrink it below doctorProbeTimeout and a slow probe does not use it all up.
func TestRunEphemeralPod_OwnDeadline(t *testing.T) {
	orig, origTimeout := runKubectlCombinedFn, doctorProbeTimeout
	t.Cleanup(func() { runKubectlCombinedFn, doctorProbeTimeout = orig, origTimeout })
	doctorProbeTimeout = 5 * time.Second

	var deadline time.Time
	runKubectlCombinedFn = func(ctx context.Context, _ ...string) ([]byte, error) {
		deadline, _ = ctx.Deadline()
		return []byte("TCP_OK"), nil
	}
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := runEphemeralPod(parent, "p", "kates", "true"); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(deadline); left > 5*time.Second || left < 4*time.Second {
		t.Errorf("probe deadline in %s, want about %s", left, doctorProbeTimeout)
	}
}
