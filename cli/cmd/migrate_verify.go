package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/internal/podrun"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// The verification is the valuable part of the scripts: a known corpus
// written to the old cluster, read back off the new one and compared —
// every distinct record, not just a count — then the consumer group's
// translated position, then a cutover that provably freezes the target.
// It runs through two long-lived client pods (internal/podrun): one in the
// source's namespace with the source's own image (a 4.x client cannot be
// trusted to judge a 2.8 broker), one in the target's namespace with the
// image the MirrorMaker 2 workers run, as the kates-mm2 principal.

// sourceClient is one source's client pod: the pod itself, the tools of that
// source's own Kafka line, and how they reach it.
type sourceClient struct {
	// src is the lab source this pod speaks to.
	src *migrate.Source
	pod podrun.Pod
	// tools are the source's own release's scripts (a 4.x client cannot be
	// trusted to judge a 2.8 broker).
	tools podrun.Tools
	// cfg is the client.properties path inside the pod; empty for an
	// unauthenticated source.
	cfg string
	// bootstrap is the listener the tools use; plain the plaintext one, for
	// the pre-3.0 GetOffsetShell that takes no client configuration.
	bootstrap, plain string
}

// labClients are the lab's client pods and how to reach each end: one pod
// per source (each running that source's own image) and one on the target.
type labClients struct {
	sources         []*sourceClient
	target          podrun.Pod
	targetTools     podrun.Tools
	targetCfg       string
	targetBootstrap string
	ready           bool
}

// source is the first source's client, what a single-source command works
// with.
func (c *labClients) source() *sourceClient {
	if len(c.sources) == 0 {
		return &sourceClient{}
	}
	return c.sources[0]
}

// sourceCommitted is what verify committed on the source, per topic and
// partition, for the translation check.
type sourceCommitted map[string]map[int]int64

// clientPodName names a lab's client pod on one side.
func clientPodName(lab, side string) string {
	return "kates-migrate-" + lab + "-" + side
}

// sourceClientSide names the pod of one source: "src" in a single-source
// lab (the name it has always had), the source's alias in a fan-in.
func sourceClientSide(l *migrate.Lab, s *migrate.Source) string {
	if !l.FanIn() {
		return "src"
	}
	return s.Alias
}

// targetBootstrapFor is the in-cluster address of a Strimzi cluster's
// plain listener.
func targetBootstrapFor(cluster, namespace string) string {
	return fmt.Sprintf("%s-kafka-bootstrap.%s.svc.%s:9092", cluster, namespace, migrate.ClusterDomain)
}

// ensureClients creates the client pods (idempotent) and writes their
// client.properties. The passwords are read from their Secrets into memory
// and written through tee on stdin — never an argument, never printed.
func (r *labRun) ensureClients(ctx context.Context) error {
	if r.clients != nil && r.clients.ready {
		return nil
	}
	l := r.lab
	c := &labClients{
		targetBootstrap: targetBootstrapFor(l.TargetCluster, l.TargetNamespace),
		targetTools:     podrun.ToolsFor(l.To, ""),
	}
	for i, src := range l.Sources() {
		image := r.sourceImage(ctx, src)
		namespace := src.Namespace
		plain := src.Bootstrap
		if r.sourceExternal() {
			namespace = l.MirrorNamespace // the source is outside the cluster; its client runs beside the target
		} else {
			plain = plaintextBootstrap(src) // the lab's legacy chart always keeps its plaintext listener
		}
		if i == 0 {
			if img, _ := r.state.Values["sourceImage"].(string); img != "" {
				image = img
			}
		}
		c.sources = append(c.sources, &sourceClient{
			src: src, tools: podrun.ToolsFor(src.From, ""),
			bootstrap: src.Bootstrap, plain: plain,
			pod: podrun.Pod{
				Namespace: namespace, Name: clientPodName(l.Name, sourceClientSide(l, src)), Image: image,
				Labels: l.SourceLabels(src), RunAsUser: runAsUserFor(image),
			},
		})
	}
	c.target = podrun.Pod{
		Namespace: l.MirrorNamespace, Name: clientPodName(l.Name, "tgt"), Image: r.env.clientImage(),
		Labels: l.RoleLabels(migrate.RoleMirror),
	}
	// Registered before the pods exist, so a failure halfway still lets
	// deleteClients remove what was created.
	r.clients = c
	for _, sc := range c.sources {
		if err := podrun.Ensure(ctx, defaultRunner, sc.pod, r.timeout); err != nil {
			return err
		}
	}
	if err := podrun.Ensure(ctx, defaultRunner, c.target, r.timeout); err != nil {
		return err
	}
	// The target's SCRAM credential.
	pw, err := readSecretKey(ctx, l.TargetNamespace, migrate.TargetUser, "password")
	if err != nil {
		return err
	}
	c.targetCfg = podrun.DefaultPropertiesPath
	if err := podrun.WriteFile(ctx, defaultRunner, c.target, c.targetCfg, podrun.SASLProperties("SCRAM-SHA-512", migrate.TargetUser, pw)); err != nil {
		return err
	}
	// Each source's, when it has one.
	for _, sc := range c.sources {
		if sc.src.Auth() == migrate.SourceAuthNone {
			continue
		}
		pw, err := readSecretKey(ctx, l.MirrorNamespace, sc.src.Secret, "password")
		if err != nil {
			return err
		}
		mechanism := sc.src.Mechanism()
		if m, _ := r.state.Values["sourceMechanism"].(string); m != "" && sc == c.source() {
			mechanism = m
		}
		sc.cfg = podrun.DefaultPropertiesPath
		if err := podrun.WriteFile(ctx, defaultRunner, sc.pod, sc.cfg, podrun.SASLProperties(mechanism, sc.src.User, pw)); err != nil {
			return err
		}
	}
	c.ready = true
	return nil
}

