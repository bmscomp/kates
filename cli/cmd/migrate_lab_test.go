package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
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
	f.on("kubectl get kafkamirrormaker2 -A -o json", `{"items":[]}`)
	f.on("kubectl -n kafka apply -f -", "kafkatopic.kafka.strimzi.io/m282-430-kates-orders created")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab=m282-430 --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab=m282-430 --ignore-not-found", "")
	f.on("kubectl delete namespace kafka-m282-430-src --ignore-not-found --timeout=600s", "")
	// down's data guard: the lab's client pod on the target lists the
	// topics and reads their end offsets — empty unless a test says so.
	scriptPods(f)
	scriptTargetTopics(f, "m282-430", map[string]int64{"kates.orders": 0}, "__consumer_offsets", "kates-events")
}

// topicApplies are the applies of KafkaTopic manifests (the client pod is
// applied through the same command).
func topicApplies(f *fakeProc) []fakeCall {
	var out []fakeCall
	for _, c := range f.find("kubectl -n kafka apply -f -") {
		if strings.Contains(c.stdin, "kind: KafkaTopic") {
			out = append(out, c)
		}
	}
	return out
}

// scriptTargetTopics scripts the lab's client pod on the target: the topic
// listing (the topics given, plus extra names) and each topic's end
// offsets, all on partition 0. A topic absent from offsets has none to read.
func scriptTargetTopics(f *fakeProc, lab string, offsets map[string]int64, extra ...string) {
	pod := "exec kates-migrate-" + lab + "-tgt -c client -- "
	listed := append([]string(nil), extra...)
	for t := range offsets {
		listed = append(listed, t)
	}
	f.rule(func(l string) bool {
		return strings.Contains(l, pod) && strings.Contains(l, "kafka-topics.sh") && strings.Contains(l, "--list")
	},
		func([]string, string) (string, error) { return strings.Join(listed, "\n"), nil })
	f.rule(func(l string) bool { return strings.Contains(l, pod) && strings.Contains(l, "kafka-get-offsets.sh") },
		func(args []string, _ string) (string, error) {
			for i, a := range args {
				if a == "--topic" && i+1 < len(args) {
					if n, ok := offsets[args[i+1]]; ok {
						return fmt.Sprintf("%s:0:%d", args[i+1], n), nil
					}
				}
			}
			return "", fmt.Errorf("no such topic")
		})
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
	applies := topicApplies(f)
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

// mirrorItem is a KafkaMirrorMaker2 as kubectl lists it across namespaces.
func mirrorItem(namespace, name, targetBootstrap, groupID string, aliases ...string) string {
	var mirrors []string
	for _, a := range aliases {
		mirrors = append(mirrors, fmt.Sprintf(`{"source":{"alias":%q,"bootstrapServers":"src:9092"},"topicsPattern":"other"}`, a))
	}
	return fmt.Sprintf(`{"kind":"KafkaMirrorMaker2","metadata":{"name":%q,"namespace":%q},
	  "spec":{"target":{"alias":"target","bootstrapServers":%q,"groupId":%q},"mirrors":[%s]}}`,
		name, namespace, targetBootstrap, groupID, strings.Join(mirrors, ","))
}

// Every single-source lab, and the loopback mirror kates deploy installs,
// checkpoints under the alias source: down deleted source.checkpoints.internal
// while another mirror on the same target still wrote it.
func TestMigrateDownKeepsTheTopicsAnotherMirrorWrites(t *testing.T) {
	const krafter = "krafter-kafka-bootstrap.kafka.svc.cluster.local:9092"
	own := mirrorItem("kafka", "mm2-m282-430-mirror-maker2", krafter, "mm2-m282-430", "source") // still terminating
	for _, tc := range []struct {
		name    string
		others  []string
		kept    []string
		deleted []string
	}{
		{
			// A short bootstrap names no namespace, so the loopback counts as
			// writing here; a mirror on another cluster shares nothing.
			name: "the loopback's checkpoints",
			others: []string{
				mirrorItem("kafka", "mirror-maker2", "krafter-kafka-bootstrap:9092", "mirror-maker2", "source"),
				mirrorItem("other", "elsewhere", "other-kafka-bootstrap.other.svc:9092", "mm2-m282-430", "source"),
			},
			kept:    []string{"source.checkpoints.internal"},
			deleted: []string{"kates.orders", "mm2-m282-430-configs", "mm2-m282-430-offsets", "mm2-m282-430-status"},
		},
		{
			name:    "another mirror's group",
			others:  []string{mirrorItem("dr", "mm2-dr-mirror-maker2", krafter, "mm2-m282-430", "east")},
			kept:    []string{"mm2-m282-430-configs", "mm2-m282-430-offsets", "mm2-m282-430-status"},
			deleted: []string{"kates.orders", "source.checkpoints.internal"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			migrateTestRepo(t)
			f := newFakeProc(t)
			scriptDown(f, true)
			f.on("kubectl get kafkamirrormaker2 -A -o json", `{"items":[`+strings.Join(append([]string{own}, tc.others...), ",")+`]}`)
			captureOutput(t, "table")

			migrateDownName, migrateDownYes = "m282-430", true
			t.Cleanup(func() { migrateDownName, migrateDownYes = "", false })
			if err := runMigrateDown(context.Background()); err != nil {
				t.Fatalf("down: %v", err)
			}
			manifest := topicApplies(f)[0].stdin
			for _, topic := range tc.kept {
				if strings.Contains(manifest, "topicName: "+topic+"\n") {
					t.Errorf("down deleted %s, which another mirror writes:\n%s", topic, manifest)
				}
			}
			for _, topic := range tc.deleted {
				if !strings.Contains(manifest, "topicName: "+topic+"\n") {
					t.Errorf("KafkaTopic manifests lack %s:\n%s", topic, manifest)
				}
			}
		})
	}
}

// Listing the other mirrors fails closed: down keeps every topic of the
// mirror's own, deletes the mirrored ones, and says the lab was not fully
// removed.
func TestMigrateDownKeepsTheMirrorsTopicsWhenMirrorsCannotBeListed(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	f.fail("kubectl get kafkamirrormaker2 -A -o json", "forbidden")
	captureOutput(t, "table")

	migrateDownName, migrateDownYes = "m282-430", true
	t.Cleanup(func() { migrateDownName, migrateDownYes = "", false })
	err := runMigrateDown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "kept the mirror's own topics") {
		t.Fatalf("want down to report the kept topics, got %v", err)
	}
	manifest := topicApplies(f)[0].stdin
	if !strings.Contains(manifest, "topicName: kates.orders") {
		t.Errorf("the mirrored topic must still be deleted:\n%s", manifest)
	}
	for _, kept := range []string{"mm2-m282-430-configs", "source.checkpoints.internal"} {
		if strings.Contains(manifest, kept) {
			t.Errorf("down deleted %s although the other mirrors were not listed", kept)
		}
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
	f.on("kubectl get kafkamirrormaker2 -A -o json", `{"items":[]}`)
	f.on("kubectl -n kafka apply -f -", "created")
	f.on("kubectl -n kafka delete kafkatopic -l kates.io/lab="+lab+" --ignore-not-found --timeout=120s", "")
	f.on("kubectl -n kafka delete secret -l kates.io/lab="+lab+" --ignore-not-found", "")
	f.on("kubectl -n kafka get kafka krafter -o jsonpath=", "4.3.0 True")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-"+lab+"-mirror-maker2 -o json", mirrorCRJSON("mm2-"+lab+"-mirror-maker2", "RUNNING", "RUNNING"))
	scriptPods(f)
	scriptTargetTopics(f, lab, map[string]int64{"kates.orders.src282": 0, "kates.orders.src391": 0})
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
	applies := topicApplies(f)
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
}

// A sanitised name carries a short hash of the original: a.b, a-b and A_B
// used to map to one KafkaTopic, the second apply overwriting the first. A
// name sanitising did not touch stays as it was.
func TestKafkaTopicResourceNameDoesNotCollide(t *testing.T) {
	if name := kafkaTopicResourceName("m282-430", "orders"); name != "m282-430-orders" {
		t.Errorf("an already-valid name changed: %s", name)
	}
	sum := sha256.Sum256([]byte("source.checkpoints.internal"))
	if want, got := "m282-430-source-checkpoints-internal-"+hex.EncodeToString(sum[:3]), kafkaTopicResourceName("m282-430", "source.checkpoints.internal"); got != want {
		t.Errorf("resource name = %s, want %s", got, want)
	}
	seen := map[string]string{}
	for _, topic := range []string{"a.b", "a-b", "A_B", "a..b", strings.Repeat("x", 70) + "1", strings.Repeat("x", 70) + "2"} {
		name := kafkaTopicResourceName("lab", topic)
		if len(name) > 63 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(name) {
			t.Errorf("%q → %q is not a DNS-1123 label", topic, name)
		}
		if other, dup := seen[name]; dup {
			t.Errorf("%q and %q share the name %s", topic, other, name)
		}
		seen[name] = topic
	}
}

// The topics come from the kates.io/lab-topics annotation up wrote, so a
// mirror installed with a real regular expression (through --values-mirror)
// still names its topics; topicsPattern is the fallback for a CR without it.
// In a fan-in each leg takes the recorded names its own pattern matches.
func TestReadMirrorSpecPrefersTheRecordedTopics(t *testing.T) {
	spec := json.RawMessage(`{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc:9092","groupId":"mm2-x"},
		"mirrors":[{"source":{"alias":"source","bootstrapServers":"src:9092"},"topicsPattern":"kates\\..*"}]}`)
	d := &labDiscovery{}
	d.readMirrorSpec(spec, "")
	if len(d.Topics) != 0 {
		t.Fatalf("a regex pattern without the annotation should name no topic, got %v", d.Topics)
	}
	d = &labDiscovery{}
	d.readMirrorSpec(spec, "kates.orders, kates.payments")
	if strings.Join(d.Topics, ",") != "kates.orders,kates.payments" || strings.Join(d.legs[0].Topics, ",") != "kates.orders,kates.payments" {
		t.Fatalf("recorded topics not read back: %v / %v", d.Topics, d.legs[0].Topics)
	}

	fanIn := json.RawMessage(`{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc:9092","groupId":"mm2-x"},
		"mirrors":[{"source":{"alias":"src282","bootstrapServers":"a:9092"},"topicsPattern":"kates\\.orders\\.src282"},
		           {"source":{"alias":"src391","bootstrapServers":"b:9092"},"topicsPattern":"kates\\.orders\\.src391"}]}`)
	d = &labDiscovery{}
	d.readMirrorSpec(fanIn, "kates.orders.src282,kates.orders.src391")
	if strings.Join(d.legs[0].Topics, ",") != "kates.orders.src282" || strings.Join(d.legs[1].Topics, ",") != "kates.orders.src391" {
		t.Fatalf("legs did not each take their own recorded topics: %v / %v", d.legs[0].Topics, d.legs[1].Topics)
	}
}

// SASL is read from the CR's authentication block for the source cluster,
// not from the port: a SASL listener on 9092 is SASL, a plaintext one on
// 9094 is not. A CR that lists no clusters falls back to the port.
func TestLabSourceSASLFromTheCRsAuthentication(t *testing.T) {
	withClusters := func(bootstrap, auth string) *labDiscovery {
		t.Helper()
		spec := json.RawMessage(fmt.Sprintf(`{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc:9092","groupId":"g"},
			"clusters":[{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc:9092"},{"alias":"source","bootstrapServers":%q%s}],
			"mirrors":[{"source":{"alias":"source","bootstrapServers":%q},"topicsPattern":"kates\\.orders"}]}`, bootstrap, auth, bootstrap))
		d := &labDiscovery{}
		d.readMirrorSpec(spec, "")
		d.Sources = orderLabSources([]*labSource{{Release: "lab-src", Namespace: "kafka-lab-src"}}, d.legs)
		return d
	}
	plainAuth := `,"authentication":{"type":"plain","username":"mm2reader","passwordSecret":{"secretName":"lab-src-mm2reader","password":"password"}}`

	if d := withClusters("lab-src-legacy-kafka-bootstrap.kafka-lab-src.svc:9092", plainAuth); !d.Sources[0].usesSASL() || d.Sources[0].Auth.Secret != "lab-src-mm2reader" || d.Sources[0].Auth.User != "mm2reader" {
		t.Errorf("a SASL block on 9092 was not read as SASL: %+v", d.Sources[0].Auth)
	}
	if d := withClusters("lab-src-legacy-kafka-bootstrap.kafka-lab-src.svc:9094", ""); d.Sources[0].usesSASL() {
		t.Errorf("no authentication block on 9094 was read as SASL: %+v", d.Sources[0].Auth)
	}
	noClusters := &labDiscovery{}
	noClusters.readMirrorSpec(json.RawMessage(`{"target":{"alias":"target","bootstrapServers":"krafter-kafka-bootstrap.kafka.svc:9092"},
		"mirrors":[{"source":{"alias":"source","bootstrapServers":"src:9094"},"topicsPattern":"kates\\.orders"}]}`), "")
	noClusters.Sources = orderLabSources([]*labSource{{Release: "src", Namespace: "ns"}}, noClusters.legs)
	if !noClusters.Sources[0].usesSASL() {
		t.Error("a CR listing no clusters should fall back to the port")
	}

	// mirror deploy reads the same answer off the source's bootstrap Service.
	sasl := []servicePort{{Name: "plaintext", Port: 9092}, {Name: "sasl", Port: 9095}}
	if !saslBootstrap("src.ns.svc:9095", sasl) || saslBootstrap("src.ns.svc:9092", sasl) || saslBootstrap("src.ns.svc:9094", sasl) {
		t.Error("saslBootstrap should follow the Service's sasl port, whatever its number")
	}
	if !saslBootstrap("src.ns.svc:9094", nil) || saslBootstrap("src.ns.svc:9092", nil) {
		t.Error("without ports, saslBootstrap should fall back to 9094")
	}
}
