package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// scriptVerify scripts the client pods and every Kafka tool the verify
// phases run for the 2.8.2 lab, with a mirror that stops on cutover and a
// target whose end offsets never move afterwards. consumed is what the
// target's console consumer prints.
func scriptVerify(f *fakeProc, consumed string) {
	scriptPods(f)
	f.on("kubectl -n kafka-m282-430-src get statefulset m282-430-src-legacy-kafka -o jsonpath={.spec.template.spec.containers[0].image}", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2")

	// The source's own 2.8 tools.
	f.contains("exec -i kates-migrate-m282-430-src -c client -- /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092 --topic kates.orders", "")
	f.contains("exec kates-migrate-m282-430-src -c client -- /opt/kafka/bin/kafka-run-class.sh kafka.tools.GetOffsetShell --broker-list m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092 --topic kates.orders --time -1",
		"kates.orders:0:67\nkates.orders:1:67\nkates.orders:2:66")
	f.contains("exec kates-migrate-m282-430-src -c client -- /opt/kafka/bin/kafka-console-consumer.sh", "")
	f.contains("exec kates-migrate-m282-430-src -c client -- /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server m282-430-src-legacy-kafka-bootstrap.kafka-m282-430-src.svc.cluster.local:9092 --describe --group kates-migration-m282-430",
		"GROUP                    TOPIC        PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG  CONSUMER-ID  HOST  CLIENT-ID\n"+
			"kates-migration-m282-430 kates.orders 0          34              67              33   -            -     -\n"+
			"kates-migration-m282-430 kates.orders 1          33              67              34   -            -     -\n"+
			"kates-migration-m282-430 kates.orders 2          33              66              33   -            -     -")

	// The target's 4.3 tools, as kates-mm2.
	target := "exec kates-migrate-m282-430-tgt -c client -- /opt/kafka/bin/"
	f.contains(target+"kafka-topics.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --command-config /tmp/client.properties --list", "__consumer_offsets\nkates-events\nkates.orders\nmm2-m282-430-offsets")
	f.contains(target+"kafka-console-consumer.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --consumer.config /tmp/client.properties --topic kates.orders --group kates-migration-verify-m282-430-0 --from-beginning --max-messages 400 --timeout-ms 120000", consumed)
	f.contains(target+"kafka-consumer-groups.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --command-config /tmp/client.properties --describe --group kates-migration-m282-430",
		"GROUP                    TOPIC        PARTITION  CURRENT-OFFSET  LOG-END-OFFSET  LAG  CONSUMER-ID  HOST  CLIENT-ID\n"+
			"kates-migration-m282-430 kates.orders 0          34              67              33   -            -     -\n"+
			"kates-migration-m282-430 kates.orders 1          33              67              34   -            -     -\n"+
			"kates-migration-m282-430 kates.orders 2          30              66              36   -            -     -")
	f.contains(target+"kafka-get-offsets.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --topic kates.orders --time -1 --command-config /tmp/client.properties",
		"kates.orders:0:67\nkates.orders:1:67\nkates.orders:2:66")

	// The cutover: the CR reports STOPPED/RUNNING once the overlay is applied.
	var mu sync.Mutex
	cut := false
	f.handle("helm upgrade mm2-m282-430 charts/mirror-maker2 -n kafka --reuse-values -f charts/mirror-maker2/values-cutover.yaml --timeout 600s", func([]string, string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		cut = true
		return "", nil
	})
	f.handle("kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json", func([]string, string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if cut {
			return mirrorCRJSON("mm2-m282-430-mirror-maker2", "STOPPED", "RUNNING"), nil
		}
		return mirrorCRJSON("mm2-m282-430-mirror-maker2", "RUNNING", "RUNNING"), nil
	})
}

// scriptRunDown scripts the teardown run performs from its own lab (no
// discovery call: it knows the names).
func scriptRunDown(f *fakeProc) {
	f.on("kubectl delete pod -A -l kates.io/lab=m282-430", "")
	f.on("helm uninstall mm2-m282-430 -n kafka", "")
	f.on("kubectl -n kafka delete kafkamirrormaker2 mm2-m282-430-mirror-maker2 --ignore-not-found --timeout=60s", "")
	f.on("helm uninstall m282-430-src -n kafka-m282-430-src", "")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab=m282-430 --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab=m282-430 --ignore-not-found", "")
	f.on("kubectl delete namespace kafka-m282-430-src --ignore-not-found --timeout=600s", "")
}

type reportDoc struct {
	Header  map[string]string `json:"header"`
	Rows    []migrate.Row     `json:"rows"`
	Summary struct {
		Pass   int  `json:"pass"`
		Fail   int  `json:"fail"`
		Skip   int  `json:"skip"`
		Failed bool `json:"failed"`
	} `json:"summary"`
}

func parseReport(t *testing.T, out string) reportDoc {
	t.Helper()
	var doc reportDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("run -o json is not a report: %v\n%s", err, out)
	}
	return doc
}

