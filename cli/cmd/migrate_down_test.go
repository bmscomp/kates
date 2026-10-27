package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// downFlags sets down's flags for one test.
func downFlags(t *testing.T, yes, keepTopics, deleteData bool) {
	t.Helper()
	migrateDownName, migrateDownYes, migrateDownTimeout = "m282-430", yes, 600
	migrateDownKeepTopics, migrateDownDeleteData = keepTopics, deleteData
	t.Cleanup(func() {
		migrateDownName, migrateDownYes = "", false
		migrateDownKeepTopics, migrateDownDeleteData = false, false
	})
}

// withPrompt makes the CLI interactive and answers its prompts in order,
// recording the questions.
func withPrompt(t *testing.T, answers ...bool) *[]string {
	t.Helper()
	prevInteractive, prevConfirm := interactiveAllowedFn, confirmFn
	t.Cleanup(func() { interactiveAllowedFn, confirmFn = prevInteractive, prevConfirm })
	var asked []string
	interactiveAllowedFn = func() bool { return true }
	confirmFn = func(q string) (bool, error) {
		asked = append(asked, q)
		if len(asked) > len(answers) {
			t.Errorf("unexpected prompt %q", q)
			return false, nil
		}
		return answers[len(asked)-1], nil
	}
	return &asked
}

// mirroredTopicDeleted says down's KafkaTopic manifests adopted topic.
func mirroredTopicDeleted(f *fakeProc, topic string) bool {
	for _, c := range topicApplies(f) {
		if strings.Contains(c.stdin, "topicName: "+topic+"\n") {
			return true
		}
	}
	return false
}

// --keep-topics removes the lab and leaves its mirrored topic, without
// looking at the target at all; the mirror's own topics still go.
func TestMigrateDownKeepTopics(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	captureOutput(t, "table")
	downFlags(t, true, true, false)

	if err := runMigrateDown(context.Background()); err != nil {
		t.Fatalf("down --keep-topics: %v", err)
	}
	if mirroredTopicDeleted(f, "kates.orders") {
		t.Fatal("--keep-topics deleted the mirrored topic")
	}
	if !mirroredTopicDeleted(f, "mm2-m282-430-configs") {
		t.Error("the mirror's own topics should still be removed")
	}
	if f.hasLine("kubectl -n kafka exec kates-migrate-m282-430-tgt") {
		t.Error("--keep-topics has nothing to check on the target")
	}
	if !f.hasLine("helm uninstall mm2-m282-430 -n kafka") || !f.hasLine("helm uninstall m282-430-src -n kafka-m282-430-src") {
		t.Error("the lab itself must still be removed")
	}
}

// The plan's rule: one --yes no longer deletes user data. A mirrored topic
// holding records stops down before anything is removed, naming the topic,
// its record count and both ways out.
func TestMigrateDownRefusesNonEmptyTopicsWithYesAlone(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	scriptTargetTopics(f, "m282-430", map[string]int64{"kates.orders": 120}, "__consumer_offsets")
	captureOutput(t, "table")
	downFlags(t, true, false, false)

	err := runMigrateDown(context.Background())
	if err == nil {
		t.Fatal("down --yes deleted a topic holding records")
	}
	for _, want := range []string{"kates.orders (120 records)", "--delete-data", "--keep-topics"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	if f.hasLine("helm uninstall") || f.hasLine("kubectl -n kafka delete kafkatopic") || len(topicApplies(f)) > 0 {
		t.Fatalf("a refusal must remove nothing; ran:\n  %s", strings.Join(f.lines(), "\n  "))
	}
	if !f.hasLine("kubectl -n kafka delete pod kates-migrate-m282-430-tgt") {
		t.Error("the check's client pod must be removed")
	}
}

// --delete-data is the explicit consent: the topic holding records goes.
func TestMigrateDownDeleteDataDeletesNonEmptyTopics(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	scriptTargetTopics(f, "m282-430", map[string]int64{"kates.orders": 120})
	captureOutput(t, "table")
	downFlags(t, true, false, true)

	if err := runMigrateDown(context.Background()); err != nil {
		t.Fatalf("down --yes --delete-data: %v", err)
	}
	if !mirroredTopicDeleted(f, "kates.orders") {
		t.Fatal("--delete-data did not delete the topic")
	}
}

// Empty mirrored topics need nothing more than --yes, but they were looked
// at: their end offsets were read first.
func TestMigrateDownDeletesEmptyTopicsAfterReadingThem(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	captureOutput(t, "table")
	downFlags(t, true, false, false)

	if err := runMigrateDown(context.Background()); err != nil {
		t.Fatalf("down: %v", err)
	}
	read := f.indexOf("kubectl -n kafka exec kates-migrate-m282-430-tgt -c client -- /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc.cluster.local:9092 --topic kates.orders")
	uninstall := f.indexOf("helm uninstall mm2-m282-430")
	if read < 0 || uninstall < 0 || read > uninstall {
		t.Fatalf("the end offsets must be read before anything is removed (read at %d, uninstall at %d)", read, uninstall)
	}
	if !mirroredTopicDeleted(f, "kates.orders") {
		t.Fatal("an empty mirrored topic should be deleted")
	}
}

// A target down cannot look at fails closed: every mirrored topic counts as
// unchecked.
func TestMigrateDownRefusesWhenTheTopicsCannotBeChecked(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	f.fail("kubectl -n kafka get secret kates-mm2 -o jsonpath={.data.password}", "forbidden")
	captureOutput(t, "table")
	downFlags(t, true, false, false)

	err := runMigrateDown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "kates.orders") || !strings.Contains(err.Error(), "--delete-data") {
		t.Fatalf("want a refusal naming the unchecked topic, got %v", err)
	}
	if f.hasLine("helm uninstall") {
		t.Fatal("a refusal must remove nothing")
	}
}

