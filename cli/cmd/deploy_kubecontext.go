package cmd

import (
	"context"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// Pinning a deploy to the cluster the gate chose.
//
// useTargetContext (cluster_gate.go) makes the chosen cluster kubectl's
// current context, so the user's shell and scripts/port-forward.sh follow the
// deploy there. The deploy itself does not rely on that. It runs for ten
// minutes or more and makes over a hundred kubectl and helm calls, and each
// one read the current context when it ran. Anything that moved the context
// in the meantime moved the rest of the deploy with it: a `kubectl config
// use-context` in another terminal, or `kind create cluster`, which switches
// the context to the cluster it makes. Half the stack landed on one cluster
// and half on the other.
//
// So every call carries the cluster by name: --context for kubectl,
// --kube-context for helm. The flags are added once, at the process seams
// below, rather than at each call site, so a call added later cannot leave
// them out.

// kubeContextFlag is the flag that selects a kube context for the command
// name, or "" for a command that does not take one.
func kubeContextFlag(name string) string {
	switch name {
	case "kubectl":
		return "--context"
	case "helm":
		return "--kube-context"
	}
	return ""
}

// withKubeContext returns args with the flag that points kubectl or helm at
// kubeContext. It returns args unchanged for an empty context, for any other
// command, and for args that already choose a context.
//
// The flag goes last, or just before a "--": what follows `kubectl exec … --`
// is the container's command line. Keeping the subcommand where it was also
// keeps `kubectl port-forward` matching the pattern kates ports uses to stop
// old forwards.
func withKubeContext(kubeContext, name string, args []string) []string {
	flag := kubeContextFlag(name)
	if kubeContext == "" || flag == "" {
		return args
	}

	end := len(args)
	for i, a := range args {
		if a == "--" {
			end = i
			break
		}
	}
	for _, a := range args[:end] {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return args
		}
	}

	pinned := make([]string, 0, len(args)+1)
	pinned = append(pinned, args[:end]...)
	pinned = append(pinned, flag+"="+kubeContext)
	return append(pinned, args[end:]...)
}

// pinKubeContext points every kubectl and helm call that goes through the
// process seams at kubeContext, and returns the function that restores the
// seams. It wraps what each seam holds rather than replacing it, so a test's
// stub still receives the call, flag included.
//
// Nothing may call through the seams concurrently with pinning or unpinning.
// runDeploy pins before it starts any goroutine and unpins after its deploy
// goroutine has finished; the dashboard's poller keeps the function it was
// built with (NewDeployDashboard) rather than reading the seam again.
func pinKubeContext(kubeContext string) (unpin func()) {
	if kubeContext == "" {
		return func() {}
	}

	execFn, stdinFn, helmFn := runExecFn, runExecStdinFn, runHelmFn
	outputFn, combinedFn := runExecOutputFn, runExecCombinedFn
	procFn, executor := runProcFn, defaultExecutor

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		return execFn(ctx, name, withKubeContext(kubeContext, name, args)...)
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return stdinFn(ctx, name, withKubeContext(kubeContext, name, args), stdinData)
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		return helmFn(ctx, withKubeContext(kubeContext, "helm", args)...)
	}
	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return outputFn(ctx, name, withKubeContext(kubeContext, name, args)...)
	}
	runExecCombinedFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return combinedFn(ctx, name, withKubeContext(kubeContext, name, args)...)
	}
	// defaultRunner: version resolution and the picker's catalogue.
	runProcFn = func(ctx context.Context, stdin, name string, args ...string) (string, error) {
		return procFn(ctx, stdin, name, withKubeContext(kubeContext, name, args)...)
	}
	// The introspection (pkg/detect).
	defaultExecutor = kubeContextExecutor{inner: executor, kubeContext: kubeContext}

	return func() {
		runExecFn, runExecStdinFn, runHelmFn = execFn, stdinFn, helmFn
		runExecOutputFn, runExecCombinedFn = outputFn, combinedFn
		runProcFn, defaultExecutor = procFn, executor
	}
}

// kubeContextExecutor is a detect.CommandExecutor whose kubectl and helm calls
// all go to one kube context.
type kubeContextExecutor struct {
	inner       detect.CommandExecutor
	kubeContext string
}

func (e kubeContextExecutor) Exec(name string, args ...string) (string, error) {
	return e.inner.Exec(name, withKubeContext(e.kubeContext, name, args)...)
}

func (e kubeContextExecutor) LookPath(file string) (string, error) {
	return e.inner.LookPath(file)
}
