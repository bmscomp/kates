package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Poll and heartbeat intervals of the silent waits; variables so a test can
// shorten them.
var (
	silentPollInterval = 3 * time.Second
	silentHeartbeat    = 30 * time.Second
)

// How often a wait reads why its pods are Pending, and how long the scheduler
// must say the same pods fit on no node before the wait gives up.
const (
	podBlockReadEvery = 15 * time.Second
	podBlockHold      = 3 * time.Minute
)

// waitComponentReadySilent polls pods matching a label selector until all are
// Running/Succeeded or the timeout expires. It does NOT call
// dl.StartComponent/FinishComponent — the caller (deploy.go) is responsible
// for managing the dashboard component lifecycle.
func waitComponentReadySilent(ctx context.Context, namespace, selector string, timeout time.Duration) error {
	start := deployClock.Now()
	deadline := start.Add(timeout)
	lastBeat := start
	why := podWaitWhy{namespace: namespace, selector: selector}
	for deployClock.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", "pods", "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.phase}{','}{end}")
		var phases []string
		if err == nil {
			phases = podPhases(string(out))
			if len(phases) > 0 && readyPods(phases) == len(phases) {
				return nil
			}
		}

		// With no dashboard (CI, a pipe) nothing else says the deploy is
		// alive: a line every 30 s shows the pod counts moving, where the
		// log used to go quiet for up to fifteen minutes.
		if !dl.live() && deployClock.Now().Sub(lastBeat) >= silentHeartbeat {
			lastBeat = deployClock.Now()
			dl.Printf("    %s\n", podHeartbeat(namespace, selector, phases, deployClock.Now().Sub(start)))
		}

		if err := why.check(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deployClock.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for pods (selector=%s) to become ready%s", selector, why.suffix())
}

// podWaitWhy says, during a readiness wait, why the wait's pods are Pending:
// each distinct reason once, and in the wait's error. A read that fails or is
// refused changes nothing, so the wait runs as if no one had asked.
type podWaitWhy struct {
	namespace, selector string
	nextRead            time.Time
	said                map[string]bool
	line                string // the last read's reason, for the error
	heldBy              string // what has been blocking since heldSince
	heldSince           time.Time
}

// check reads the reason when one is due. Its error stops the wait: every node
// has been short of CPU or memory for the same pods for podBlockHold, with no
// autoscaler adding one, which a longer wait will not change.
func (w *podWaitWhy) check(ctx context.Context) error {
	now := deployClock.Now()
	if now.Before(w.nextRead) {
		return nil
	}
	w.nextRead = now.Add(podBlockReadEvery)
	b, ok := readPodBlock(ctx, w.namespace, w.selector)
	if !ok {
		w.heldBy = ""
		return nil
	}
	if b.detail == "" {
		w.line, w.heldBy = "", ""
		return nil
	}
	reason := b.problem + ": " + b.detail
	w.line = fmt.Sprintf("%d pod(s) in %s %s", len(b.pods), w.namespace, reason)
	if !w.said[reason] {
		if w.said == nil {
			w.said = map[string]bool{}
		}
		w.said[reason] = true
		dl.Printf("    ⚠ %s\n", w.line)
	}
	heldBy := reason + " " + strings.Join(b.pods, " ")
	switch {
	case b.scalingUp || !everyNodeShort(b.detail):
		w.heldBy = ""
	case heldBy != w.heldBy:
		w.heldBy, w.heldSince = heldBy, now
	case now.Sub(w.heldSince) >= podBlockHold:
		return fmt.Errorf("%s, unchanged for %s: stopped waiting", w.line, podBlockHold)
	}
	return nil
}

// suffix is the last reason, for the end of a timeout error.
func (w *podWaitWhy) suffix() string {
	if w.line == "" {
		return ""
	}
	return ": " + w.line
}

// podBlock is what keeps a wait's Pending pods from starting. An empty detail
// means nothing does, as far as the events say.
type podBlock struct {
	problem, detail string
	pods            []string // the UIDs of the pods it holds back
	scalingUp       bool     // an autoscaler answered for a Pending pod
}

// podEvent is the part of a core/v1 Event a wait reads. The kubelet stamps
// lastTimestamp; the scheduler leaves it null and stamps eventTime and series.
type podEvent struct {
	InvolvedObject struct {
		UID string `json:"uid"`
	} `json:"involvedObject"`
	Reason        string    `json:"reason"`
	Message       string    `json:"message"`
	LastTimestamp time.Time `json:"lastTimestamp"`
	EventTime     time.Time `json:"eventTime"`
	Series        struct {
		LastObservedTime time.Time `json:"lastObservedTime"`
	} `json:"series"`
}

func (e podEvent) at() time.Time {
	t := e.LastTimestamp
	for _, o := range []time.Time{e.EventTime, e.Series.LastObservedTime} {
		if o.After(t) {
			t = o
		}
	}
	return t
}