func TestMigrateRunGreen(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	scriptVerify(f, strings.Join(migrate.Records("m282-430", 200), "\n"))
	scriptRunDown(f)
	out := captureOutput(t, "json")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	if err := runMigrateRun(nil, flags); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	doc := parseReport(t, out.String())
	if doc.Summary.Failed || doc.Summary.Fail != 0 || doc.Summary.Pass != len(migrate.RowNames) {
		t.Fatalf("summary: %+v\nrows: %+v", doc.Summary, doc.Rows)
	}
	// The scripts' eighteen rows, in the order run executes them: up (the
	// mirror is installed before the corpus is seeded), verify, cutover.
	wantRows := []string{
		migrate.RowClusterReachable, migrate.RowStrimziCRDs, migrate.RowTargetKafkaReady, migrate.RowTargetCredentials,
		migrate.RowSourceDeployed, migrate.RowSourceServing,
		migrate.RowMirrorInstalled, migrate.RowCRReady, migrate.RowConnectorsRunning,
		migrate.RowCorpusProduced, migrate.RowSourceEndOffsets, migrate.RowSourceConsumerGroup,
		migrate.RowReplicatedTopic, migrate.RowRecordCount, migrate.RowRecordContent, migrate.RowOffsetTranslation,
		migrate.RowCutoverApplied, migrate.RowCutoverFroze,
	}
	if len(doc.Rows) != len(wantRows) {
		t.Fatalf("want %d rows, got %d: %+v", len(wantRows), len(doc.Rows), doc.Rows)
	}
	for i, name := range wantRows {
		if doc.Rows[i].Name != name || doc.Rows[i].Status != migrate.StatusPass {
			t.Fatalf("row %d = %+v, want PASS %q", i, doc.Rows[i], name)
		}
	}
	for _, key := range []string{"pair", "source", "target", "operator", "policy", "corpus", "client"} {
		if doc.Header[key] == "" {
			t.Errorf("header lacks %q: %v", key, doc.Header)
		}
	}
	if doc.Header["pair"] != "Kafka 2.8.2 → 4.3.0" || !strings.Contains(doc.Header["operator"], "Strimzi 1.1.0 in strimzi-operator (cluster scope") || !strings.HasPrefix(doc.Header["policy"], "identity") {
		t.Fatalf("header: %v", doc.Header)
	}
	// The corpus went in on stdin, never on a command line.
	producers := f.find("kubectl -n kafka-m282-430-src exec -i kates-migrate-m282-430-src -c client -- /opt/kafka/bin/kafka-console-producer.sh")
	if len(producers) != 2 { // the corpus, then the post-cutover records
		t.Fatalf("want two producer runs, got %d", len(producers))
	}
	if !strings.Contains(producers[0].stdin, `"lab":"m282-430"`) || strings.Count(producers[0].stdin, "\n") != 200 {
		t.Fatalf("producer stdin: %q…", producers[0].stdin[:80])
	}
	if !strings.HasPrefix(producers[1].stdin, "after-cutover-1\n") {
		t.Fatalf("post-cutover stdin: %q…", producers[1].stdin[:40])
	}
	// The credential reached the pod through tee on stdin only.
	for _, l := range f.lines() {
		if strings.Contains(l, "s3cret") {
			t.Fatalf("the password reached a command line: %q", l)
		}
	}
	tees := f.find("kubectl -n kafka exec -i kates-migrate-m282-430-tgt -c client -- tee /tmp/client.properties")
	if len(tees) != 1 || !strings.Contains(tees[0].stdin, `username="kates-mm2" password="s3cret"`) || !strings.Contains(tees[0].stdin, "SCRAM-SHA-512") {
		t.Fatalf("target client.properties: %+v", tees)
	}
	// The lab was torn down: the mirror, the CR, the source, its namespace.
	for _, want := range []string{
		"helm uninstall mm2-m282-430 -n kafka",
		"kubectl -n kafka delete kafkamirrormaker2 mm2-m282-430-mirror-maker2",
		"helm uninstall m282-430-src -n kafka-m282-430-src",
		"kubectl delete namespace kafka-m282-430-src",
		"kubectl -n kafka delete pod kates-migrate-m282-430-tgt --ignore-not-found",
	} {
		if !f.hasLine(want) {
			t.Errorf("run did not clean up with %q", want)
		}
	}
	if f.indexOf("helm uninstall mm2-m282-430") < f.indexOf("helm upgrade mm2-m282-430 charts/mirror-maker2 -n kafka --reuse-values") {
		t.Fatal("the teardown ran before the cutover")
	}
}

