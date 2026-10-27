package cmd

import (
	"github.com/bmscomp/kates/cli/internal/helm"
	"github.com/bmscomp/kates/cli/internal/kubectl"
	"github.com/bmscomp/kates/cli/internal/proc"
)

// commandRunner runs every kubectl and helm call made through the typed
// clients below: deploy status, kyverno, connect and doctor. It is the OS
// runner; a test puts a proc.Fake here (stubRunner), and pinKubeContext wraps
// it so every call carries the cluster the gate chose.
//
// The deploy orchestration (runExecFn and friends in deploy.go) and the
// migrate, version and operator commands (procRunner in runner.go) still have
// seams of their own; they are the next to move onto this one.
var commandRunner proc.Runner = proc.OS{}

// newKubectl is the kubectl client for namespace, through commandRunner. An
// empty namespace leaves each call to choose its own.
func newKubectl(namespace string) *kubectl.Client {
	return kubectl.NewWith(commandRunner, namespace)
}

// newHelm is the helm client for namespace, through commandRunner.
func newHelm(namespace string) *helm.Client {
	return helm.NewWith(commandRunner, namespace)
}
