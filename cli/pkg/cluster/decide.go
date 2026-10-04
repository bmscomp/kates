package cluster

import "fmt"

// Outcome is what the deploy path should do next. Resolve computes it from
// detected state alone, with no I/O and no prompting, so the whole decision
// tree is testable and the UI stays a thin renderer over it.
type Outcome int

const (
	// UseContext: exactly one reachable context — proceed, no questions.
	UseContext Outcome = iota
	// ChooseContext: several reachable contexts — the user must pick.
	ChooseContext
	// OfferKind: nothing reachable, but Docker and kind are both ready.
	OfferKind
	// NeedKind: Docker runs but kind is missing — install kind, then retry.
	NeedKind
	// NeedDockerRunning: Docker is installed but the daemon is stopped.
	NeedDockerRunning
	// NeedDocker: no Docker at all.
	NeedDocker
)

// Decision is Resolve's result.
type Decision struct {
	Outcome Outcome
	// Candidates are the reachable contexts (for UseContext / ChooseContext).
	Candidates []Context
	// Unreachable are contexts that exist but did not answer. Kept so the UI can
	// say "3 contexts found, none reachable" instead of the untrue "no clusters
	// found" — a stale kubeconfig is the normal laptop case and deserves its own
	// message.
	Unreachable []Context
}

// Reason renders a short human explanation of why this outcome was chosen.
func (d Decision) Reason() string {
	switch d.Outcome {
	case UseContext:
		return fmt.Sprintf("using the only reachable cluster: %s", d.Candidates[0].Name)
	case ChooseContext:
		return fmt.Sprintf("%d reachable clusters found", len(d.Candidates))
	case OfferKind:
		if len(d.Unreachable) > 0 {
			return fmt.Sprintf("%d context(s) configured but none reachable; Docker and kind are available", len(d.Unreachable))
		}
		return "no Kubernetes context configured; Docker and kind are available"
	case NeedKind:
		return "Docker is running but kind is not installed"
	case NeedDockerRunning:
		return "Docker is installed but the daemon is not running"
	default:
		return "no Docker and no reachable Kubernetes cluster"
	}
}

// Resolve maps detected state onto an outcome.
//
// Ordering matters: a reachable cluster always wins over offering to build one.
// We never propose creating a Kind cluster while a usable cluster exists.
func Resolve(contexts []Context, env Environment) Decision {
	reachable := Reachable(contexts)

	var unreachable []Context
	for _, c := range contexts {
		if !c.Reachable {
			unreachable = append(unreachable, c)
		}
	}

	switch {
	case len(reachable) == 1:
		return Decision{Outcome: UseContext, Candidates: reachable, Unreachable: unreachable}
	case len(reachable) > 1:
		return Decision{Outcome: ChooseContext, Candidates: reachable, Unreachable: unreachable}
	}

	// Nothing reachable — can we build one?
	d := Decision{Unreachable: unreachable}
	switch {
	case env.Docker == DockerNotInstalled:
		d.Outcome = NeedDocker
	case env.Docker == DockerInstalledNotRunning:
		d.Outcome = NeedDockerRunning
	case !env.KindInstalled:
		d.Outcome = NeedKind
	default:
		d.Outcome = OfferKind
	}
	return d
}