// scriptVerifyFanIn scripts the client pods and tools of the two-source lab:
// one pod per source, each speaking its own release's scripts, and one on
// the target that sees both replicated topics.
func scriptVerifyFanIn(f *fakeProc, consumed string) {
	scriptPods(f)
	const lab = "m282-391-430"
	for _, tc := range []struct{ alias, image string }{{"src282", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"}, {"src391", "apache/kafka:3.9.1"}} {
		ns := "kafka-" + lab + "-" + tc.alias
		f.on("kubectl -n "+ns+" get statefulset "+lab+"-"+tc.alias+"-legacy-kafka -o jsonpath={.spec.template.spec.containers[0].image}", tc.image)
		pod := "kates-migrate-" + lab + "-" + tc.alias
		topic := "kates.orders." + tc.alias
		f.contains("exec -i "+pod+" -c client -- /opt/kafka/bin/kafka-console-producer.sh", "")
		// The 2.8 line answers through GetOffsetShell, the 3.9 line through
		// kafka-get-offsets.sh: both are matched by the topic they name.
		f.rule(func(l string) bool {
			return strings.Contains(l, pod) && strings.Contains(l, "--topic "+topic) &&
				(strings.Contains(l, "GetOffsetShell") || strings.Contains(l, "kafka-get-offsets.sh"))
		}, func([]string, string) (string, error) {
			return topic + ":0:67\n" + topic + ":1:67\n" + topic + ":2:66", nil
		})
		f.contains("exec "+pod+" -c client -- /opt/kafka/bin/kafka-console-consumer.sh", "")
		f.contains("exec "+pod+" -c client -- /opt/kafka/bin/kafka-consumer-groups.sh",
			"GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG CONSUMER-ID HOST CLIENT-ID\n"+
				"kates-migration-"+lab+" "+topic+" 0 34 67 33 - - -\n"+
				"kates-migration-"+lab+" "+topic+" 1 33 67 34 - - -")
	}
	target := "exec kates-migrate-" + lab + "-tgt -c client -- /opt/kafka/bin/"
	f.contains(target+"kafka-topics.sh", "__consumer_offsets\nkates.orders.src282\nkates.orders.src391")
	f.contains(target+"kafka-console-consumer.sh", consumed)
	f.contains(target+"kafka-consumer-groups.sh",
		"GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG CONSUMER-ID HOST CLIENT-ID\n"+
			"kates-migration-"+lab+" kates.orders.src282 0 34 67 33 - - -\n"+
			"kates-migration-"+lab+" kates.orders.src391 0 33 67 34 - - -")
	f.contains(target+"kafka-get-offsets.sh",
		"kates.orders.src282:0:67\nkates.orders.src282:1:67\nkates.orders.src282:2:66\n"+
			"kates.orders.src391:0:67\nkates.orders.src391:1:67\nkates.orders.src391:2:66")

	cut := false
	f.handle("helm upgrade mm2-"+lab+" charts/mirror-maker2 -n kafka --reuse-values -f charts/mirror-maker2/values-cutover.yaml --timeout 600s",
		func([]string, string) (string, error) { cut = true; return "", nil })
	f.handle("kubectl -n kafka get kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 -o json", func([]string, string) (string, error) {
		if cut {
			return mirrorCRJSON("mm2-"+lab+"-mirror-maker2", "STOPPED", "RUNNING"), nil
		}
		return mirrorCRJSON("mm2-"+lab+"-mirror-maker2", "RUNNING", "RUNNING"), nil
	})
}

func TestMigrateRunFanInReportsEveryLeg(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	const lab = "m282-391-430"
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUpFanIn(f)
	scriptVerifyFanIn(f, strings.Join(migrate.Records(lab, 200), "\n"))
	// The teardown of both sources.
	f.on("kubectl delete pod -A -l kates.io/lab="+lab, "")
	f.on("helm uninstall mm2-"+lab+" -n kafka", "")
	f.on("kubectl -n kafka delete kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 --ignore-not-found --timeout=60s", "")
	f.on("helm uninstall "+lab+"-src282 -n kafka-"+lab+"-src282", "")
	f.on("helm uninstall "+lab+"-src391 -n kafka-"+lab+"-src391", "")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab="+lab+" --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab="+lab+" --ignore-not-found", "")
	f.on("kubectl delete namespace kafka-"+lab+"-src282 --ignore-not-found --timeout=600s", "")
	f.on("kubectl delete namespace kafka-"+lab+"-src391 --ignore-not-found --timeout=600s", "")
	out := captureOutput(t, "json")

	flags := newPairFlags("2.8.2", "3.9.1")
	flags.SkipBuild = true
	if err := runMigrateRun(nil, flags); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	doc := parseReport(t, out.String())
	if doc.Summary.Failed || doc.Summary.Fail != 0 {
		t.Fatalf("summary: %+v\nrows: %+v", doc.Summary, doc.Rows)
	}
	rows := map[string]migrate.Row{}
	for _, r := range doc.Rows {
		rows[r.Name] = r
	}
	// Every per-source assertion is one row per leg, named by its alias; the
	// rows about the release itself stay single.
	for _, name := range []string{
		migrate.RowSourceDeployed, migrate.RowSourceServing, migrate.RowCorpusProduced,
		migrate.RowSourceEndOffsets, migrate.RowSourceConsumerGroup,
		migrate.RowRecordCount, migrate.RowRecordContent, migrate.RowOffsetTranslation,
	} {
		for _, alias := range []string{"src282", "src391"} {
			row, ok := rows[name+" ["+alias+"]"]
			if !ok || row.Status != migrate.StatusPass {
				t.Errorf("missing PASS row %q: %+v", name+" ["+alias+"]", row)
			}
		}
		if _, ok := rows[name]; ok {
			t.Errorf("row %q is not per-source in a fan-in", name)
		}
	}
	for _, name := range []string{migrate.RowMirrorInstalled, migrate.RowCRReady, migrate.RowConnectorsRunning, migrate.RowReplicatedTopic, migrate.RowCutoverApplied, migrate.RowCutoverFroze} {
		if row, ok := rows[name]; !ok || row.Status != migrate.StatusPass {
			t.Errorf("missing PASS row %q: %+v", name, row)
		}
	}
	if !strings.Contains(rows[migrate.RowReplicatedTopic].Detail, "kates.orders.src282, kates.orders.src391") {
		t.Errorf("replicated topic row: %+v", rows[migrate.RowReplicatedTopic])
	}
	// The header states both sources and where the offset-syncs topic lives.
	if doc.Header["pair"] != "Kafka 2.8.2 + 3.9.1 → 4.3.0" {
		t.Errorf("header pair = %q", doc.Header["pair"])
	}
	if !strings.Contains(doc.Header["source"], "src282: legacy") || !strings.Contains(doc.Header["source"], "src391: legacy") {
		t.Errorf("header source = %q", doc.Header["source"])
	}
	if !strings.HasPrefix(doc.Header["offset-syncs"], "source — Kafka's default") {
		t.Errorf("header offset-syncs = %q", doc.Header["offset-syncs"])
	}
	// One producer run per source per phase (the corpus, then the
	// post-cutover records), each on that source's own pod.
	for _, alias := range []string{"src282", "src391"} {
		if n := len(f.find("kubectl -n kafka-" + lab + "-" + alias + " exec -i kates-migrate-" + lab + "-" + alias + " -c client -- /opt/kafka/bin/kafka-console-producer.sh")); n != 2 {
			t.Errorf("%s ran %d producers, want 2", alias, n)
		}
	}
}

func TestMigrateRunReadOnlySourceHeaderAndValues(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	scriptVerify(f, strings.Join(migrate.Records("m282-430", 200), "\n"))
	scriptRunDown(f)
	out := captureOutput(t, "json")

	// --keep so the generated values are still on disk to be read back; the
	// teardown removes them with the lab.
	flags := newPairFlags("2.8.2")
	flags.SkipBuild, flags.ReadOnlySource, flags.Keep = true, true, true
	if err := runMigrateRun(nil, flags); err != nil {
		t.Fatalf("run --read-only-source: %v\n%s", err, out.String())
	}
	doc := parseReport(t, out.String())
	if !strings.HasPrefix(doc.Header["offset-syncs"], "target — read-only source") || !strings.Contains(doc.Header["offset-syncs"], "Read + Describe only") {
		t.Fatalf("header offset-syncs = %q", doc.Header["offset-syncs"])
	}
	mirror, err := os.ReadFile(".build/migrate/m282-430/mirror-values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mirror), "readOnlySource: true") {
		t.Fatalf("mirror values lack readOnlySource:\n%s", mirror)
	}
}

func TestMigrateRunFailedRowExitsOne(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	// Only the first half of the corpus comes back off the target.
	scriptVerify(f, strings.Join(migrate.Records("m282-430", 200)[:100], "\n"))
	scriptRunDown(f)
	out := captureOutput(t, "json")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	err := runMigrateRun(nil, flags)
	if err == nil {
		t.Fatal("a failed row must make run fail")
	}
	if !strings.Contains(err.Error(), "2 assertion(s) failed") {
		t.Fatalf("error = %v", err)
	}
	doc := parseReport(t, out.String())
	if !doc.Summary.Failed || doc.Summary.Fail != 2 {
		t.Fatalf("summary: %+v", doc.Summary)
	}
	rows := map[string]migrate.Row{}
	for _, r := range doc.Rows {
		rows[r.Name] = r
	}
	if rows[migrate.RowRecordCount].Status != migrate.StatusFail || !strings.Contains(rows[migrate.RowRecordCount].Detail, "only 100 distinct of 200") {
		t.Fatalf("record count row: %+v", rows[migrate.RowRecordCount])
	}
	if rows[migrate.RowRecordContent].Status != migrate.StatusFail || !strings.Contains(rows[migrate.RowRecordContent].Detail, "missing from the target: kates.orders#101") {
		t.Fatalf("record content row: %+v", rows[migrate.RowRecordContent])
	}
	// The run went on to the translation and the cutover, as the scripts
	// did, and still cleaned up.
	if rows[migrate.RowOffsetTranslation].Status != migrate.StatusPass || rows[migrate.RowCutoverFroze].Status != migrate.StatusPass {
		t.Fatalf("later rows: %+v %+v", rows[migrate.RowOffsetTranslation], rows[migrate.RowCutoverFroze])
	}
	if !f.hasLine("helm uninstall mm2-m282-430 -n kafka") {
		t.Fatal("the lab was not removed after a failure")
	}
}

func TestMigrateRunKeepLeavesTheLab(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	scriptVerify(f, strings.Join(migrate.Records("m282-430", 200), "\n"))
	out := captureOutput(t, "table")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild, flags.Keep = true, true
	if err := runMigrateRun(nil, flags); err != nil {
		t.Fatalf("run --keep: %v\n%s", err, out.String())
	}
	if f.hasLine("helm uninstall") || f.hasLine("kubectl delete namespace") {
		t.Fatal("--keep must leave the lab in place")
	}
	if !f.hasLine("kubectl -n kafka delete pod kates-migrate-m282-430-tgt") {
		t.Fatal("--keep still removes the client pods")
	}
	text := stripAnsi(out.String())
	for _, want := range []string{"kates migrate status --name m282-430", "kates migrate down --name m282-430 --yes", "18 passed, 0 failed"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestMigrateRunStopsWhenTheTopicNeverAppears(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	scriptPrerequisites(f)
	scriptUp(f)
	scriptVerify(f, "")
	f.contains("exec kates-migrate-m282-430-tgt -c client -- /opt/kafka/bin/kafka-topics.sh", "__consumer_offsets\nsource.kates.orders")
	scriptRunDown(f)
	out := captureOutput(t, "json")

	flags := newPairFlags("2.8.2")
	flags.SkipBuild = true
	if err := runMigrateRun(nil, flags); err == nil {
		t.Fatal("run must fail when the replicated topic never appears")
	}
	doc := parseReport(t, out.String())
	last := doc.Rows[len(doc.Rows)-1]
	if last.Name != migrate.RowReplicatedTopic || last.Status != migrate.StatusFail || !strings.Contains(last.Detail, "kates.orders never appeared") {
		t.Fatalf("last row: %+v", last)
	}
	if f.hasLine("helm upgrade mm2-m282-430 charts/mirror-maker2 -n kafka --reuse-values") {
		t.Fatal("no cutover after the topic wait failed")
	}
	if !f.hasLine("helm uninstall mm2-m282-430 -n kafka") {
		t.Fatal("the lab must still be removed")
	}
	// The wait polled until the budget ran out, on the fake clock.
	if n := len(f.find("kubectl -n kafka exec kates-migrate-m282-430-tgt -c client -- /opt/kafka/bin/kafka-topics.sh")); n < 40 {
		t.Fatalf("the topic wait polled %d times, want the whole 600s budget at 15s", n)
	}
}

func TestMigrateVerifyAgainstTheLab(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCluster(f, true)
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab=m282-430 -o json", fakeLabList("m282-430"))
	f.on(`kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, "m282-430")
	scriptVerify(f, strings.Join(migrate.Records("m282-430", 200), "\n"))
	out := captureOutput(t, "json")

	flags := &migrateLabFlags{Name: "m282-430", Timeout: 600, Messages: 200, TargetCluster: "krafter", TargetNamespace: "kafka"}
	if err := runMigrateVerify(nil, flags); err != nil {
		t.Fatalf("verify: %v\n%s", err, out.String())
	}
	doc := parseReport(t, out.String())
	want := []string{migrate.RowCorpusProduced, migrate.RowSourceEndOffsets, migrate.RowSourceConsumerGroup, migrate.RowReplicatedTopic, migrate.RowRecordCount, migrate.RowRecordContent, migrate.RowOffsetTranslation}
	if len(doc.Rows) != len(want) {
		t.Fatalf("rows: %+v", doc.Rows)
	}
	for i, name := range want {
		if doc.Rows[i].Name != name || doc.Rows[i].Status != migrate.StatusPass {
			t.Fatalf("row %d = %+v, want PASS %q", i, doc.Rows[i], name)
		}
	}
	if !strings.Contains(doc.Rows[6].Detail, "kates.orders/2=30 (source 33)") || !strings.Contains(doc.Rows[6].Detail, "trail the source's position") {
		t.Fatalf("translation detail: %s", doc.Rows[6].Detail)
	}
	if f.hasLine("helm upgrade") || f.hasLine("helm uninstall") {
		t.Fatal("verify neither installs nor removes anything")
	}
	if !f.hasLine("kubectl -n kafka-m282-430-src delete pod kates-migrate-m282-430-src") {
		t.Fatal("verify removes its client pods")
	}
}