// readPodBlock reads the first reason a Pending pod of selector gives. Events
// are matched by UID, because a Strimzi pod comes back under its old name and
// an hour of the old pod's events remain; and a pod's newest event decides, so
// a FailedScheduling it has since got past does not count. ok is false when
// either read failed.
func readPodBlock(ctx context.Context, namespace, selector string) (b podBlock, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runExecOutputFn(ctx, "kubectl", "get", "pods", "-n", namespace, "-l", selector, "--field-selector=status.phase=Pending", "-o", "jsonpath={.items[*].metadata.uid}")
	if err != nil {
		return podBlock{}, false
	}
	uids := strings.Fields(string(out))
	if len(uids) == 0 {
		return podBlock{}, true
	}
	out, err = runExecOutputFn(ctx, "kubectl", "get", "events", "-n", namespace, "--field-selector=involvedObject.kind=Pod", "-o", "json")
	if err != nil {
		return podBlock{}, false
	}
	var events struct {
		Items []podEvent `json:"items"`
	}
	if err := json.Unmarshal(out, &events); err != nil {
		return podBlock{}, false
	}
	slices.SortStableFunc(events.Items, func(x, y podEvent) int { return x.at().Compare(y.at()) })

	newest := map[string]podEvent{}
	pullError := map[string]string{} // the event that names the image and the cause
	for _, e := range events.Items {
		uid := e.InvolvedObject.UID
		newest[uid] = e
		if strings.Contains(e.Message, "Failed to pull image") {
			pullError[uid] = e.Message
		}
		if slices.Contains(uids, uid) && (e.Reason == "TriggeredScaleUp" || e.Reason == "Nominated") {
			b.scalingUp = true
		}
	}
	for _, uid := range uids {
		e := newest[uid]
		problem, detail := "", ""
		switch {
		case e.Reason == "FailedScheduling":
			problem = "cannot be scheduled"
			detail, _, _ = strings.Cut(e.Message, " preemption:")
			detail = strings.TrimRight(detail, ". ")
		case imagePullFailed(e.Message):
			problem, detail = "cannot pull an image", e.Message
			if m := pullError[uid]; m != "" {
				detail = m
			}
		default:
			continue
		}
		detail = strings.Join(strings.Fields(detail), " ")
		if b.detail == "" {
			b.problem, b.detail = problem, detail
		}
		if problem == b.problem && detail == b.detail {
			b.pods = append(b.pods, uid)
		}
	}
	return b, true
}

func imagePullFailed(message string) bool {
	for _, s := range []string{"ImagePullBackOff", "ErrImagePull", "Failed to pull image", "Back-off pulling image"} {
		if strings.Contains(message, s) {
			return true
		}
	}
	return false
}

var nodesShort = regexp.MustCompile(`^0/\d+ nodes are available: (?:(?:, )?\d+ (?:Insufficient (?:cpu|memory)|node\(s\) had untolerated taint \{node-role\.kubernetes\.io/control-plane: \}))+$`)

// everyNodeShort says the scheduler's message refuses every node for CPU or
// memory, or for the control-plane taint, which no wait clears either. A node
// refused for anything else (another taint, a volume, an affinity) is a reason
// this wait does not judge, so it does not stop on it.
func everyNodeShort(detail string) bool {
	return nodesShort.MatchString(detail) && strings.Contains(detail, "Insufficient")
}

// podPhases splits kubectl's "Running,Pending," list, dropping the empty tail.
func podPhases(out string) []string {
	var phases []string
	for _, p := range strings.Split(strings.TrimSpace(out), ",") {
		if p != "" {
			phases = append(phases, p)
		}
	}
	return phases
}

// readyPods counts the phases that are done waiting.
func readyPods(phases []string) int {
	n := 0
	for _, p := range phases {
		if p == "Running" || p == "Succeeded" {
			n++
		}
	}
	return n
}

// podHeartbeat is one progress line for a log: "still waiting for pods in
// kafka (strimzi.io/cluster=krafter): 2/3 ready, 1 Pending (1m30s)".
func podHeartbeat(namespace, selector string, phases []string, elapsed time.Duration) string {
	where := fmt.Sprintf("pods in %s (%s)", namespace, selector)
	if len(phases) == 0 {
		return fmt.Sprintf("still waiting for %s: none yet (%s)", where, elapsed.Round(time.Second))
	}
	counts := map[string]int{}
	for _, p := range phases {
		if p != "Running" && p != "Succeeded" {
			counts[p]++
		}
	}
	others := make([]string, 0, len(counts))
	for p, n := range counts {
		others = append(others, fmt.Sprintf("%d %s", n, p))
	}
	sort.Strings(others)
	line := fmt.Sprintf("still waiting for %s: %d/%d ready", where, readyPods(phases), len(phases))
	if len(others) > 0 {
		line += ", " + strings.Join(others, ", ")
	}
	return fmt.Sprintf("%s (%s)", line, elapsed.Round(time.Second))
}

// waitCustomResourceReadySilent polls custom resources matching a label selector
// until all have Ready=True condition or the timeout expires.
func waitCustomResourceReadySilent(ctx context.Context, kind, namespace, selector string, timeout time.Duration) error {
	deadline := deployClock.Now().Add(timeout)
	for deployClock.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.conditions[?(@.type==\"Ready\")].status}{','}{end}")
		if err == nil {
			statuses := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, s := range statuses {
				if s != "" {
					count++
					if s != "True" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deployClock.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for %s to become ready", kind)
}

func waitNamedCustomResourceReadySilent(ctx context.Context, kind, namespace, name string, timeout time.Duration) error {
	deadline := deployClock.Now().Add(timeout)
	pods := podWaitWhy{namespace: namespace, selector: "strimzi.io/cluster=" + name}
	for deployClock.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, name, "-n", namespace, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		if err == nil && strings.Contains(string(out), "True") {
			return nil
		}
		if err := pods.check(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deployClock.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for %s/%s to become ready%s", kind, name, pods.suffix())
}

func waitKafkaUsersReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkauser", namespace, "strimzi.io/cluster="+deployKafkaName, timeout)
}

func waitKafkaConnectReadySilent(ctx context.Context, namespace, name string, timeout time.Duration) error {
	return waitNamedCustomResourceReadySilent(ctx, "kafkaconnect", namespace, name, timeout)
}

func waitConnectorReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkaconnector", namespace, "strimzi.io/cluster=connect-cluster", timeout)
}