// On a terminal without --yes, the second confirmation names the topic and
// its records; declining it removes nothing, accepting it deletes the topic.
func TestMigrateDownAsksAgainForTopicsWithData(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(fmt.Sprintf("accept=%v", accept), func(t *testing.T) {
			migrateTestRepo(t)
			f := newFakeProc(t)
			scriptDown(f, true)
			scriptTargetTopics(f, "m282-430", map[string]int64{"kates.orders": 7})
			captureOutput(t, "table")
			downFlags(t, false, false, false)
			asked := withPrompt(t, true, accept)

			err := runMigrateDown(context.Background())
			if len(*asked) != 2 || !strings.Contains((*asked)[1], "kates.orders (7 records)") {
				t.Fatalf("want a second prompt naming kates.orders, got %q", *asked)
			}
			if accept {
				if err != nil || !mirroredTopicDeleted(f, "kates.orders") {
					t.Fatalf("an accepted second prompt should delete the topic: %v", err)
				}
				return
			}
			if err == nil || f.hasLine("helm uninstall") {
				t.Fatalf("a declined second prompt must remove nothing (err %v)", err)
			}
		})
	}
}

// -o json never prompts: without --yes it refuses and names the flag.
func TestMigrateDownJSONDoesNotPrompt(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptDown(f, true)
	captureOutput(t, "json")
	downFlags(t, false, false, false)
	asked := withPrompt(t)

	err := runMigrateDown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("want -o json to refuse and name --yes, got %v", err)
	}
	if len(*asked) > 0 || f.hasLine("helm uninstall") {
		t.Fatal("-o json must neither prompt nor remove anything")
	}
}

// M1: a mirror installed with a real regular expression and no recorded
// list used to name no topic, so down deleted none while saying it had. The
// pattern is now resolved against the target's topics; the names it finds
// need the second consent, and --delete-data deletes exactly them.
func TestMigrateDownResolvesARegexPatternAgainstTheTarget(t *testing.T) {
	regexLab := strings.Replace(fakeLabList("m282-430"), `"topicsPattern":"kates\\.orders"`, `"topicsPattern":"kates\\..*"`, 1)
	if regexLab == fakeLabList("m282-430") {
		t.Fatal("fixture: the pattern was not replaced")
	}
	for _, deleteData := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete-data=%v", deleteData), func(t *testing.T) {
			migrateTestRepo(t)
			f := newFakeProc(t)
			scriptDown(f, true)
			f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab=m282-430 -o json", regexLab)
			scriptTargetTopics(f, "m282-430", map[string]int64{"kates.orders": 0, "kates.payments": 0},
				"__consumer_offsets", "kates-events", "mm2-m282-430-offsets", "source.checkpoints.internal")
			captureOutput(t, "table")
			downFlags(t, true, false, deleteData)

			err := runMigrateDown(context.Background())
			if !deleteData {
				if err == nil || !strings.Contains(err.Error(), "kates.orders") || !strings.Contains(err.Error(), "kates.payments") {
					t.Fatalf("want the pattern's topics named and refused, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("down --delete-data: %v", err)
			}
			for _, topic := range []string{"kates.orders", "kates.payments"} {
				if !mirroredTopicDeleted(f, topic) {
					t.Errorf("%s matched the pattern but was not deleted", topic)
				}
			}
			if mirroredTopicDeleted(f, "kates-events") || mirroredTopicDeleted(f, "__consumer_offsets") {
				t.Error("a topic the pattern does not match was deleted")
			}
		})
	}
}