// deleteClients removes the client pods; errors are reported, not returned.
func (r *labRun) deleteClients(ctx context.Context) {
	if r.clients == nil {
		return
	}
	pods := []podrun.Pod{r.clients.target}
	for _, sc := range r.clients.sources {
		pods = append(pods, sc.pod)
	}
	for _, p := range pods {
		if err := podrun.Delete(ctx, defaultRunner, p); err != nil {
			migrateWarn(err.Error())
		}
	}
	r.clients = nil
}

// sourceExternal says the source was given as a bootstrap address rather
// than created by the lab.
func (r *labRun) sourceExternal() bool {
	return r.state != nil && r.state.Values["sourceExternal"] == true
}

// sourceImage is the image a source's client pod runs: the broker's own
// image as the StatefulSet carries it (an overlay may have changed it), the
// lab's expectation otherwise.
func (r *labRun) sourceImage(ctx context.Context, src *migrate.Source) string {
	if src.Provider == kafkaversion.ProviderLegacy && !r.sourceExternal() {
		if img, err := defaultRunner.Run(ctx, "kubectl", "-n", src.Namespace, "get", "statefulset", src.Release+"-legacy-kafka",
			"-o", "jsonpath={.spec.template.spec.containers[0].image}"); err == nil && strings.TrimSpace(img) != "" {
			return strings.TrimSpace(img)
		}
	}
	if src.Image != "" {
		return src.Image
	}
	return kafkaversion.LegacyImageFor(src.From, r.lab.Registry)
}

// runAsUserFor picks the uid for an image whose USER is a name rather than
// a number: apache/kafka's is `appuser`, which the kubelet refuses under
// runAsNonRoot unless the uid is stated.
func runAsUserFor(image string) int64 {
	if strings.HasPrefix(image, "apache/kafka:") {
		return 1000
	}
	return 0
}

// plaintextBootstrap is a legacy source's plaintext listener (the chart
// keeps it enabled beside SASL), which the pre-3.0 offset tool needs.
func plaintextBootstrap(s *migrate.Source) string {
	if s.Provider != kafkaversion.ProviderLegacy {
		return s.Bootstrap
	}
	host := s.Bootstrap
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host + ":9092"
}

