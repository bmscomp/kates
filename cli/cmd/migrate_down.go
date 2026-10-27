package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/internal/podrun"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// The mirrored topics of a lab are user data once anything has written to
// them, so down deletes them only after it has looked: it lists the target's
// topics (which also resolves a topicsPattern the lab recorded no names for)
// and reads the end offsets of each, and a topic that holds records, that
// could not be read, or that only a regular expression names needs a second,
// explicit consent.

// topicHolding is one mirrored topic down would delete that needs consent,
// and why.
type topicHolding struct {
	Topic   string
	Records migrate.OffsetSum
	Reason  string
}

// patternLegs are the mirror's legs whose topicsPattern names topics the
// lab recorded no list for.
func (d *labDiscovery) patternLegs() []mirrorLeg {
	var out []mirrorLeg
	for _, leg := range d.legs {
		if leg.PatternOnly && leg.Pattern != "" {
			out = append(out, leg)
		}
	}
	return out
}

// mirrorOwnTopic says a target topic is Kafka's or MirrorMaker's own, never
// a mirrored one, whatever a pattern matches: a pattern such as .* must not
// resolve to the brokers' offsets or another mirror's checkpoints.
func (d *labDiscovery) mirrorOwnTopic(t string) bool {
	switch {
	case strings.HasPrefix(t, "__"), strings.HasSuffix(t, ".internal"),
		t == "heartbeats", strings.HasSuffix(t, ".heartbeats"),
		strings.HasPrefix(t, "mm2-offset-syncs."):
		return true
	case d.GroupID != "" && strings.HasPrefix(t, d.GroupID+"-"):
		return true
	}
	return false
}

// resolvePatternTopics resolves every pattern-only leg against the target's
// topic list: a listed topic whose source-side name (the alias prefix
// removed under the default policy) the leg's pattern matches becomes one of
// the leg's topics, and so one down deletes. It returns the target-side
// names it added, and the patterns Go's regexp could not compile, whose
// topics stay unnamed and so are kept.
func (d *labDiscovery) resolvePatternTopics(listed []string) (added, unresolved []string) {
	for i := range d.legs {
		leg := &d.legs[i]
		if !leg.PatternOnly || leg.Pattern == "" {
			continue
		}
		re, err := regexp.Compile("^(?:" + leg.Pattern + ")$")
		if err != nil {
			unresolved = append(unresolved, leg.Pattern)
			continue
		}
		var names []string
		for _, t := range listed {
			if d.mirrorOwnTopic(t) {
				continue
			}
			name := t
			if d.Policy == migrate.PolicyDefault && leg.Alias != "" {
				if !strings.HasPrefix(t, leg.Alias+".") {
					continue
				}
				name = strings.TrimPrefix(t, leg.Alias+".")
			}
			if re.MatchString(name) && !contains(leg.Topics, name) {
				names = append(names, name)
				added = append(added, t)
			}
		}
		leg.PatternOnly = false
		if len(names) == 0 {
			continue
		}
		// Fresh slices: a source's Topics may share the leg's backing array.
		leg.Topics = append(append([]string(nil), leg.Topics...), names...)
		d.Topics = append(append([]string(nil), d.Topics...), names...)
		for _, s := range d.Sources {
			if s.Alias == leg.Alias {
				s.Topics = append(append([]string(nil), s.Topics...), names...)
			}
		}
	}
	return added, unresolved
}