func TestResolvePatternTopicsUnderTheDefaultPolicy(t *testing.T) {
	d := &labDiscovery{Policy: migrate.PolicyDefault, GroupID: "mm2-x"}
	d.legs = []mirrorLeg{{Alias: "src282", Pattern: `orders\..*`, PatternOnly: true}}
	d.Sources = []*labSource{{Alias: "src282"}}
	added, unresolved := d.resolvePatternTopics([]string{"src282.orders.eu", "orders.us", "src282.checkpoints.internal", "mm2-x-offsets", "src391.orders.eu"})
	if strings.Join(added, ",") != "src282.orders.eu" || len(unresolved) != 0 {
		t.Fatalf("added %v unresolved %v", added, unresolved)
	}
	if strings.Join(d.ReplicatedTopics(), ",") != "src282.orders.eu" {
		t.Fatalf("replicated topics %v", d.ReplicatedTopics())
	}
	bad := &labDiscovery{legs: []mirrorLeg{{Alias: "source", Pattern: `(?<=x)y`, PatternOnly: true}}}
	if _, unresolved := bad.resolvePatternTopics([]string{"y"}); len(unresolved) != 1 {
		t.Fatalf("a Java-only pattern should be reported unresolved, got %v", unresolved)
	}
}

// ── cutover --dry-run and the RUNNING pre-check ──────────────────────────

