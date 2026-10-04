package migrate

import (
	"reflect"
	"testing"
)

// preflightPass is the log of the chart's preflight Job on a plaintext 2.8
// source that answers, as templates/preflight-job.yaml prints it.
const preflightPass = `MirrorMaker 2 preflight — client quay.io/strimzi/kafka:1.1.0-kafka-4.3.0

══ source: source ── m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092  [plaintext]
  ✅ HANDSHAKE broker answered ApiVersions to a 4.3.0 client
               m282-430-src-legacy-kafka-0.m282-430-src-legacy-kafka-headless.kafka-m282-430-src.svc.cluster.local:9092 (id: 0 rack: null) -> (

✅ preflight: every source is reachable by a 4.3.0 client
`

// preflightFail has two sources: one below the protocol floor (with the
// client's own output echoed after the verdict), one unresolvable.
const preflightFail = "MirrorMaker 2 preflight — client quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\r\n" +
	"\r\n" +
	"══ source: legacy ── legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092  [plaintext]\r\n" +
	"  ❌ PROTOCOL  UNSUPPORTED_VERSION — this broker predates the Kafka 2.1\r\n" +
	"               floor that 4.3.0 clients enforce (KIP-896).\r\n" +
	"               There is no flag for this. Migrate via an intermediate 3.x cluster.\r\n" +
	"               [2026-09-08 10:03:12,441] ERROR [AdminClient clientId=adminclient-1] Connection to node -1 failed (org.apache.kafka.clients.NetworkClient)\r\n" +
	"               Exception in thread \"main\" java.util.concurrent.ExecutionException: org.apache.kafka.common.errors.UnsupportedVersionException: The broker does not support API_VERSIONS\r\n" +
	"               at java.base/java.util.concurrent.CompletableFuture.reportGet(CompletableFuture.java:396)\r\n" +
	"\r\n" +
	"══ source: dr ── kafka-bootstrap.kafka-dr.svc.cluster.local:9094  [sasl]\r\n" +
	"  ❌ DNS       the bootstrap host does not resolve\r\n" +
	"               check the namespace and cluster domain in bootstrapServers\r\n" +
	"\r\n" +
	"❌ preflight: 2 source(s) failed\r\n"

// preflightSkip is a SASL source whose Secret secretSync has not copied yet,
// and a TLS source the probe cannot verify trust for.
const preflightSkip = `MirrorMaker 2 preflight — client quay.io/strimzi/kafka:1.1.0-kafka-4.3.0

══ source: source ── m391-430-src-legacy-kafka-bootstrap.kafka-m391-430-src.svc.cluster.local:9094  [sasl]
  ⏭  CREDENTIAL not present yet (secretSync copies it after install) —
               probing without it; the credentials are NOT verified this time
  ⏭  AUTH      the listener requires authentication and the credentials
               were not available to this probe. Name resolution,
               reachability and the protocol floor are verified; the
               credentials will be checked on the next upgrade.
               [2026-09-08 10:05:00,000] WARN [AdminClient clientId=adminclient-1] Connection to node -1 terminated during authentication. (org.apache.kafka.clients.NetworkClient)

══ source: prod ── kafka-bootstrap.kafka-prod.svc.cluster.local:9093  [tls]
  ⏭  TLS       reachable and speaking TLS, but the certificate is not
               trusted by this probe (it has no truststore). Trust and
               credentials were NOT verified — the workers will use
               tls.trustedCertificateSecret, which this probe does not.

✅ preflight: every source is reachable by a 4.3.0 client
`

func TestParseVerdictsPass(t *testing.T) {
	p := ParsePreflight(preflightPass)
	if p.Client != "quay.io/strimzi/kafka:1.1.0-kafka-4.3.0" || !p.Concluded || !p.Passed {
		t.Errorf("preflight = %+v", p)
	}
	want := []Verdict{{
		Source: "source", Bootstrap: "m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092", Mode: "plaintext",
		Status: VerdictPass, Code: CodeHandshake, Detail: "broker answered ApiVersions to a 4.3.0 client",
		Output: []string{"m282-430-src-legacy-kafka-0.m282-430-src-legacy-kafka-headless.kafka-m282-430-src.svc.cluster.local:9092 (id: 0 rack: null) -> ("},
	}}
	if !reflect.DeepEqual(p.Verdicts, want) {
		t.Errorf("verdicts = %+v\nwant %+v", p.Verdicts, want)
	}
	if AnyFailed(p.Verdicts) {
		t.Error("a passing log has no failure")
	}
	if got := ParseVerdicts(preflightPass); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseVerdicts = %+v", got)
	}
}