// readSecretKey reads one key of a Secret, base64-decoded, into memory.
func readSecretKey(ctx context.Context, namespace, name, key string) (string, error) {
	out, err := defaultRunner.Run(ctx, "kubectl", "-n", namespace, "get", "secret", name, "-o", "jsonpath={.data."+jsonpathKey(key)+"}")
	if err != nil {
		return "", fmt.Errorf("read Secret %s/%s: %w", namespace, name, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("Secret %s/%s key %s is not base64", namespace, name, key)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("Secret %s/%s has no %s", namespace, name, key)
	}
	return string(raw), nil
}

// corpus is the lab's corpus for one run.
func (r *labRun) corpus() []string {
	return migrate.Records(r.lab.Name, r.lab.Messages)
}

// verifyPhases are the scripts' phases 3–9 (and 10 with cutover), in order.
// A failed seed or a missing replicated topic stops the run, as the scripts
// exited there; the assertions in between record their row and continue.
func (r *labRun) verifyPhases(cutover bool) []migrate.Phase {
	phases := []migrate.Phase{
		{Name: "clients", Run: func(ctx context.Context, s *migrate.State) error { return r.ensureClients(ctx) }},
		{Name: "seed", Run: r.phaseSeed},
		{Name: "source end offsets", Run: r.phaseSourceOffsets, Optional: true},
		{Name: "source consumer group", Run: r.phaseSourceGroup, Optional: true},
		{Name: "replicated topic", Run: r.phaseReplicatedTopic},
		{Name: "read back", Run: r.phaseReadBack, Optional: true},
		{Name: "offset translation", Run: r.phaseOffsetTranslation, Optional: true},
	}
	if cutover {
		phases = append(phases, migrate.Phase{Name: "cutover rehearsal", Run: r.phaseCutoverRehearsal, Optional: true})
	}
	return phases
}

// phaseSeed produces the corpus to every topic of every source, from Go on
// the producer's stdin. Each source is its own leg: its own client pod, its
// own topics, its own row.
func (r *labRun) phaseSeed(ctx context.Context, s *migrate.State) error {
	l := r.lab
	records := r.corpus()
	stdin := strings.Join(records, "\n") + "\n"
	for _, sc := range r.clients.sources {
		if r.sourceExternal() && !sc.tools.Version.Less(kafkaversion.Version{Major: 2, Minor: 2}) {
			for _, t := range sc.src.Topics {
				_, _ = podrun.Exec(ctx, defaultRunner, sc.pod, sc.tools.CreateTopic(sc.bootstrap, sc.cfg, t, migrate.SourceTopicPartitions, 1)...)
			}
		}
		for _, t := range sc.src.Topics {
			if _, err := podrun.ExecInput(ctx, defaultRunner, sc.pod, stdin, sc.tools.ProducerArgs(sc.bootstrap, sc.cfg, t)...); err != nil {
				s.Report.Fail(l.RowFor(migrate.RowCorpusProduced, sc.src), "the producer did not report success")
				return fmt.Errorf("produce to %s on %s: %w", t, sc.src.Alias, err)
			}
		}
		s.Report.Pass(l.RowFor(migrate.RowCorpusProduced, sc.src), fmt.Sprintf("%d records → %s", len(records), strings.Join(sc.src.Topics, ", ")))
	}
	s.Produced = records
	return nil
}

// sourceEndOffsets reads the end-offset sum of a topic on one source.
func (r *labRun) sourceEndOffsets(ctx context.Context, sc *sourceClient, topic string) migrate.OffsetSum {
	bootstrap, cfg := sc.bootstrap, sc.cfg
	if !sc.tools.OffsetsTakeConfig() {
		bootstrap, cfg = sc.plain, ""
	}
	out, err := podrun.Exec(ctx, defaultRunner, sc.pod, sc.tools.EndOffsets(bootstrap, cfg, topic)...)
	if err != nil {
		return migrate.OffsetSum{}
	}
	_, sum, err := podrun.ParseTopicOffsets(out, topic)
	if err != nil {
		return migrate.OffsetSum{}
	}
	return migrate.Measured(sum)
}

// targetEndOffsets reads the end-offset sum of a topic on the target.
func (r *labRun) targetEndOffsets(ctx context.Context, topic string) migrate.OffsetSum {
	c := r.clients
	out, err := podrun.Exec(ctx, defaultRunner, c.target, c.targetTools.EndOffsets(c.targetBootstrap, c.targetCfg, topic)...)
	if err != nil {
		return migrate.OffsetSum{}
	}
	_, sum, err := podrun.ParseTopicOffsets(out, topic)
	if err != nil {
		return migrate.OffsetSum{}
	}
	return migrate.Measured(sum)
}

// sumOffsets adds readings; one unmeasured reading makes the sum unmeasured.
func sumOffsets(readings []migrate.OffsetSum) migrate.OffsetSum {
	var total int64
	for _, o := range readings {
		if !o.Measured {
			return migrate.OffsetSum{}
		}
		total += o.Sum
	}
	return migrate.Measured(total)
}

// phaseSourceOffsets is the scripts' second half of phase 3: the source's
// end offsets after seeding must cover the corpus.
func (r *labRun) phaseSourceOffsets(ctx context.Context, s *migrate.State) error {
	l := r.lab
	var all []migrate.OffsetSum
	for _, sc := range r.clients.sources {
		var readings []migrate.OffsetSum
		for _, t := range sc.src.Topics {
			readings = append(readings, r.sourceEndOffsets(ctx, sc, t))
		}
		sum := sumOffsets(readings)
		all = append(all, sum)
		want := int64(l.Messages * len(sc.src.Topics))
		row := l.RowFor(migrate.RowSourceEndOffsets, sc.src)
		if sum.Measured && sum.Sum >= want {
			s.Report.Pass(row, fmt.Sprintf("%d across all partitions", sum.Sum))
			continue
		}
		s.Report.Fail(row, fmt.Sprintf("expected >= %d, measured '%s'", want, sum))
	}
	s.SourceEndOffsets = sumOffsets(all)
	return nil
}

// phaseSourceGroup is the scripts' phase 4: a consumer group reads half the
// corpus on the source and commits, so there is a position to translate.
func (r *labRun) phaseSourceGroup(ctx context.Context, s *migrate.State) error {
	l := r.lab
	half := l.Messages / 2
	if half < 1 {
		half = 1
	}
	committed := sourceCommitted{}
	total := 0
	for _, sc := range r.clients.sources {
		for _, t := range sc.src.Topics {
			// The consumer's own exit code is not the verdict; the committed
			// offsets are.
			_, _ = podrun.Exec(ctx, defaultRunner, sc.pod, sc.tools.ConsumerArgs(sc.bootstrap, sc.cfg, t, l.ConsumerGroup, half, true, 60*time.Second)...)
		}
		out, err := podrun.Exec(ctx, defaultRunner, sc.pod, sc.tools.DescribeGroup(sc.bootstrap, sc.cfg, l.ConsumerGroup)...)
		partitions := 0
		if err == nil {
			for _, row := range podrun.ParseGroupDescribe(out) {
				if row.CurrentOffset < 0 {
					continue
				}
				if committed[row.Topic] == nil {
					committed[row.Topic] = map[int]int64{}
				}
				committed[row.Topic][row.Partition] = row.CurrentOffset
				partitions++
			}
		}
		total += partitions
		row := l.RowFor(migrate.RowSourceConsumerGroup, sc.src)
		if partitions > 0 {
			s.Report.Pass(row, fmt.Sprintf("%s committed on %d partition(s)", l.ConsumerGroup, partitions))
			continue
		}
		s.Report.Fail(row, "no committed offset — offset translation cannot be tested")
	}
	s.Values["sourceCommitted"] = committed
	s.GroupPartitions = total
	return nil
}

// phaseReplicatedTopic is the scripts' phase 7: every replicated topic
// appears on the target within the budget.
func (r *labRun) phaseReplicatedTopic(ctx context.Context, s *migrate.State) error {
	l, c := r.lab, r.clients
	deadline := migrateNow().Add(r.timeout)
	want := l.ReplicatedTopics()
	var listed []string
	for {
		out, err := podrun.Exec(ctx, defaultRunner, c.target, c.targetTools.ListTopics(c.targetBootstrap, c.targetCfg)...)
		if err == nil {
			listed = strings.Fields(out)
			if allListed(want, listed) {
				s.Report.Pass(migrate.RowReplicatedTopic, strings.Join(want, ", ")+" on the target")
				return nil
			}
		}
		if !migrateNow().Before(deadline) {
			break
		}
		if err := migrateSleep(ctx, 15*time.Second); err != nil {
			return err
		}
	}
	missing := want[0]
	for _, t := range want {
		if !contains(listed, t) {
			missing = t
			break
		}
	}
	s.Report.Fail(migrate.RowReplicatedTopic, missing+" never appeared")
	if len(listed) > 0 {
		migrateHint("topics on the target: " + strings.Join(listed, " "))
	}
	if l.Policy == migrate.PolicyIdentity {
		for _, src := range l.Sources() {
			for _, t := range src.Topics {
				if contains(listed, src.Alias+"."+t) {
					migrateWarn(fmt.Sprintf("%s.%s is on the target: the replication policy in force is not %s", src.Alias, t, l.Policy))
				}
			}
		}
	}
	return errors.New("the replicated topic never appeared on the target")
}

func allListed(want, listed []string) bool {
	for _, t := range want {
		if !contains(listed, t) {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// phaseReadBack is the scripts' phase 8: the corpus read back off the
// target, compared as distinct values (MirrorMaker 2 is at-least-once) and
// as a full set (min-and-max cannot see a hole in the middle).
func (r *labRun) phaseReadBack(ctx context.Context, s *migrate.State) error {
	l, c := r.lab, r.clients
	expected := r.corpus()
	var consumed []string
	group := 0
	for _, src := range l.Sources() {
		distinct, lines := 0, 0
		var missing []string
		for _, t := range src.Topics {
			replicated := l.ReplicatedTopicOf(src, t)
			out, err := podrun.Exec(ctx, defaultRunner, c.target,
				c.targetTools.ConsumerArgs(c.targetBootstrap, c.targetCfg, replicated, fmt.Sprintf("%s-%d", l.VerifyGroup, group), 2*l.Messages, true, 120*time.Second)...)
			group++
			if err != nil {
				migrateWarn(fmt.Sprintf("consume %s: %v", replicated, err))
			}
			got := migrate.ParseConsumed(out)
			consumed = append(consumed, got...)
			lines += len(got)
			distinct += migrate.Distinct(got)
			m, _ := migrate.Diff(expected, got)
			for _, id := range migrate.IDs(m) {
				missing = append(missing, fmt.Sprintf("%s#%d", replicated, id))
			}
		}
		want := l.Messages * len(src.Topics)
		if distinct >= want {
			s.Report.Pass(l.RowFor(migrate.RowRecordCount, src), fmt.Sprintf("%d distinct of %d received (%d lines — duplicates are at-least-once, not a fault)", distinct, want, lines))
		} else {
			s.Report.Fail(l.RowFor(migrate.RowRecordCount, src), fmt.Sprintf("only %d distinct of %d arrived (%d lines)", distinct, want, lines))
		}
		if len(missing) == 0 {
			s.Report.Pass(l.RowFor(migrate.RowRecordContent, src), fmt.Sprintf("every record 1..%d present on the target", l.Messages))
		} else {
			shown := missing
			if len(shown) > 5 {
				shown = shown[:5]
			}
			s.Report.Fail(l.RowFor(migrate.RowRecordContent, src), fmt.Sprintf("missing from the target: %s… (%d in all)", strings.Join(shown, " "), len(missing)))
		}
	}
	s.Consumed = consumed
	return nil
}

// phaseOffsetTranslation is the scripts' phase 9: the source consumer
// group has a committed position on the replicated topic. The translated
// position is compared with the source's for the detail — MirrorMaker
// translates through offset syncs, so it can trail the source's by design,
// which is stated rather than failed.
func (r *labRun) phaseOffsetTranslation(ctx context.Context, s *migrate.State) error {
	l, c := r.lab, r.clients
	committed, _ := s.Values["sourceCommitted"].(sourceCommitted)
	deadline := migrateNow().Add(r.timeout)
	pending := l.Sources()
	for {
		out, err := podrun.Exec(ctx, defaultRunner, c.target, c.targetTools.DescribeGroup(c.targetBootstrap, c.targetCfg, l.ConsumerGroup)...)
		if err == nil {
			rows := podrun.ParseGroupDescribe(out)
			var still []*migrate.Source
			for _, src := range pending {
				var translated []string
				exact, behind := 0, 0
				for _, t := range src.Topics {
					for _, row := range podrun.GroupOffsetsFor(rows, l.ReplicatedTopicOf(src, t)) {
						if row.CurrentOffset < 0 {
							continue
						}
						detail := fmt.Sprintf("%s/%d=%d", row.Topic, row.Partition, row.CurrentOffset)
						if at, ok := committed[t][row.Partition]; ok {
							detail += fmt.Sprintf(" (source %d)", at)
							if at == row.CurrentOffset {
								exact++
							} else {
								behind++
							}
						}
						translated = append(translated, detail)
					}
				}
				if len(translated) == 0 {
					still = append(still, src)
					continue
				}
				detail := fmt.Sprintf("%s has a committed position on the target: %s", l.ConsumerGroup, strings.Join(translated, " "))
				if behind > 0 {
					detail += fmt.Sprintf(" — %d partition(s) trail the source's position (offset syncs are emitted every offset.lag.max records, so the translated position is conservative)", behind)
				} else if exact > 0 {
					detail += " — equal to the source's committed position"
				}
				s.Report.Pass(l.RowFor(migrate.RowOffsetTranslation, src), detail)
			}
			pending = still
			if len(pending) == 0 {
				return nil
			}
		}
		if !migrateNow().Before(deadline) {
			break
		}
		if err := migrateSleep(ctx, 15*time.Second); err != nil {
			return err
		}
	}
	for _, src := range pending {
		s.Report.Fail(l.RowFor(migrate.RowOffsetTranslation, src), l.ConsumerGroup+" never appeared on the target")
	}
	migrateWarn("check sync.group.offsets.enabled, groupsPattern, and the target user's group ACLs")
	return nil
}

// applyCutover is the cutover: the overlay on top of the release's values,
// then the source connector STOPPED and the checkpoint connector RUNNING.
func (r *labRun) applyCutover(ctx context.Context, s *migrate.State) error {
	l := r.lab
	if err := ensureMirrorChartDeps(ctx); err != nil {
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", helmCutoverArgs(l.MirrorRelease, l.MirrorNamespace, r.timeout)...); err != nil {
		s.Report.Fail(migrate.RowCutoverApplied, "helm upgrade with values-cutover.yaml failed")
		return fmt.Errorf("apply the cutover: %w", err)
	}
	if _, err := waitConnectorStates(ctx, l.MirrorNamespace, l.MirrorCR, migrate.StateStopped, migrate.StateRunning, r.timeout); err != nil {
		s.Report.Fail(migrate.RowCutoverApplied, err.Error())
		return err
	}
	s.Report.Pass(migrate.RowCutoverApplied, "source connector stopped, checkpoint connector still running")
	return nil
}

// proveFrozen is the scripts' phase 10 after the cutover: a baseline once
// the cutover has settled, more records to the source, and the target's end
// offsets unchanged. Three outcomes, and a sanity floor on the baseline.
func (r *labRun) proveFrozen(ctx context.Context, s *migrate.State) error {
	l := r.lab
	migrateHint("letting the cutover settle before taking a baseline...")
	if err := migrateSleep(ctx, migrateCutoverSettle); err != nil {
		return err
	}
	var before []migrate.OffsetSum
	for _, t := range l.ReplicatedTopics() {
		before = append(before, r.targetEndOffsets(ctx, t))
	}
	s.TargetOffsetsBefore = sumOffsets(before)

	extra := make([]string, 0, migrateCutoverExtra)
	for i := 1; i <= migrateCutoverExtra; i++ {
		extra = append(extra, "after-cutover-"+strconv.Itoa(i))
	}
	stdin := strings.Join(extra, "\n") + "\n"
	for _, sc := range r.clients.sources {
		for _, t := range sc.src.Topics {
			_, _ = podrun.ExecInput(ctx, defaultRunner, sc.pod, stdin, sc.tools.ProducerArgs(sc.bootstrap, sc.cfg, t)...)
		}
	}
	if err := migrateSleep(ctx, migrateCutoverSettle); err != nil {
		return err
	}
	var after []migrate.OffsetSum
	for _, t := range l.ReplicatedTopics() {
		after = append(after, r.targetEndOffsets(ctx, t))
	}
	s.TargetOffsetsAfter = sumOffsets(after)

	floor := int64(l.Messages * len(l.Topics))
	b, a := s.TargetOffsetsBefore, s.TargetOffsetsAfter
	switch {
	case !b.Measured || !a.Measured:
		s.Report.Fail(migrate.RowCutoverFroze, fmt.Sprintf("could not read end offsets (before='%s' after='%s') — the measurement failed, not necessarily the cutover", b, a))
	case b.Sum < floor:
		s.Report.Fail(migrate.RowCutoverFroze, fmt.Sprintf("baseline %d is below the %d records already read back — the offset tool is not measuring what it should", b.Sum, floor))
	case b.Sum == a.Sum:
		s.Report.Pass(migrate.RowCutoverFroze, fmt.Sprintf("end offsets unchanged at %d after %d more source records", a.Sum, migrateCutoverExtra*len(l.Topics)))
	default:
		s.Report.Fail(migrate.RowCutoverFroze, fmt.Sprintf("offsets moved %d → %d — the source connector did not stop", b.Sum, a.Sum))
	}
	return nil
}

// phaseCutoverRehearsal is the cutover and its proof, as one phase of run.
func (r *labRun) phaseCutoverRehearsal(ctx context.Context, s *migrate.State) error {
	if err := r.applyCutover(ctx, s); err != nil {
		return err
	}
	return r.proveFrozen(ctx, s)
}

// applyRollback reverses a cutover: the rollback overlay, then both
// connectors RUNNING.
func (r *labRun) applyRollback(ctx context.Context, s *migrate.State) error {
	l := r.lab
	path := rollbackValuesPath(l.Name)
	if err := writeLabFile(path, rollbackValues); err != nil {
		return err
	}
	if err := ensureMirrorChartDeps(ctx); err != nil {
		return err
	}
	if _, err := defaultRunner.Run(ctx, "helm", helmRollbackArgs(l.MirrorRelease, l.MirrorNamespace, path, r.timeout)...); err != nil {
		s.Report.Fail(rowRollbackApplied, "helm upgrade without the cutover overlay failed")
		return fmt.Errorf("apply the rollback: %w", err)
	}
	if _, err := waitConnectorStates(ctx, l.MirrorNamespace, l.MirrorCR, migrate.StateRunning, migrate.StateRunning, r.timeout); err != nil {
		s.Report.Fail(rowRollbackApplied, err.Error())
		return err
	}
	s.Report.Pass(rowRollbackApplied, "source connector running again from its committed offsets")
	return nil
}

// rowRollbackApplied is the one row rollback adds to the scripts' set.
const rowRollbackApplied = "rollback applied"

// ── the lab commands: verify, cutover, rollback, run ─────────────────────

// migrateLabFlags are the flags of the commands that act on an existing
// lab.
type migrateLabFlags struct {
	Name            string
	Yes             bool
	Keep            bool
	Timeout         int
	Messages        int
	TargetCluster   string
	TargetNamespace string
	// verify against a real source
	FromBootstrap   string
	SourceVersion   string
	SourceSecret    string
	SourceUser      string
	SourceMechanism string
	SourceImage     string
	Policy          string
	Topics          string
}

func (f *migrateLabFlags) timeout() time.Duration {
	if f.Timeout <= 0 {
		return migrateDefaultTimeout * time.Second
	}
	return time.Duration(f.Timeout) * time.Second
}

func addMigrateLabFlags(cmd *cobra.Command, f *migrateLabFlags, yes bool) {
	cmd.Flags().StringVar(&f.Name, "name", "", "Lab name (default: the single lab on the cluster)")
	if yes {
		cmd.Flags().BoolVarP(&f.Yes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")
	}
	cmd.Flags().IntVar(&f.Timeout, "timeout", migrateDefaultTimeout, "Per-phase wait budget in seconds")
	cmd.Flags().StringVar(&f.TargetCluster, "target-cluster", migrate.DefaultTargetCluster, "Name of the target Kafka")
	cmd.Flags().StringVar(&f.TargetNamespace, "target-namespace", migrate.DefaultTargetNamespace, "Namespace of the target Kafka")
}

var (
	migrateVerifyFlags   migrateLabFlags
	migrateCutoverFlags  migrateLabFlags
	migrateRollbackFlags migrateLabFlags
	migrateRunFlags      migratePairFlags

	migrateVerifyCmd = &cobra.Command{
		Use:   "verify [--name <lab>] | --from-bootstrap host:9092 --source-version <v>",
		Short: "Seed the source, read the corpus back off the target, check the offset translation",
		Long: `The scripts' phases 3–9 against a lab (found by label) or against a real
source (--from-bootstrap, with --source-version and, for an authenticated
listener, --source-secret/--source-user/--source-mechanism): a known corpus
is produced to the source through a client pod running the source's own
image, a consumer group commits on it, the replicated topic is awaited on
the target, the corpus is read back as kates-mm2 and compared — every
distinct record — and the group's translated position is checked. Every
assertion is a row; the exit code is 1 on any failed row. The client pods
are removed at the end unless --keep.`,
		Example: `  kates migrate verify --name m282-430
  kates migrate verify --from-bootstrap old-kafka.example:9092 --source-version 2.8.2 --source-secret old-kafka-reader --source-user reader --source-mechanism plain
  kates migrate verify -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateVerify(cmd.Context(), &migrateVerifyFlags)
		},
	}

	migrateCutoverCmd = &cobra.Command{
		Use:   "cutover [--name <lab>] [--yes]",
		Short: "Stop the source connector, keep the checkpoints running, prove the target is frozen",
		Long: `Applies charts/mirror-maker2/values-cutover.yaml on top of the mirror's
values: the source connector goes STOPPED (data replication ends), the
checkpoint connector stays RUNNING (offset translation continues for the
consumers that have not moved yet). Then it produces more records to the
source and proves the target's end offsets do not move.`,
		Example: `  kates migrate cutover --name m282-430 --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateCutover(cmd.Context(), &migrateCutoverFlags)
		},
	}

	migrateRollbackCmd = &cobra.Command{
		Use:   "rollback [--name <lab>] [--yes]",
		Short: "Reverse a cutover: the source connector running again",
		Long: `Removes the cutover overlay: stopped → running resumes from the committed
offsets, so nothing is re-read from the start — but anything produced to the
TARGET in the meantime is not replicated backwards. A rollback after
producers have moved is a data merge, not a switch.`,
		Example: `  kates migrate rollback --name m282-430 --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateRollback(cmd.Context(), &migrateRollbackFlags)
		},
	}

	migrateRunCmd = &cobra.Command{
		Use:   "run --from <version> [--from <version>…] [--to <version>]",
		Short: "up → verify → cutover → down: one report, one exit code",
		Long: `The CI leg: creates the lab, runs the verification, rehearses the cutover
and removes the lab, printing one report — the scripts' eighteen rows under
a header that states the pair, the providers, the operator, the policy and
where the offset-syncs topic lives. With several --from values every source
is its own leg of the report, named by its alias. The exit code is 1 on any
failed row. --keep leaves the lab in place and prints how to inspect and
remove it; --skip-build assumes the source image is present;
--source-version is accepted as an alias of --from.`,
		Example: `  kates migrate run --from 2.8.2 --skip-build --timeout 600 --yes
  kates migrate run --from 3.9.1 --policy default --name m391-430-default --yes
  kates migrate run --from 2.8.2 --from 3.9.1 --skip-build --yes
  kates migrate run --from 3.9.1 --keep -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateRun(cmd.Context(), &migrateRunFlags)
		},
	}
)

func init() {
	addMigrateLabFlags(migrateVerifyCmd, &migrateVerifyFlags, false)
	migrateVerifyCmd.Flags().BoolVar(&migrateVerifyFlags.Keep, "keep", false, "Leave the client pods in place")
	migrateVerifyCmd.Flags().IntVar(&migrateVerifyFlags.Messages, "messages", migrate.DefaultMessages, "Records to produce per topic")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.FromBootstrap, "from-bootstrap", "", "Verify against a real source at this bootstrap address instead of a lab")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.SourceVersion, "source-version", "", "Kafka version of the --from-bootstrap source")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.SourceSecret, "source-secret", "", "Secret in the target namespace holding the source password under \"password\"")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.SourceUser, "source-user", "", "Username for the --from-bootstrap source")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.SourceMechanism, "source-mechanism", migrate.MechanismPlain, "SASL mechanism of the --from-bootstrap source: plain, scram-sha-256 or scram-sha-512")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.SourceImage, "source-image", "", "Client image for the --from-bootstrap source (default: the legacy image for its version)")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.Policy, "policy", migrate.PolicyIdentity, "Replication policy of the mirror in front of a --from-bootstrap source")
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyFlags.Topics, "topics", migrate.DefaultTopic, "Topics to seed on a --from-bootstrap source, comma-separated")

	addMigrateLabFlags(migrateCutoverCmd, &migrateCutoverFlags, true)
	addMigrateLabFlags(migrateRollbackCmd, &migrateRollbackFlags, true)

	addMigratePairFlags(migrateRunCmd, &migrateRunFlags)
	migrateRunCmd.Flags().BoolVar(&migrateRunFlags.Keep, "keep", false, "Leave the lab running for inspection")
	migrateRunCmd.Flags().BoolVar(&migrateRunFlags.SkipBuild, "skip-build", false, "Do not build/load the source image (assume it is present)")
	migrateRunCmd.Flags().StringVar(&migrateRunFlags.SourceVersion, "source-version", "", "Alias of --from (the scripts' spelling)")
	_ = migrateRunCmd.Flags().MarkDeprecated("source-version", "use --from")

	migrateCmd.AddCommand(migrateVerifyCmd, migrateCutoverCmd, migrateRollbackCmd, migrateRunCmd)
}

// openLabRun finds a lab by label and opens a run on it.
func openLabRun(ctx context.Context, f *migrateLabFlags) (*labRun, error) {
	d, err := discoverLab(ctx, f.Name)
	if err != nil {
		return nil, err
	}
	targetCluster, targetNamespace := f.TargetCluster, f.TargetNamespace
	if d.TargetCluster != "" && d.TargetNamespace != "" {
		targetCluster, targetNamespace = d.TargetCluster, d.TargetNamespace
	}
	env, err := discoverMigrateEnv(ctx, targetCluster, targetNamespace, false)
	if err != nil {
		return nil, err
	}
	l, err := labFromDiscovery(d, env)
	if err != nil {
		return nil, err
	}
	if f.Messages > 0 {
		l.Messages = f.Messages
	}
	res := &migrateResolution{Env: env, Lab: l, Opts: l.Options, SourceOperator: "none — plain StatefulSets from charts/legacy-kafka"}
	r := &labRun{lab: l, env: env, res: res, timeout: f.timeout(), keep: f.Keep}
	r.state = migrate.NewState(l, labHeader(res))
	r.state.Values["discovery"] = d
	return r, nil
}

// openExternalRun opens a run against a real source given by its bootstrap
// address: the lab is only a name for the corpus and the client pods.
func openExternalRun(ctx context.Context, f *migrateLabFlags) (*labRun, error) {
	if f.SourceVersion == "" {
		return nil, errors.New("--from-bootstrap needs --source-version (the source's Kafka version)")
	}
	from, err := kafkaversion.Parse(f.SourceVersion)
	if err != nil {
		return nil, fmt.Errorf("--source-version: %w", err)
	}
	if !strings.Contains(f.FromBootstrap, ":") {
		return nil, fmt.Errorf("--from-bootstrap %q: want host:port", f.FromBootstrap)
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.TargetNamespace, false)
	if err != nil {
		return nil, err
	}
	opts := migrate.Options{
		Name: f.Name, From: from, To: env.Target.Version, SourceProvider: kafkaversion.ProviderLegacy,
		TargetIsPrimary: true, TargetCluster: env.Target.Cluster, TargetNamespace: env.Target.Namespace,
		Policy: f.Policy, Topics: splitTopics(f.Topics), Messages: f.Messages,
		SASL: f.SourceSecret != "", SourcePassword: "external", StrimziVersion: env.StrimziVersion, Scope: env.Scope,
	}
	l, err := migrate.New(opts)
	if err != nil {
		return nil, err
	}
	l.SourceBootstrap = f.FromBootstrap
	l.SourcePassword = ""
	if f.SourceSecret != "" {
		if f.SourceUser == "" {
			return nil, errors.New("--source-secret needs --source-user")
		}
		l.SourceSecret, l.SourceUser = f.SourceSecret, f.SourceUser
		l.SASL = true
	}
	res := &migrateResolution{Env: env, Lab: l, Opts: l.Options, SourceOperator: "external — " + f.FromBootstrap}
	r := &labRun{lab: l, env: env, res: res, timeout: f.timeout(), keep: f.Keep}
	r.state = migrate.NewState(l, labHeader(res))
	r.state.Report.Header["source"] = "external " + f.FromBootstrap + " (Kafka " + from.String() + ")"
	r.state.Values["sourceExternal"] = true
	if f.SourceImage != "" {
		r.state.Values["sourceImage"] = f.SourceImage
	}
	if f.SourceMechanism != "" {
		r.state.Values["sourceMechanism"] = f.SourceMechanism
	}
	return r, nil
}

// runMigrateVerify is `kates migrate verify`.
func runMigrateVerify(ctx context.Context, f *migrateLabFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var r *labRun
	var err error
	if f.FromBootstrap != "" {
		r, err = openExternalRun(ctx, f)
	} else {
		r, err = openLabRun(ctx, f)
	}
	if err != nil {
		return err
	}
	runErr := migrate.RunPhases(ctx, traced(r.verifyPhases(false)), r.state)
	if !f.Keep {
		r.deleteClients(context.Background())
	} else if r.clients != nil {
		pods := []string{r.clients.target.Namespace + "/" + r.clients.target.Name}
		for _, sc := range r.clients.sources {
			pods = append(pods, sc.pod.Namespace+"/"+sc.pod.Name)
		}
		migrateHint("--keep: client pods " + strings.Join(pods, ", ") + " left in place")
	}
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), fmt.Sprintf("lab %s", r.lab.Name)); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess(fmt.Sprintf("Kafka %s → %s verified (%s policy)", r.lab.From, r.lab.To, r.lab.Policy))
	return nil
}

// runMigrateCutover is `kates migrate cutover`.
func runMigrateCutover(ctx context.Context, f *migrateLabFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := openLabRun(ctx, f)
	if err != nil {
		return err
	}
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Cut over lab %s: stop the source connector of %s (the checkpoints keep running)?", r.lab.Name, r.lab.MirrorRelease)); err != nil {
		return err
	}
	phases := []migrate.Phase{
		{Name: "clients", Run: func(ctx context.Context, s *migrate.State) error { return r.ensureClients(ctx) }},
		{Name: "cutover", Run: r.applyCutover},
		{Name: "cutover froze the target", Run: r.proveFrozen},
	}
	runErr := migrate.RunPhases(ctx, traced(phases), r.state)
	r.deleteClients(context.Background())
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), fmt.Sprintf("the cutover of lab %s", r.lab.Name)); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess("cutover applied — the target is frozen; kates migrate rollback reverses it")
	return nil
}

// runMigrateRollback is `kates migrate rollback`.
func runMigrateRollback(ctx context.Context, f *migrateLabFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := openLabRun(ctx, f)
	if err != nil {
		return err
	}
	migrateWarn("anything produced to the target since the cutover is not replicated backwards — a rollback after producers have moved is a data merge, not a switch")
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Roll back the cutover of lab %s: run the source connector of %s again?", r.lab.Name, r.lab.MirrorRelease)); err != nil {
		return err
	}
	runErr := migrate.RunPhases(ctx, traced([]migrate.Phase{{Name: "rollback", Run: r.applyRollback}}), r.state)
	if runErr != nil {
		output.Error(runErr.Error())
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), fmt.Sprintf("the rollback of lab %s", r.lab.Name)); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess("rollback applied — the mirror replicates again from its committed offsets")
	return nil
}

// runMigrateRun is `kates migrate run`: up → verify → cutover → down.
func runMigrateRun(ctx context.Context, f *migratePairFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	env, err := discoverMigrateEnv(ctx, f.TargetCluster, f.TargetNamespace, false)
	if err != nil {
		return err
	}
	if f.Interactive {
		if err := migrateInteractivePick(env, f); err != nil {
			return err
		}
	}
	res, err := resolveMigratePair(env, f)
	if err != nil {
		return err
	}
	if err := refuseUnimplemented(res); err != nil {
		return err
	}
	printNotes(res)
	if err := migrateConfirm(f.Yes, fmt.Sprintf("Run the migration lab %s (%s), then remove it?", res.Lab.Name, res.Lab.Describe())); err != nil {
		return err
	}
	r := newLabRun(res, f)
	if outputMode != "json" {
		output.SubHeader("MirrorMaker 2 cross-version migration — " + res.Lab.Describe())
	}

	// Ctrl-C stops the phases; the teardown still runs, on its own context,
	// as the scripts' EXIT trap did.
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	runErr := migrate.RunPhases(runCtx, traced(r.upPhases()), r.state)
	if runErr == nil {
		runErr = migrate.RunPhases(runCtx, traced(r.verifyPhases(true)), r.state)
	}
	if runErr != nil {
		output.Error(runErr.Error())
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*r.timeout)
	defer cancel()
	if f.Keep {
		if r.clients != nil {
			r.deleteClients(cleanupCtx)
		}
		printKeepBlock(r.lab)
	} else {
		r.deleteClients(cleanupCtx)
		migrateHint("cleaning up...")
		if err := labDown(cleanupCtx, discoveryFromLab(r.lab, r.createdNamespace), r.timeout); err != nil {
			migrateWarn(err.Error())
		}
	}
	printMigrateReport(r.report())
	if err := reportFailure(r.report(), fmt.Sprintf("the Kafka %s → %s path", r.lab.From, r.lab.To)); err != nil {
		return err
	}
	if runErr != nil {
		return cmdErr(runErr.Error())
	}
	migrateSuccess(fmt.Sprintf("Kafka %s → %s migration verified end to end (%s policy)", r.lab.From, r.lab.To, r.lab.Policy))
	return nil
}

// printKeepBlock is the scripts' --keep message: leave it, here is how to
// inspect and remove it.
func printKeepBlock(l *migrate.Lab) {
	migrateWarn("--keep: leaving the lab in place.")
	migrateHint(fmt.Sprintf("  status:  kates migrate status --name %s", l.Name))
	migrateHint(fmt.Sprintf("  source:  kubectl -n %s get all", l.SourceNamespace))
	migrateHint(fmt.Sprintf("  mirror:  kubectl -n %s get kafkamirrormaker2 %s", l.MirrorNamespace, l.MirrorCR))
	migrateHint(fmt.Sprintf("  remove:  kates migrate down --name %s --yes", l.Name))
}
