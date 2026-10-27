package detect

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tests for the pieces the wave-5 split pulled out of the collector's
// probes, the analyzer's verdict and the remediation rules.

func TestStrimziWarningLogs_KeepsTroubleSkipsKnownNoiseAndStopsAtTen(t *testing.T) {
	logs := strings.Join([]string{
		"INFO reconciled",
		"WARN MeterRegistry gauge clash",              // known noise
		"WARN PlatformFeaturesAvailability: no route", // known noise
		"ERROR reconcile failed",
		"Permission denied on secrets",
		"leader election lost",
	}, "\n")
	got := strimziWarningLogs(logs)
	want := []string{"ERROR reconcile failed", "Permission denied on secrets", "leader election lost"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("strimziWarningLogs = %q, want %q", got, want)
	}

	many := strings.Repeat("WARN again\n", 25)
	if got := strimziWarningLogs(many); len(got) != 10 {
		t.Errorf("strimziWarningLogs kept %d lines of 25, want 10", len(got))
	}
	if got := strimziWarningLogs(""); got != nil {
		t.Errorf("strimziWarningLogs(\"\") = %q, want nil", got)
	}
}

func TestKafkaConnectFromSpec(t *testing.T) {
	var item kafkaConnectItem
	if err := json.Unmarshal([]byte(`{
		"metadata":{"name":"cdc","namespace":"conn"},
		"spec":{"replicas":3,"image":"img","build":{},"rack":{"topologyKey":"z"},
			"jvmOptions":{"-Xms":"1g","-Xmx":"2g"},
			"resources":{"requests":{"cpu":"500m"},"limits":{"cpu":"2","memory":"4Gi"}},
			"template":{"podDisruptionBudget":{"maxUnavailable":1}}},
		"status":{"connectorPlugins":[{"class":"a.B"}]}}`), &item); err != nil {
		t.Fatal(err)
	}
	got := kafkaConnectFromSpec(item)
	want := KafkaConnectStatus{
		Installed: true, Name: "cdc", Namespace: "conn", TotalReplicas: 3, Image: "img",
		HasBuild: true, RackAware: true, HeapXms: "1g", HeapXmx: "2g",
		MemLimitMi: 4096, CPULimitM: 2000, CPURequestM: 500,
		PDBConfigured: true, Plugins: []string{"a.B"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kafkaConnectFromSpec =\n%+v\nwant\n%+v", got, want)
	}
}

func TestKyvernoPolicyFromJSON(t *testing.T) {
	pol, ok := kyvernoPolicyFromJSON(json.RawMessage(`{
		"metadata":{"name":"require-limits","annotations":{"kyverno.io/category":"Res","policies.kyverno.io/description":"d"}},
		"spec":{"rules":[{"name":"r1","match":{"resources":{"namespaces":["other"]}}},
			{"name":"r2","match":{"any":[{"resources":{"namespaces":["Kafka"]}}]}}]}}`))
	if !ok {
		t.Fatal("kyvernoPolicyFromJSON refused a policy")
	}
	want := KyvernoPolicyInfo{Name: "require-limits", Action: "audit", Category: "Res", AffectsKafka: true, Rules: []string{"r1", "r2"}, Description: "d"}
	if !reflect.DeepEqual(pol, want) {
		t.Errorf("policy = %+v, want %+v", pol, want)
	}
	if _, ok := kyvernoPolicyFromJSON(json.RawMessage(`{"metadata":"broken"}`)); ok {
		t.Error("kyvernoPolicyFromJSON accepted a broken policy")
	}
}

func TestKyvernoRuleAffectsKafka(t *testing.T) {
	for _, tc := range []struct {
		match string
		want  bool
	}{
		{`{}`, true}, // no namespace filter: every namespace
		{`{"resources":{"namespaces":["*"]}}`, true},
		{`{"resources":{"namespaces":["KAFKA"]}}`, true},
		{`{"resources":{"namespaces":["other"]}}`, false},
		{`{"any":[{"resources":{}}]}`, true},
		{`{"any":[{"resources":{"namespaces":["a"]}},{"resources":{"namespaces":["kafka"]}}]}`, true},
		{`{"any":[{"resources":{"namespaces":["a"]}}]}`, false},
	} {
		var r kyvernoRule
		if err := json.Unmarshal([]byte(`{"name":"r","match":`+tc.match+`}`), &r); err != nil {
			t.Fatal(err)
		}
		if got := kyvernoRuleAffectsKafka(r); got != tc.want {
			t.Errorf("kyvernoRuleAffectsKafka(%s) = %v, want %v", tc.match, got, tc.want)
		}
	}
}

func TestApplyKyvernoConstraints(t *testing.T) {
	var con PolicyConstraints
	applyKyvernoConstraints(&con, KyvernoPolicyInfo{Name: "Disallow-Empty-PodSelector", Rules: []string{"run-as-non-root"}})
	applyKyvernoConstraints(&con, KyvernoPolicyInfo{Name: "require-resource-limits"})
	want := PolicyConstraints{EmptyPodSelectorBlocked: true, RunAsRootBlocked: true, ResourceLimitsRequired: true}
	if !reflect.DeepEqual(con, want) {
		t.Errorf("constraints = %+v, want %+v", con, want)
	}
}

func TestFioJobMeasurement(t *testing.T) {
	dir := func(iops, latNs float64) fioDirection {
		d := fioDirection{IOPS: iops}
		d.Lat.Mean = latNs
		return d
	}
	for _, tc := range []struct {
		name    string
		job     fioJob
		iops    int
		latency float64
	}{
		{"mixed", fioJob{Read: dir(1000, 2e6), Write: dir(500, 4e6)}, 1500, 3},
		{"read only", fioJob{Read: dir(800, 1e6)}, 800, 1},
		{"write only", fioJob{Write: dir(300, 5e6)}, 300, 5},
		{"idle", fioJob{}, 0, 0},
	} {
		iops, lat := fioJobMeasurement(tc.job)
		if iops != tc.iops || lat != tc.latency {
			t.Errorf("%s: fioJobMeasurement = %d, %v; want %d, %v", tc.name, iops, lat, tc.iops, tc.latency)
		}
	}
}

func TestParseIperfMbps(t *testing.T) {
	for _, tc := range []struct {
		out  string
		mbps float64
		ok   bool
	}{
		{`Connecting...{"end":{"sum_received":{"bits_per_second":2500000000}}}`, 2500, true},
		{`{"end":{"sum_received":{"bits_per_second":0}}}`, 0, false},
		{`{"error":"unable to connect","end":{"sum_received":{"bits_per_second":10}}}`, 0, false},
		{`no json here`, 0, false},
		{`{broken`, 0, false},
	} {
		mbps, ok := parseIperfMbps(tc.out)
		if mbps != tc.mbps || ok != tc.ok {
			t.Errorf("parseIperfMbps(%q) = %v, %v; want %v, %v", tc.out, mbps, ok, tc.mbps, tc.ok)
		}
	}
}

func TestFirstNodePerZone(t *testing.T) {
	got := firstNodePerZone([]NodeInfo{
		{Name: "a1", Zone: "a"}, {Name: "a2", Zone: "a"}, {Name: "b1", Zone: "b"},
		{Name: "x", Zone: "-"}, {Name: "y", Zone: ""},
	})
	want := map[string]string{"a": "a1", "b": "b1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("firstNodePerZone = %v, want %v", got, want)
	}
}

func TestPSAEnforceLabel(t *testing.T) {
	if level, ok := psaEnforceLabel(`{"metadata":{"labels":{"pod-security.kubernetes.io/enforce":"restricted"}}}`); !ok || level != "restricted" {
		t.Errorf("psaEnforceLabel = %q, %v; want restricted, true", level, ok)
	}
	if _, ok := psaEnforceLabel(`{"metadata":{"labels":{}}}`); ok {
		t.Error("psaEnforceLabel found a label on an unlabelled namespace")
	}
	if _, ok := psaEnforceLabel(`not json`); ok {
		t.Error("psaEnforceLabel found a label in output that is not JSON")
	}
}

func testCertB64(t *testing.T, cn string, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: notAfter.Add(-48 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestTLSSecretExpiries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	soon := testCertB64(t, "broker", now.Add(10*24*time.Hour+time.Hour))
	secrets := `{"items":[
		{"metadata":{"name":"tls"},"type":"kubernetes.io/tls","data":{"tls.crt":"` + soon + `"}},
		{"metadata":{"name":"opaque"},"type":"Opaque","data":{"tls.crt":"` + soon + `"}},
		{"metadata":{"name":"bad"},"type":"kubernetes.io/tls","data":{"tls.crt":"!!"}},
		{"metadata":{"name":"none"},"type":"kubernetes.io/tls"}]}`
	got := tlsSecretExpiries(secrets, now)
	want := []CertExpirationInfo{{SecretName: "tls", Subject: "broker", ExpiryDate: "2026-01-11", DaysLeft: 10}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tlsSecretExpiries = %+v, want %+v", got, want)
	}
	if got := tlsSecretExpiries("not json", now); got != nil {
		t.Errorf("tlsSecretExpiries(not json) = %+v, want nil", got)
	}
}

func TestRolesHaveWildcardRule(t *testing.T) {
	for _, tc := range []struct {
		roles string
		want  bool
	}{
		// Wildcards on verbs and resources in different rules do not count.
		{`{"items":[{"rules":[{"verbs":["get"],"resources":["*"]},{"verbs":["*"],"resources":["pods"]}]}]}`, false},
		{`{"items":[{"rules":[{"verbs":["list"]}]},{"rules":[{"verbs":["get","*"],"resources":["x","*"]}]}]}`, true},
		{`{"items":[]}`, false},
		{`not json`, false},
	} {
		if got := rolesHaveWildcardRule(tc.roles); got != tc.want {
			t.Errorf("rolesHaveWildcardRule(%s) = %v, want %v", tc.roles, got, tc.want)
		}
	}
}

// TestGenerateRemediation_RuleOrder pins the order the rule table lists the
// hints in, with every rule firing.
func TestGenerateRemediation_RuleOrder(t *testing.T) {
	r := &DetectReport{}
	r.Strimzi.CRDsPresent = true // the operator-not-running hint
	r.Admission.Kyverno.Installed = true
	r.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked = true
	r.Admission.Kyverno.Constraints.ResourceLimitsRequired = true
	r.ExistingKafka.KafkaClusters = 1
	r.Verdict.Checks = []CheckResult{
		{Description: "RBAC permissions", Status: false},
		{Description: "Sizing recommendation available", Status: false},
	}
	r.Security.PSALabelEnforced = "restricted"
	r.Security.HasExcessivePrivileges = true
	r.Security.ExpiringCerts = []CertExpirationInfo{{SecretName: "s", Subject: "x", DaysLeft: 3}}

	var got []string
	for _, h := range GenerateRemediation(r) {
		got = append(got, h.Check)
	}
	want := []string{
		"Strimzi operator running",
		"≥ 3 availability zones",
		"StorageClass available",
		"Broker resources fit (all zones)",
		"DNS resolution",
		"RBAC permissions",
		"Prometheus monitoring",
		"Kyverno NetworkPolicy safe",
		"Resource limits required",
		"Existing deployment",
		"Sizing recommendation available",
		"Pod Security Standards compatible",
		"Wildcard RBAC roles avoided",
		"TLS certificate validity",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hints in order\n%q\nwant\n%q", got, want)
	}
}

func TestCheckConnectHeap(t *testing.T) {
	for _, tc := range []struct {
		xmx          string
		limit        int
		rows, fails  int
		warns        int
		detailPrefix string
	}{
		{"3g", 4096, 1, 0, 1, "75% of limit — limited off-heap"}, // 75 is not over 75
		{"3100m", 4096, 1, 1, 0, "76% of limit — risks OOMKill"},
		{"2100m", 4096, 1, 0, 1, "51% of limit — limited off-heap"},
		{"1g", 4096, 1, 0, 0, "25% of limit — healthy"},
		{"", 4096, 0, 0, 0, ""},
		{"1g", 0, 0, 0, 0, ""},
	} {
		b := &verdictBuilder{}
		b.checkConnectHeap(KafkaConnectStatus{HeapXmx: tc.xmx, MemLimitMi: tc.limit})
		if len(b.v.Checks) != tc.rows || b.v.Fails != tc.fails || b.v.Warns != tc.warns {
			t.Errorf("%s/%d: rows %d fails %d warns %d; want %d %d %d", tc.xmx, tc.limit, len(b.v.Checks), b.v.Fails, b.v.Warns, tc.rows, tc.fails, tc.warns)
			continue
		}
		if tc.rows > 0 && !strings.HasPrefix(b.v.Checks[0].Detail, tc.detailPrefix) {
			t.Errorf("%s/%d: detail %q, want prefix %q", tc.xmx, tc.limit, b.v.Checks[0].Detail, tc.detailPrefix)
		}
	}
}

func TestCheckSecretCapability(t *testing.T) {
	for _, tc := range []struct {
		audit  SecretCreationAudit
		detail string // "" for no row
	}{
		{SecretCreationAudit{NamespaceCreated: true, SecretCreated: true}, "verified (functional)"},
		{SecretCreationAudit{NamespaceCreated: true, BlockedByPolicy: true}, "blocked by admission policy: unknown policy"},
		{SecretCreationAudit{NamespaceCreated: true, BlockedByPolicy: true, PolicyName: "p"}, "blocked by admission policy: p"},
		{SecretCreationAudit{NamespaceCreated: true, ErrorMsg: "boom"}, "failed: boom"},
		{SecretCreationAudit{ErrorMsg: "nope"}, "namespace blocked: nope"},
		{SecretCreationAudit{}, ""},
	} {
		b := &verdictBuilder{}
		b.checkSecretCapability(&DetectReport{SecretAudit: tc.audit})
		switch {
		case tc.detail == "" && len(b.v.Checks) != 0:
			t.Errorf("%+v: got a row %+v, want none", tc.audit, b.v.Checks)
		case tc.detail != "" && (len(b.v.Checks) != 1 || b.v.Checks[0].Detail != tc.detail):
			t.Errorf("%+v: rows %+v, want one with detail %q", tc.audit, b.v.Checks, tc.detail)
		}
	}
}

// TestComputeStorageBudget_ReadsThePVTotal: the PV total the collector
// writes ("<n>Gi") sizes the volumes; any other unit falls back to the
// profile defaults, as no PV inventory does.
func TestComputeStorageBudget_ReadsThePVTotal(t *testing.T) {
	gen := func(total string, count int) *ValuesGenerator {
		r := &DetectReport{StorageAudit: StorageAudit{PVCount: count, PVTotalCapacity: total}}
		return &ValuesGenerator{Report: r, ReservePct: 0}
	}
	if b, c := gen("1000Gi", 1).computeStorageBudget(3, 3); b != "266Gi" || c != "50Gi" {
		t.Errorf("1000Gi over 3+3: %s, %s; want 266Gi, 50Gi", b, c)
	}
	fb, fc := gen("", 0).computeStorageBudget(3, 3)
	if b, c := gen("1000Mi", 1).computeStorageBudget(3, 3); b != fb || c != fc {
		t.Errorf("1000Mi: %s, %s; want the fallback %s, %s", b, c, fb, fc)
	}
}