// inspectLabTopics lists the target's topics and reads the end offsets of
// every mirrored topic of the lab there, from a client pod carrying the
// lab's label that it removes afterwards. It resolves the pattern-only legs
// first, so the topics a regular expression mirrored are named too. It
// returns the topics that need consent.
func inspectLabTopics(ctx context.Context, d *labDiscovery, timeout time.Duration) ([]topicHolding, error) {
	c, err := openTargetClient(ctx, migrateTargetOptions{
		Namespace: d.TargetNamespace, Cluster: d.TargetCluster, User: migrate.TargetUser, Timeout: int(timeout / time.Second),
		Pod:    clientPodName(d.Name, "tgt"),
		Labels: map[string]string{migrate.LabelLab: d.Name, migrate.LabelRole: migrate.RoleMirror},
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := podrun.Delete(context.Background(), defaultRunner, c.pod); err != nil {
			migrateWarn(err.Error())
		}
	}()
	out, err := podrun.Exec(ctx, defaultRunner, c.pod, c.tools.ListTopics(c.bootstrap, c.cfg)...)
	if err != nil {
		return nil, fmt.Errorf("list the topics on %s: %w", d.TargetCluster, err)
	}
	listed := strings.Fields(out)
	added, unresolved := d.resolvePatternTopics(listed)
	for _, p := range unresolved {
		migrateWarn(fmt.Sprintf("topicsPattern %q is not a pattern Go can read, so the topics it mirrored cannot be named: they are kept", p))
	}
	fromPattern := map[string]bool{}
	for _, t := range added {
		fromPattern[t] = true
	}
	var held []topicHolding
	for _, t := range d.ReplicatedTopics() {
		if !contains(listed, t) {
			continue // not on the target: nothing to delete
		}
		h := topicHolding{Topic: t}
		raw, err := podrun.Exec(ctx, defaultRunner, c.pod, c.tools.EndOffsets(c.bootstrap, c.cfg, t)...)
		if err == nil {
			if _, sum, perr := podrun.ParseTopicOffsets(raw, t); perr == nil {
				h.Records = migrate.Measured(sum)
			}
		}
		var why []string
		switch {
		case !h.Records.Measured:
			why = append(why, "its end offsets could not be read")
		case h.Records.Sum > 0:
			why = append(why, fmt.Sprintf("holds %d record(s)", h.Records.Sum))
		}
		if fromPattern[t] {
			why = append(why, "named only by the mirror's topicsPattern")
		}
		if len(why) > 0 {
			h.Reason = strings.Join(why, "; ")
			held = append(held, h)
		}
	}
	return held, nil
}

// guardLabTopics is down's data guard: before the mirrored topics go, it
// looks at them, and asks again — or, without a prompt, wants
// --delete-data — for every one that holds records, could not be read, or
// only a pattern names. A target it cannot look at fails closed: every
// mirrored topic then counts as unchecked.
func guardLabTopics(ctx context.Context, d *labDiscovery, timeout time.Duration, yes, deleteData bool) error {
	if d.Name == "" || d.TargetNamespace == "" || d.TargetCluster == "" {
		return nil // labDown deletes no topic without these
	}
	if len(d.ReplicatedTopics()) == 0 && len(d.patternLegs()) == 0 {
		return nil
	}
	held, err := inspectLabTopics(ctx, d, timeout)
	if err != nil {
		reason := "could not be checked: " + err.Error()
		held = nil
		for _, t := range d.ReplicatedTopics() {
			held = append(held, topicHolding{Topic: t, Reason: reason})
		}
		for _, p := range d.patternLegs() {
			migrateWarn(fmt.Sprintf("the topics topicsPattern %q mirrored could not be listed, so they are kept", p.Pattern))
		}
	}
	return confirmTopicData(d, held, yes, deleteData)
}

// confirmTopicData asks for the second consent the held topics need: none
// when there are none or --delete-data gave it; a prompt naming each on a
// terminal without --yes; otherwise a refusal naming them and the two ways
// out. A refusal removes nothing.
func confirmTopicData(d *labDiscovery, held []topicHolding, yes, deleteData bool) error {
	if len(held) == 0 {
		return nil
	}
	if outputMode != "json" {
		output.SubHeader("Mirrored topics that need a second confirmation")
	}
	names := make([]string, 0, len(held))
	for _, h := range held {
		label := h.Topic
		if h.Records.Measured && h.Records.Sum > 0 {
			label += fmt.Sprintf(" (%d records)", h.Records.Sum)
		}
		names = append(names, label)
		migrateWarn(fmt.Sprintf("topic %s on %s: %s", h.Topic, d.TargetCluster, h.Reason))
	}
	if deleteData {
		migrateWarn("--delete-data: deleting " + strings.Join(names, ", ") + " with whatever they hold")
		return nil
	}
	if yes || outputMode == "json" || !IsInteractive() {
		return fmt.Errorf("refusing to delete %d mirrored topic(s) on %s that hold records or could not be checked (%s): pass --delete-data with --yes to delete them, or --keep-topics to remove the lab and leave them",
			len(held), d.TargetCluster, strings.Join(names, ", "))
	}
	ok, err := confirm(fmt.Sprintf("Delete %s on %s, records and all?", strings.Join(names, ", "), d.TargetCluster))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cancelled: nothing was removed (--keep-topics removes the lab and leaves its topics)")
	}
	return nil
}
