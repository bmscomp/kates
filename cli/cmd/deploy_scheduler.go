package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"golang.org/x/sync/errgroup"
)

// The deploy pipeline is a list of components run by one scheduler. Each
// component says what it needs before it starts, how it tells it is already
// there, how it installs and how it waits; the scheduler owns everything the
// three group functions used to repeat: the "already deployed → skip" block
// (eight copies), the dashboard bookkeeping (StartComponent, FinishComponent,
// the progress step), the KafkaUser credential copies into the component's
// namespace (four copies), and the ordering, which was three groups run one
// after the other with the waits of a group serialised after its installs.
// The dependencies say the same order where it matters — operators before
// custom resources, Kafka before Connect, MirrorMaker 2 and the UI, Kates
// after Kafka and PostgreSQL, everything after the monitoring CRDs — and
// nothing more, so two components with no dependency between them run side
// by side.

// deployParallelism caps how many components install or wait at once. The
// operators used to run three abreast; this keeps that and nothing more, so a
// kind cluster is not asked to start every pod of the stack at once.
const deployParallelism = 3

// component is one unit of the deploy pipeline.
type component struct {
	// id is the dashboard's name for it (the component table's ID). Empty
	// for a bookkeeping step the dashboard does not show.
	id string
	// name is how messages and errors call it.
	name string
	// deps are the ids that must be done (installed and ready) first.
	deps []string
	// after are the ids whose install must have been submitted first; their
	// readiness is not waited on. Kafka renders its PodMonitor only if the
	// monitoring CRDs exist when Helm renders it, so it installs after the
	// monitoring chart was applied, not after Prometheus is up.
	after []string
	// enabled says whether the flags turned the component on.
	enabled func() bool
	// namespace is where the component goes; the scheduler makes sure it
	// exists before copying credentials into it.
	namespace string
	// secrets are the KafkaUser credentials, Secrets in the Kafka namespace,
	// that the component reads from its own namespace. Copied before install
	// when the namespaces differ; a refused copy fails the component there
	// and then, where the consumer's pod used to sit in
	// CreateContainerConfigError until its readiness wait timed out.
	secrets []secretCopy
	// timeout is the readiness budget the dashboard shows.
	timeout time.Duration
	// installed reports a component already deployed: the scheduler prints
	// skipMsg, marks it done and installs nothing. nil means always install
	// (helm upgrade --install converges).
	installed func(ctx context.Context) (bool, error)
	skipMsg   string
	// banner is the line printed when the component installs.
	banner func(ctx context.Context) string
	// install submits the component to the cluster; wait blocks until it is
	// ready. Either may be nil. A wait returning errNotReady marks the
	// component failed on the dashboard but does not stop the deploy.
	install func(ctx context.Context) error
	wait    func(ctx context.Context) error
}

// secretCopy names a KafkaUser Secret a component reads from its own
// namespace.
type secretCopy struct {
	name string
	// jaas also copies the sasl.jaas.config entry (Kafka Connect's worker
	// reads it).
	jaas bool
	// wait is how long to wait for the Secret to appear when the KafkaUser is
	// not reconciled yet; zero reads it once.
	wait time.Duration
}

// errNotReady is returned by a wait whose component may stay unready without
// failing the deploy (PostgreSQL: the CDC demo degrades, the stack does not).
var errNotReady = errors.New("not ready")

// deployNode is a component with the channels the scheduler orders on.
type deployNode struct {
	component
	installed chan struct{} // closed once install returned, or the component was skipped
	done      chan struct{} // closed once the component is ready, or skipped
	once      sync.Once
}

func (n *deployNode) markInstalled() {
	n.once.Do(func() { close(n.installed) })
}

// runComponents runs the enabled components under their dependencies, at
// most deployParallelism at a time, and returns the first failure. A failure
// cancels the context the others run on; a component waiting on a failed one
// never starts. A dependency on a component the flags left out is ignored.
func runComponents(ctx context.Context, dc *deployContext, comps []component) error {
	nodes := map[string]*deployNode{}
	var order []*deployNode
	for _, c := range comps {
		if c.enabled != nil && !c.enabled() {
			continue
		}
		n := &deployNode{component: c, installed: make(chan struct{}), done: make(chan struct{})}
		order = append(order, n)
		if c.id != "" {
			nodes[c.id] = n
		} else {
			nodes[c.name] = n
		}
	}

	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, deployParallelism)
	for _, n := range order {
		g.Go(func() error {
			for _, dep := range n.deps {
				if d, ok := nodes[dep]; ok {
					select {
					case <-d.done:
					case <-gCtx.Done():
						return gCtx.Err()
					}
				}
			}
			for _, dep := range n.after {
				if d, ok := nodes[dep]; ok {
					select {
					case <-d.installed:
					case <-gCtx.Done():
						return gCtx.Err()
					}
				}
			}
			select {
			case sem <- struct{}{}:
			case <-gCtx.Done():
				return gCtx.Err()
			}
			defer func() { <-sem }()
			if err := dc.runComponent(gCtx, n); err != nil {
				return err
			}
			n.markInstalled()
			close(n.done)
			return nil
		})
	}
	return g.Wait()
}

