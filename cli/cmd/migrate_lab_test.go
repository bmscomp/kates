package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// fakeLabList is what kubectl lists for the m282-430 lab's label: the
// mirror CR, the broker and ZooKeeper StatefulSets. A second lab's items are
// appended by the tests that need them.
func fakeLabList(labs ...string) string {
	var items []string
	for _, lab := range labs {
		items = append(items,
			fmt.Sprintf(`{"kind":"KafkaMirrorMaker2","metadata":{"name":"mm2-%[1]s-mirror-maker2","namespace":"kafka",
			  "labels":{"app.kubernetes.io/instance":"mm2-%[1]s","kates.io/lab":"%[1]s","kates.io/lab-role":"mirror"},
			  "annotations":{"meta.helm.sh/release-name":"mm2-%[1]s"}},
			  "spec":{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc.cluster.local:9092","groupId":"mm2-%[1]s"},
			          "mirrors":[{"source":{"alias":"source","bootstrapServers":"%[1]s-src-legacy-kafka-bootstrap.kafka-%[1]s-src.svc.cluster.local:9092"},
			                      "topicsPattern":"kates\\.orders","sourceConnector":{"config":{"replication.policy.class":"org.apache.kafka.connect.mirror.IdentityReplicationPolicy"}}}]}}`, lab),
			fmt.Sprintf(`{"kind":"StatefulSet","metadata":{"name":"%[1]s-src-legacy-kafka","namespace":"kafka-%[1]s-src",
			  "labels":{"app.kubernetes.io/instance":"%[1]s-src","app.kubernetes.io/component":"broker","app.kubernetes.io/version":"2.8.2","kates.io/kafka-mode":"zookeeper","kates.io/lab":"%[1]s","kates.io/lab-role":"source"}},"spec":{}}`, lab),
			fmt.Sprintf(`{"kind":"StatefulSet","metadata":{"name":"%[1]s-src-legacy-kafka-zookeeper","namespace":"kafka-%[1]s-src",
			  "labels":{"app.kubernetes.io/instance":"%[1]s-src","app.kubernetes.io/component":"zookeeper","kates.io/lab":"%[1]s","kates.io/lab-role":"source"}},"spec":{}}`, lab),
		)
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

// scriptDown scripts the deletions of the m282-430 lab.
func scriptDown(f *fakeProc, namespaceOwned bool) {
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab=m282-430 -o json", fakeLabList("m282-430"))
	owner := ""
	if namespaceOwned {
		owner = "m282-430"
	}
	f.on(`kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, owner)
	f.on("kubectl delete pod -A -l kates.io/lab=m282-430", "")
	f.on("helm uninstall mm2-m282-430 -n kafka", "release \"mm2-m282-430\" uninstalled")
	f.on("kubectl -n kafka delete kafkamirrormaker2 mm2-m282-430-mirror-maker2 --ignore-not-found --timeout=60s", "")
	f.on("helm uninstall m282-430-src -n kafka-m282-430-src", "release \"m282-430-src\" uninstalled")
	f.on("kubectl -n kafka apply -f -", "kafkatopic.kafka.strimzi.io/m282-430-kates-orders created")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab=m282-430 --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab=m282-430 --ignore-not-found", "")
	f.on("kubectl delete namespace kafka-m282-430-src --ignore-not-found --timeout=600s", "")
}

func TestMigrateDownTouchesOnlyTheLabelledLab(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	captureOutput(t, "table")

	migrateDownName, migrateDownYes, migrateDownTimeout = "m282-430", true, 600
	t.Cleanup(func() { migrateDownName, migrateDownYes = "", false })
	if err := runMigrateDown(nil); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, want := range []string{
		"helm uninstall mm2-m282-430 -n kafka",
		"kubectl -n kafka delete kafkamirrormaker2 mm2-m282-430-mirror-maker2 --ignore-not-found --timeout=60s",
		"helm uninstall m282-430-src -n kafka-m282-430-src",
		"kubectl -n kafka delete kafkatopic -l kates.io/lab=m282-430",
		"kubectl -n kafka delete secret -l kates.io/lab=m282-430",
		"kubectl delete pod -A -l kates.io/lab=m282-430",
		"kubectl delete namespace kafka-m282-430-src --ignore-not-found --timeout=600s",
	} {
		if !f.hasLine(want) {
			t.Errorf("down did not run %q; ran:\n  %s", want, strings.Join(f.lines(), "\n  "))
		}
	}
	// Every deletion names the lab; the primary is never touched.
	for _, l := range f.lines() {
		if strings.Contains(l, "delete") || strings.Contains(l, "uninstall") {
			if !strings.Contains(l, "m282-430") {
				t.Errorf("a deletion does not name the lab: %q", l)
			}
			if strings.Contains(l, "krafter") || strings.Contains(l, "strimzi-operator") {
				t.Errorf("down touched the primary: %q", l)
			}
		}
	}
	// The lab's topics on the target are adopted as labelled KafkaTopics,
	// the mirrored one and the mirror's own, then deleted.
	applies := f.find("kubectl -n kafka apply -f -")
	if len(applies) != 1 {
		t.Fatalf("want one KafkaTopic apply, got %d", len(applies))
	}
	manifest := applies[0].stdin
	for _, want := range []string{"topicName: kates.orders", "topicName: mm2-m282-430-configs", "topicName: mm2-m282-430-offsets", "topicName: mm2-m282-430-status", "topicName: source.checkpoints.internal", "kates.io/lab: m282-430", "strimzi.io/cluster: krafter"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("KafkaTopic manifests lack %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "kates-events") {
		t.Error("down must not adopt the primary's own topics")
	}
}

func TestMigrateDownLeavesAForeignNamespace(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, false)
	captureOutput(t, "table")

	migrateDownName, migrateDownYes = "m282-430", true
	t.Cleanup(func() { migrateDownName, migrateDownYes = "", false })
	if err := runMigrateDown(nil); err != nil {
		t.Fatalf("down: %v", err)
	}
	if f.hasLine("kubectl delete namespace") {
		t.Fatal("a namespace the lab did not create must be left in place")
	}
	if !f.hasLine("helm uninstall m282-430-src -n kafka-m282-430-src") {
		t.Fatal("the source release must still be uninstalled")
	}
}

func TestMigrateDownNeedsANameWithSeveralLabs(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab -o json", fakeLabList("m282-430", "m391-430"))
	captureOutput(t, "table")

	migrateDownName, migrateDownYes = "", true
	t.Cleanup(func() { migrateDownYes = false })
	err := runMigrateDown(nil)
	if err == nil || !strings.Contains(err.Error(), "pass --name") || !strings.Contains(err.Error(), "m282-430, m391-430") {
		t.Fatalf("want the ambiguity refusal, got %v", err)
	}
	for _, l := range f.lines() {
		if strings.Contains(l, "delete") || strings.Contains(l, "uninstall") {
			t.Fatalf("nothing may be removed: %q", l)
		}
	}
}

func TestMigrateDownRefusesWithoutConsent(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	captureOutput(t, "table")

	migrateDownName, migrateDownYes = "m282-430", false
	t.Cleanup(func() { migrateDownName = "" })
	err := runMigrateDown(nil)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without a terminal down must refuse and name --yes, got %v", err)
	}
	if f.hasLine("helm uninstall") {
		t.Fatal("nothing may be removed without consent")
	}
}

func TestMigrateStatusJSON(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab -o json", fakeLabList("m282-430"))
	f.on(`kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, "m282-430")
	f.on("kubectl -n kafka-m282-430-src get statefulset m282-430-src-legacy-kafka -o jsonpath={.status.readyReplicas}/{.spec.replicas}", "1/1")
	f.on("kubectl -n kafka get kafka krafter -o jsonpath=", "4.3.0 True")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json", mirrorCRJSON("mm2-m282-430-mirror-maker2", "STOPPED", "RUNNING"))
	out := captureOutput(t, "json")

	migrateStatusName, migrateStatusOffsets, migrateStatusWatch = "", false, false
	if err := runMigrateStatus(nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	var st labStatus
	if err := json.Unmarshal([]byte(out.String()), &st); err != nil {
		t.Fatalf("status -o json: %v\n%s", err, out.String())
	}
	if st.Lab != "m282-430" || st.Source.Version != "2.8.2" || st.Source.Mode != "zookeeper" || st.Source.Ready != "1/1 ready" {
		t.Fatalf("source: %+v", st.Source)
	}
	if st.Target.Version != "4.3.0" || st.Mirror.Policy != migrate.PolicyIdentity || !st.Mirror.Ready {
		t.Fatalf("target/mirror: %+v %+v", st.Target, st.Mirror)
	}
	if len(st.Mirror.Connectors) != 2 || st.Mirror.Connectors[0].State != "STOPPED" {
		t.Fatalf("connectors: %+v", st.Mirror.Connectors)
	}
}

// fakeFanInLabList is what kubectl lists for a two-source lab: one mirror
// with two legs, and one broker StatefulSet per source, each labelled with
// its alias.
func fakeFanInLabList() string {
	const lab = "m282-391-430"
	leg := func(alias, version string) string {
		return fmt.Sprintf(`{"source":{"alias":"%[1]s","bootstrapServers":"%[2]s-%[1]s-legacy-kafka-bootstrap.kafka-%[2]s-%[1]s.svc.cluster.local:9092"},
		  "topicsPattern":"kates\\.orders\\.%[1]s","sourceConnector":{"config":{"replication.policy.class":"org.apache.kafka.connect.mirror.IdentityReplicationPolicy"}}}`, alias, lab)
	}
	sts := func(alias, version, mode string) string {
		return fmt.Sprintf(`{"kind":"StatefulSet","metadata":{"name":"%[2]s-%[1]s-legacy-kafka","namespace":"kafka-%[2]s-%[1]s",
		  "labels":{"app.kubernetes.io/instance":"%[2]s-%[1]s","app.kubernetes.io/component":"broker","app.kubernetes.io/version":"%[3]s",
		            "kates.io/kafka-mode":"%[4]s","kates.io/lab":"%[2]s","kates.io/lab-role":"source","kates.io/lab-source":"%[1]s"}},"spec":{}}`, alias, lab, version, mode)
	}
	mirror := fmt.Sprintf(`{"kind":"KafkaMirrorMaker2","metadata":{"name":"mm2-%[1]s-mirror-maker2","namespace":"kafka",
	  "labels":{"app.kubernetes.io/instance":"mm2-%[1]s","kates.io/lab":"%[1]s","kates.io/lab-role":"mirror"}},
	  "spec":{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc.cluster.local:9092","groupId":"mm2-%[1]s"},
	          "mirrors":[%s,%s]}}`, lab, leg("src282", "2.8.2"), leg("src391", "3.9.1"))
	return `{"items":[` + strings.Join([]string{mirror, sts("src282", "2.8.2", "zookeeper"), sts("src391", "3.9.1", "kraft")}, ",") + `]}`
}

func TestMigrateDownAndStatusAcrossSeveralSources(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	const lab = "m282-391-430"
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab="+lab+" -o json", fakeFanInLabList())
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab -o json", fakeFanInLabList())
	for _, alias := range []string{"src282", "src391"} {
		ns := "kafka-" + lab + "-" + alias
		f.on(`kubectl get namespace `+ns+` --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, lab)
		f.on("helm uninstall "+lab+"-"+alias+" -n "+ns, "uninstalled")
		f.on("kubectl delete namespace "+ns+" --ignore-not-found --timeout=600s", "")
		f.on("kubectl -n "+ns+" get statefulset "+lab+"-"+alias+"-legacy-kafka -o jsonpath={.status.readyReplicas}/{.spec.replicas}", "1/1")
	}
	f.on("kubectl delete pod -A -l kates.io/lab="+lab, "")
	f.on("helm uninstall mm2-"+lab+" -n kafka", "uninstalled")
	f.on("kubectl -n kafka delete kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 --ignore-not-found --timeout=60s", "")
	f.on("kubectl -n kafka apply -f -", "created")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab="+lab+" --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab="+lab+" --ignore-not-found", "")
	f.on("kubectl -n kafka get kafka krafter -o jsonpath=", "4.3.0 True")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 -o json", mirrorCRJSON("mm2-"+lab+"-mirror-maker2", "RUNNING", "RUNNING"))
	out := captureOutput(t, "json")

	// status finds both legs by the lab label alone.
	migrateStatusName, migrateStatusOffsets, migrateStatusWatch = "", false, false
	if err := runMigrateStatus(nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	var st labStatus
	if err := json.Unmarshal([]byte(out.String()), &st); err != nil {
		t.Fatalf("status -o json: %v\n%s", err, out.String())
	}
	if len(st.Sources) != 2 {
		t.Fatalf("status sources: %+v", st.Sources)
	}
	if st.Sources[0].Alias != "src282" || st.Sources[0].Version != "2.8.2" || st.Sources[1].Alias != "src391" || st.Sources[1].Version != "3.9.1" {
		t.Fatalf("status sources: %+v", st.Sources)
	}
	if strings.Join(st.Mirror.Topics, ",") != "kates.orders.src282,kates.orders.src391" {
		t.Fatalf("status topics: %v", st.Mirror.Topics)
	}

	// down removes both sources and both namespaces.
	migrateDownName, migrateDownYes, migrateDownTimeout = lab, true, 600
	t.Cleanup(func() { migrateDownName, migrateDownYes = "", false })
	if err := runMigrateDown(nil); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, want := range []string{
		"helm uninstall mm2-" + lab + " -n kafka",
		"helm uninstall " + lab + "-src282 -n kafka-" + lab + "-src282",
		"helm uninstall " + lab + "-src391 -n kafka-" + lab + "-src391",
		"kubectl delete namespace kafka-" + lab + "-src282",
		"kubectl delete namespace kafka-" + lab + "-src391",
	} {
		if !f.hasLine(want) {
			t.Errorf("down did not run %q; ran:\n  %s", want, strings.Join(f.lines(), "\n  "))
		}
	}
	// Both legs' topics and both checkpoints topics are adopted and deleted.
	applies := f.find("kubectl -n kafka apply -f -")
	if len(applies) != 1 {
		t.Fatalf("want one KafkaTopic apply, got %d", len(applies))
	}
	for _, want := range []string{"topicName: kates.orders.src282", "topicName: kates.orders.src391", "topicName: src282.checkpoints.internal", "topicName: src391.checkpoints.internal"} {
		if !strings.Contains(applies[0].stdin, want) {
			t.Errorf("KafkaTopic manifests lack %q:\n%s", want, applies[0].stdin)
		}
	}
}

func TestMigrateTopicsFromPattern(t *testing.T) {
	got := topicsFromPattern(`kates\.orders|kates\.pay-ments`)
	if strings.Join(got, ",") != "kates.orders,kates.pay-ments" {
		t.Fatalf("topicsFromPattern = %v", got)
	}
	if got := topicsFromPattern(`kates\..*`); len(got) != 0 {
		t.Fatalf("a real regex must yield no topic, got %v", got)
	}
	if name := kafkaTopicResourceName("m282-430", "source.checkpoints.internal"); name != "m282-430-source-checkpoints-internal" {
		t.Fatalf("resource name = %s", name)
	}
}
