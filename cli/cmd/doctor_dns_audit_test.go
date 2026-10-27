package cmd

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bmscomp/kates/cli/internal/kubectl"
)

func TestParseResolvConf(t *testing.T) {
	searches, ndots := parseResolvConf("nameserver 10.96.0.10\nsearch default.svc.cluster.local svc.cluster.local cluster.local\noptions ndots:5 timeout:1\n")
	if want := []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"}; !slices.Equal(searches, want) {
		t.Errorf("searches = %q, want %q", searches, want)
	}
	if ndots != "5" {
		t.Errorf("ndots = %q, want 5", ndots)
	}
	if searches, ndots := parseResolvConf("nameserver 1.1.1.1\n"); searches != nil || ndots != "" {
		t.Errorf("no search or options line: got %q, %q", searches, ndots)
	}
}

// The cluster domain is the search domain that is not a svc domain, and
// cluster.local when there is none.
func TestClusterDomainFromSearches(t *testing.T) {
	for _, tc := range []struct {
		searches []string
		want     string
	}{
		{[]string{"default.svc.corp.internal", "svc.corp.internal", "corp.internal"}, "corp.internal"},
		{[]string{"default.svc.cluster.local", "default"}, "cluster.local"},
		{nil, "cluster.local"},
	} {
		if got := clusterDomainFromSearches(tc.searches); got != tc.want {
			t.Errorf("clusterDomainFromSearches(%q) = %q, want %q", tc.searches, got, tc.want)
		}
	}
}

// A component is patched only when it is outside the Kafka namespace, lacks
// the search domain and --fix is on; a fix counts only when the patch works.
func TestAuditComponentDNS(t *testing.T) {
	origKafka, origFix := doctorKafkaNS, doctorDnsFix
	t.Cleanup(func() { doctorKafkaNS, doctorDnsFix = origKafka, origFix })
	doctorKafkaNS = "kafka"

	for _, tc := range []struct {
		name      string
		ns        string
		missing   bool
		fix       bool
		patchErr  error
		wantFixed bool
		wantPatch bool
	}{
		{"same namespace as Kafka", "kafka", true, true, nil, false, false},
		{"already has the domain", "kates", false, true, nil, false, false},
		{"missing without --fix", "kates", true, false, nil, false, false},
		{"missing with --fix", "kates", true, true, nil, true, true},
		{"patch fails", "kates", true, true, errors.New("forbidden"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doctorDnsFix = tc.fix
			patched := false
			c := dnsAuditComponent{label: "kates backend", patchName: "kates deployment", ns: tc.ns,
				check: func(context.Context, *kubectl.Client, string, string) bool { return tc.missing },
				patch: func(context.Context, *kubectl.Client, string, string) error { patched = true; return tc.patchErr },
			}
			if got := auditComponentDNS(context.Background(), nil, c, "kafka.svc.cluster.local"); got != tc.wantFixed {
				t.Errorf("fixed = %v, want %v", got, tc.wantFixed)
			}
			if patched != tc.wantPatch {
				t.Errorf("patched = %v, want %v", patched, tc.wantPatch)
			}
		})
	}
}