func TestParseVerdictsFail(t *testing.T) {
	p := ParsePreflight(preflightFail)
	if !p.Concluded || p.Passed {
		t.Errorf("concluded=%v passed=%v", p.Concluded, p.Passed)
	}
	want := []Verdict{
		{
			Source: "legacy", Bootstrap: "legacy-legacy-kafka-bootstrap.kafka-legacy-2x.svc.cluster.local:9092", Mode: "plaintext",
			Status: VerdictFail, Code: CodeProtocol,
			Detail: "UNSUPPORTED_VERSION — this broker predates the Kafka 2.1 floor that 4.3.0 clients enforce (KIP-896). There is no flag for this. Migrate via an intermediate 3.x cluster.",
			Output: []string{
				"[2026-09-08 10:03:12,441] ERROR [AdminClient clientId=adminclient-1] Connection to node -1 failed (org.apache.kafka.clients.NetworkClient)",
				"Exception in thread \"main\" java.util.concurrent.ExecutionException: org.apache.kafka.common.errors.UnsupportedVersionException: The broker does not support API_VERSIONS",
				"at java.base/java.util.concurrent.CompletableFuture.reportGet(CompletableFuture.java:396)",
			},
		},
		{
			Source: "dr", Bootstrap: "kafka-bootstrap.kafka-dr.svc.cluster.local:9094", Mode: "sasl",
			Status: VerdictFail, Code: CodeDNS,
			Detail: "the bootstrap host does not resolve check the namespace and cluster domain in bootstrapServers",
		},
	}
	if !reflect.DeepEqual(p.Verdicts, want) {
		t.Errorf("verdicts = %+v\nwant %+v", p.Verdicts, want)
	}
	if !AnyFailed(p.Verdicts) || !p.Verdicts[0].Failed() {
		t.Error("failures not reported")
	}
}

func TestParseVerdictsSkip(t *testing.T) {
	p := ParsePreflight(preflightSkip)
	if !p.Concluded || !p.Passed || AnyFailed(p.Verdicts) {
		t.Errorf("skips are not failures: %+v", p)
	}
	if len(p.Verdicts) != 3 {
		t.Fatalf("got %d verdicts: %+v", len(p.Verdicts), p.Verdicts)
	}
	codes := []string{p.Verdicts[0].Code, p.Verdicts[1].Code, p.Verdicts[2].Code}
	if !reflect.DeepEqual(codes, []string{CodeCredential, CodeAuth, CodeTLS}) {
		t.Errorf("codes = %v", codes)
	}
	for i, v := range p.Verdicts {
		if v.Status != VerdictSkip {
			t.Errorf("verdict %d status = %s", i, v.Status)
		}
	}
	if p.Verdicts[0].Detail != "not present yet (secretSync copies it after install) — probing without it; the credentials are NOT verified this time" {
		t.Errorf("credential detail = %q", p.Verdicts[0].Detail)
	}
	if len(p.Verdicts[1].Output) != 1 || p.Verdicts[1].Detail == "" {
		t.Errorf("auth verdict = %+v", p.Verdicts[1])
	}
	if p.Verdicts[2].Source != "prod" || p.Verdicts[2].Mode != "tls" || len(p.Verdicts[2].Output) != 0 {
		t.Errorf("tls verdict = %+v", p.Verdicts[2])
	}
}

func TestParseVerdictsTruncatedAndEmpty(t *testing.T) {
	cut := "MirrorMaker 2 preflight — client img\n\n══ source: s ── h:9092  [plaintext]\n  ❌ NETWORK   resolvable, not reachable — NetworkPolicy, a wrong port,\n"
	p := ParsePreflight(cut)
	if p.Concluded || p.Passed {
		t.Error("a truncated log has not concluded")
	}
	if len(p.Verdicts) != 1 || p.Verdicts[0].Code != CodeNetwork || p.Verdicts[0].Status != VerdictFail {
		t.Errorf("verdicts = %+v", p.Verdicts)
	}
	if got := ParseVerdicts(""); got != nil {
		t.Errorf("empty log = %+v", got)
	}
	if got := ParseVerdicts("Error from server (NotFound): jobs.batch \"x\" not found\n"); got != nil {
		t.Errorf("kubectl error = %+v", got)
	}
}