// runComponent is one component's life: the skip check, the dashboard start,
// the credential copies, the install, the wait, the dashboard finish.
func (dc *deployContext) runComponent(ctx context.Context, n *deployNode) error {
	c := n.component
	if c.installed != nil {
		skip, err := c.installed(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		if skip {
			dl.Println(output.Glyphs().Skip + "  " + c.skipMsg)
			dc.finish(c, true)
			return nil
		}
	}
	if c.banner != nil {
		dl.Println(c.banner(ctx))
	}
	if c.id != "" {
		dl.StartComponent(c.id, c.timeout)
	}
	if err := dc.copyCredentials(ctx, c); err != nil {
		dc.finish(c, false)
		return fmt.Errorf("%s: %w", c.name, err)
	}
	if c.install != nil {
		if err := c.install(ctx); err != nil {
			dc.finish(c, false)
			return fmt.Errorf("%s: %w", c.name, err)
		}
		dc.recordInstalled(c.id)
	}
	n.markInstalled()
	if c.wait != nil {
		switch err := c.wait(ctx); {
		case errors.Is(err, errNotReady):
			dc.finish(c, false)
			return nil
		case err != nil:
			dc.finish(c, false)
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	dc.finish(c, true)
	return nil
}

// finish closes a component on the dashboard and counts its step. A
// bookkeeping component (no id) is not a step.
func (dc *deployContext) finish(c component, ok bool) {
	if c.id == "" {
		return
	}
	dl.FinishComponent(c.id, ok)
	if ok {
		dc.advanceStep()
	}
}

// recordInstalled notes that this run installed the component, as opposed to
// finding it there. wireKyvernoScrape reads it: an existing Kyverno is left
// as it is, like every other existing release.
func (dc *deployContext) recordInstalled(id string) {
	if id == "" {
		return
	}
	dc.installedMu.Lock()
	defer dc.installedMu.Unlock()
	if dc.installedNow == nil {
		dc.installedNow = map[string]bool{}
	}
	dc.installedNow[id] = true
}

// installedThisRun says whether this run installed the component.
func (dc *deployContext) installedThisRun(id string) bool {
	dc.installedMu.Lock()
	defer dc.installedMu.Unlock()
	return dc.installedNow[id]
}

// copyCredentials puts the component's KafkaUser Secrets into its namespace,
// when that is not the Kafka namespace, creating the namespace first. The
// KafkaUsers were waited on (kafka-users) before any component with secrets
// starts, so a Secret still missing is reported, not waited on, unless the
// component asks for a wait.
func (dc *deployContext) copyCredentials(ctx context.Context, c component) error {
	if len(c.secrets) == 0 || c.namespace == "" || c.namespace == dc.ns.kafka {
		return nil
	}
	if err := ensureConfiguredDeployNamespace(ctx, c.namespace); err != nil {
		return err
	}
	for _, s := range c.secrets {
		dl.Printf("    - Copying %s credentials to namespace %s...\n", s.name, c.namespace)
		found, err := copyUserSecret(ctx, s, dc.ns.kafka, c.namespace)
		if err != nil {
			dl.Printf("    ✗ %v\n", err)
			return err
		}
		if !found {
			dl.Printf("    ⚠  Secret '%s' not found in namespace %s — KafkaUser may not be ready\n", s.name, dc.ns.kafka)
		}
	}
	return nil
}

// copyUserSecret copies Secret s from fromNS into toNS, polling for up to
// s.wait while the KafkaUser has not produced it yet.
func copyUserSecret(ctx context.Context, s secretCopy, fromNS, toNS string) (found bool, err error) {
	deadline := deployClock.Now().Add(s.wait)
	for {
		found, err = copySecretPassword(ctx, s.name, fromNS, toNS, s.jaas)
		if found || err != nil || !deployClock.Now().Before(deadline) {
			return found, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deployClock.After(5 * time.Second):
		}
	}
}

// waitPods is a component's readiness wait on its pods. errMsg, when set, is
// printed with the error (the operators say which one failed).
func (dc *deployContext) waitPods(ctx context.Context, namespace, selector string, timeout time.Duration, errMsg string) error {
	if err := waitComponentReadySilent(ctx, namespace, selector, timeout); err != nil {
		if errMsg != "" {
			output.Error(fmt.Sprintf("%s: %v", errMsg, err))
		}
		return err
	}
	return nil
}

// ── Clock ─────────────────────────────────────────────────────────────────

// deployClocker is the time the deploy's waits and retries read.
type deployClocker interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) Sleep(d time.Duration)                  { time.Sleep(d) }

// deployClock is the clock every deploy wait reads. Tests hand it a fake, so
// a readiness timeout takes no real time; the deploy used to skip every wait
// under isTesting instead, and no timeout was ever exercised.
var deployClock deployClocker = realClock{}