// scriptCutoverLab scripts the m282-430 lab for cutover: the cluster, the
// lab's resources and the pre-checks' reads.
func scriptCutoverLab(f *fakeProc, sourceState, brokersReady string) {
	scriptCluster(f, true)
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab=m282-430 -o json", fakeLabList("m282-430"))
	f.on(`kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, "m282-430")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json", mirrorCRJSON("mm2-m282-430-mirror-maker2", sourceState, "RUNNING"))
	f.on("kubectl -n kafka-m282-430-src get statefulset m282-430-src-legacy-kafka -o jsonpath={.status.readyReplicas}/{.spec.replicas}", brokersReady)
	f.on("kubectl -n kafka get kafka krafter -o jsonpath=", "4.3.0 True")
}

// writesNothing fails the test on any command that changes the cluster.
func writesNothing(t *testing.T, f *fakeProc) {
	t.Helper()
	for _, l := range f.lines() {
		if strings.HasPrefix(l, "helm upgrade") || strings.HasPrefix(l, "helm install") || strings.HasPrefix(l, "helm uninstall") ||
			strings.Contains(l, " apply ") || strings.Contains(l, " exec ") || strings.Contains(l, " delete ") ||
			strings.Contains(l, " annotate ") || strings.Contains(l, " create ") || strings.Contains(l, " patch ") {
			t.Errorf("a dry run ran %q", l)
		}
	}
	if _, err := os.Stat(".build"); err == nil {
		t.Error("a dry run wrote under .build")
	}
}

func TestMigrateCutoverDryRunWritesNothing(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	scriptCutoverLab(f, "RUNNING", "1/1")
	out := captureOutput(t, "json")

	flags := &migrateLabFlags{Name: "m282-430", Timeout: 600, TargetCluster: "krafter", TargetNamespace: "kafka", DryRun: true}
	if err := runMigrateCutover(context.Background(), flags); err != nil {
		t.Fatalf("cutover --dry-run: %v", err)
	}
	writesNothing(t, f)
	var p cutoverPlan
	if err := json.Unmarshal([]byte(out.String()), &p); err != nil {
		t.Fatalf("cutover --dry-run -o json: %v\n%s", err, out.String())
	}
	if !p.DryRun || !p.Ready || p.Release != "mm2-m282-430" {
		t.Fatalf("plan: %+v", p)
	}
	helm := strings.Join(p.Helm, " ")
	if !strings.HasPrefix(helm, "helm upgrade mm2-m282-430 ") || !strings.Contains(helm, "--reuse-values") || !strings.Contains(helm, "values-cutover.yaml") {
		t.Fatalf("helm command: %s", helm)
	}
	if p.Values["cutover.enabled"] != "true" {
		t.Fatalf("overlay values: %v", p.Values)
	}
	if !strings.Contains(p.Connectors[migrate.SourceConnectorSuffix], "STOPPED") || !strings.Contains(p.Connectors[migrate.CheckpointConnectorSuffix], "RUNNING") {
		t.Fatalf("connectors: %v", p.Connectors)
	}
	if len(p.Clients) == 0 || !strings.Contains(p.Proof, "kates.orders") {
		t.Fatalf("clients/proof: %v / %s", p.Clients, p.Proof)
	}
	for _, c := range p.Checks {
		if !c.OK {
			t.Errorf("pre-check %s failed: %s", c.Name, c.Detail)
		}
	}
}

// The RUNNING pre-check: a lab whose source connector is already STOPPED,
// or whose brokers are not all ready, is refused with exit 1 — by the dry
// run, and by the cutover itself before it asks or changes anything.
func TestMigrateCutoverRefusesALabThatIsNotRunning(t *testing.T) {
	for _, tc := range []struct {
		name, sourceState, brokers, want string
	}{
		{"already cut over", "STOPPED", "1/1", "already STOPPED"},
		{"brokers down", "RUNNING", "0/1", "0/1 brokers ready"},
	} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dry-run=%v", tc.name, dryRun), func(t *testing.T) {
				migrateTestRepo(t)
				f := newFakeProc(t)
				scriptCutoverLab(f, tc.sourceState, tc.brokers)
				captureOutput(t, "table")
				withPrompt(t) // any prompt fails the test

				flags := &migrateLabFlags{Name: "m282-430", Timeout: 600, TargetCluster: "krafter", TargetNamespace: "kafka", DryRun: dryRun}
				err := runMigrateCutover(context.Background(), flags)
				var silent *silentErr
				if !errors.As(err, &silent) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want an exit-1 refusal naming %q, got %v", tc.want, err)
				}
				writesNothing(t, f)
			})
		}
	}
}

// status --watch does not read the end offsets, and says so.
func TestMigrateStatusWatchSaysOffsetsAreOff(t *testing.T) {
	migrateTestRepo(t)
	f := newFakeProc(t)
	f.on("kubectl get kafkamirrormaker2,statefulset,kafka -A -l kates.io/lab -o json", fakeLabList("m282-430"))
	f.on(`kubectl get namespace kafka-m282-430-src --ignore-not-found -o jsonpath={.metadata.labels.kates\.io/lab}`, "m282-430")
	f.on("kubectl -n kafka-m282-430-src get statefulset m282-430-src-legacy-kafka -o jsonpath={.status.readyReplicas}/{.spec.replicas}", "1/1")
	f.on("kubectl -n kafka get kafka krafter -o jsonpath=", "4.3.0 True")
	f.on("kubectl -n kafka get kafkamirrormaker2 mm2-m282-430-mirror-maker2 -o json", mirrorCRJSON("mm2-m282-430-mirror-maker2", "RUNNING", "RUNNING"))
	out := captureOutput(t, "json")

	prevName, prevOffsets, prevWatch := migrateStatusName, migrateStatusOffsets, migrateStatusWatch
	t.Cleanup(func() { migrateStatusName, migrateStatusOffsets, migrateStatusWatch = prevName, prevOffsets, prevWatch })
	migrateStatusName, migrateStatusOffsets, migrateStatusWatch = "", true, true
	// A cancelled context ends the watch after its first frame.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runMigrateStatus(ctx); err != nil {
		t.Fatalf("status --watch: %v", err)
	}
	var st labStatus
	if err := json.Unmarshal([]byte(out.String()), &st); err != nil {
		t.Fatalf("status -o json: %v\n%s", err, out.String())
	}
	if !contains(st.Notes, watchOffsetsNote) {
		t.Fatalf("status --watch does not say the offsets are off: %v", st.Notes)
	}
	if len(st.Offsets) > 0 || f.hasLine("kubectl -n kafka exec") {
		t.Fatal("status --watch read the end offsets")
	}
}
