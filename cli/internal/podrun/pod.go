package podrun

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// ContainerName is the name of the single container in a client pod.
	ContainerName = "client"
	// DefaultReadyTimeout is how long Ensure waits for the pod to become
	// Ready when the caller passes no timeout — the scripts'
	// --pod-running-timeout=5m, which covers a cold image pull.
	DefaultReadyTimeout = 5 * time.Minute

	// LabelTestPod is the label the kafka-cluster chart's NetworkPolicy
	// admits to the brokers. Without it every client pod times out against a
	// healthy cluster, which is how the scripts learned about it.
	LabelTestPod = "kates.io/test-pod"
	// LabelPodrun marks a pod as created by this package.
	LabelPodrun = "kates.io/podrun"
	// LabelPartOf is the platform's part-of label.
	LabelPartOf = "app.kubernetes.io/part-of"

	// entrypoint keeps the pod alive until it is deleted. It is the only
	// shell program in this package: it is the pod's own command, a
	// constant, and nothing a caller passes ever reaches it. The trap makes
	// SIGTERM end the pod at once instead of after the grace period.
	entrypoint = "trap : TERM INT; sleep infinity & wait"
)

// SecretRef names one key of a Secret in the pod's namespace.
type SecretRef struct {
	Secret, Key string
}

// Pod describes a long-lived client pod. Ensure creates it, Exec and
// ExecInput run argv inside it, Delete removes it.
type Pod struct {
	Namespace, Name, Image string
	// Labels are merged with the fixed labels every client pod carries
	// (LabelTestPod, LabelPartOf, LabelPodrun); the fixed ones win.
	Labels map[string]string
	// Env are plain environment variables. LOG_DIR=/tmp is always set (the
	// Kafka tool scripts write their log4j output there) unless Env carries
	// its own LOG_DIR.
	Env map[string]string
	// SecretEnv are environment variables sourced from Secrets, rendered as
	// valueFrom.secretKeyRef so the value never appears in the pod spec.
	SecretEnv map[string]SecretRef
	// ImagePullPolicy is left to Kubernetes when empty.
	ImagePullPolicy string
	// RunAsUser is the uid the container runs as. Leave it 0 to trust the
	// image's own numeric USER (quay.io/strimzi/kafka runs as 1001,
	// kates-legacy-kafka as 1000). Set it for an image whose USER is a name
	// rather than a number — apache/kafka's is `appuser` — because with
	// runAsNonRoot the kubelet refuses to start such a container ("cannot
	// verify user is non-root") unless the uid is given explicitly.
	RunAsUser int64
}

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate checks that the pod can be named and addressed: namespace and name
// are DNS labels, an image is given, environment names are identifiers and
// every SecretRef is complete.
func (p Pod) Validate() error {
	switch {
	case p.Namespace == "":
		return errors.New("pod namespace is required")
	case !dnsLabel.MatchString(p.Namespace) || len(p.Namespace) > 63:
		return fmt.Errorf("pod namespace %q is not a DNS label", p.Namespace)
	case p.Name == "":
		return errors.New("pod name is required")
	case !dnsLabel.MatchString(p.Name) || len(p.Name) > 63:
		return fmt.Errorf("pod name %q is not a DNS label", p.Name)
	case p.Image == "":
		return errors.New("pod image is required")
	case p.RunAsUser < 0:
		return fmt.Errorf("pod runAsUser %d is negative", p.RunAsUser)
	}
	for k := range p.Env {
		if !envName.MatchString(k) {
			return fmt.Errorf("env name %q is not an identifier", k)
		}
	}
	for k, ref := range p.SecretEnv {
		if !envName.MatchString(k) {
			return fmt.Errorf("secret env name %q is not an identifier", k)
		}
		if ref.Secret == "" || ref.Key == "" {
			return fmt.Errorf("secret env %s needs both a Secret name and a key", k)
		}
		if _, dup := p.Env[k]; dup {
			return fmt.Errorf("env %s is set both as a value and from a Secret", k)
		}
	}
	return nil
}

// The manifest types below exist so the YAML kubectl receives is rendered by
// a marshaller, in a fixed key order, from typed fields — never by string
// concatenation.
type podManifest struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   podMetadata `yaml:"metadata"`
	Spec       podSpec     `yaml:"spec"`
}

type podMetadata struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Labels    map[string]string `yaml:"labels"`
}

type podSpec struct {
	RestartPolicy                 string             `yaml:"restartPolicy"`
	TerminationGracePeriodSeconds int                `yaml:"terminationGracePeriodSeconds"`
	AutomountServiceAccountToken  bool               `yaml:"automountServiceAccountToken"`
	SecurityContext               podSecurityContext `yaml:"securityContext"`
	Containers                    []container        `yaml:"containers"`
}

type podSecurityContext struct {
	RunAsNonRoot   bool              `yaml:"runAsNonRoot"`
	RunAsUser      *int64            `yaml:"runAsUser,omitempty"`
	SeccompProfile map[string]string `yaml:"seccompProfile"`
}

type container struct {
	Name            string                   `yaml:"name"`
	Image           string                   `yaml:"image"`
	ImagePullPolicy string                   `yaml:"imagePullPolicy,omitempty"`
	Command         []string                 `yaml:"command"`
	Env             []envVar                 `yaml:"env"`
	SecurityContext containerSecurityContext `yaml:"securityContext"`
	Resources       resources                `yaml:"resources"`
}

type envVar struct {
	Name      string     `yaml:"name"`
	Value     string     `yaml:"value,omitempty"`
	ValueFrom *valueFrom `yaml:"valueFrom,omitempty"`
}

type valueFrom struct {
	SecretKeyRef secretKeyRef `yaml:"secretKeyRef"`
}

type secretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

type containerSecurityContext struct {
	AllowPrivilegeEscalation bool                `yaml:"allowPrivilegeEscalation"`
	Capabilities             map[string][]string `yaml:"capabilities"`
}

type resources struct {
	Requests map[string]string `yaml:"requests"`
	Limits   map[string]string `yaml:"limits"`
}

// Manifest renders the pod as the YAML Ensure applies. It is exported so a
// --dry-run can print exactly what would be created.
func Manifest(p Pod) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	labels := map[string]string{}
	for k, v := range p.Labels {
		labels[k] = v
	}
	labels[LabelTestPod] = "true"
	labels[LabelPartOf] = "kates"
	labels[LabelPodrun] = "true"

	env := []envVar{}
	if _, own := p.Env["LOG_DIR"]; !own {
		env = append(env, envVar{Name: "LOG_DIR", Value: "/tmp"})
	}
	for _, k := range sortedKeys(p.Env) {
		env = append(env, envVar{Name: k, Value: p.Env[k]})
	}
	secretNames := make([]string, 0, len(p.SecretEnv))
	for k := range p.SecretEnv {
		secretNames = append(secretNames, k)
	}
	sort.Strings(secretNames)
	for _, k := range secretNames {
		ref := p.SecretEnv[k]
		env = append(env, envVar{Name: k, ValueFrom: &valueFrom{SecretKeyRef: secretKeyRef{Name: ref.Secret, Key: ref.Key}}})
	}

	var runAsUser *int64
	if p.RunAsUser > 0 {
		uid := p.RunAsUser
		runAsUser = &uid
	}

	m := podManifest{
		APIVersion: "v1",
		Kind:       "Pod",
		Metadata:   podMetadata{Name: p.Name, Namespace: p.Namespace, Labels: labels},
		Spec: podSpec{
			RestartPolicy:                 "Never",
			TerminationGracePeriodSeconds: 10,
			AutomountServiceAccountToken:  false,
			SecurityContext: podSecurityContext{
				RunAsNonRoot:   true,
				RunAsUser:      runAsUser,
				SeccompProfile: map[string]string{"type": "RuntimeDefault"},
			},
			Containers: []container{{
				Name:            ContainerName,
				Image:           p.Image,
				ImagePullPolicy: p.ImagePullPolicy,
				Command:         []string{"/bin/sh", "-c", entrypoint},
				Env:             env,
				SecurityContext: containerSecurityContext{
					AllowPrivilegeEscalation: false,
					Capabilities:             map[string][]string{"drop": {"ALL"}},
				},
				Resources: resources{
					Requests: map[string]string{"memory": "64Mi", "cpu": "50m"},
					Limits:   map[string]string{"memory": "512Mi", "cpu": "500m"},
				},
			}},
		},
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("render pod %s/%s: %w", p.Namespace, p.Name, err)
	}
	return string(out), nil
}

// Ensure makes the client pod exist and be Ready. It is idempotent: a Running
// or Pending pod is only waited for; a pod that has terminated (restartPolicy
// is Never, so a finished pod stays as Succeeded or Failed) is deleted and
// recreated; an absent pod is applied from Manifest. A timeout of zero means
// DefaultReadyTimeout.
func Ensure(ctx context.Context, r Runner, p Pod, timeout time.Duration) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	phase, err := r.Run(ctx, "kubectl", "-n", p.Namespace, "get", "pod", p.Name,
		"--ignore-not-found", "-o", "jsonpath={.status.phase}")
	if err != nil {
		return fmt.Errorf("look up pod %s/%s: %w", p.Namespace, p.Name, err)
	}
	phase = strings.TrimSpace(phase)
	create := phase == ""
	if phase == "Succeeded" || phase == "Failed" {
		if err := Delete(ctx, r, p); err != nil {
			return fmt.Errorf("replace terminated (%s) pod: %w", phase, err)
		}
		create = true
	}
	if create {
		manifest, err := Manifest(p)
		if err != nil {
			return err
		}
		if _, err := r.RunInput(ctx, manifest, "kubectl", "-n", p.Namespace, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("create pod %s/%s: %w", p.Namespace, p.Name, err)
		}
	}
	if _, err := r.Run(ctx, "kubectl", "-n", p.Namespace, "wait", "--for=condition=Ready",
		"pod/"+p.Name, "--timeout="+formatTimeout(timeout)); err != nil {
		return fmt.Errorf("pod %s/%s did not become Ready within %s: %w", p.Namespace, p.Name, timeout, err)
	}
	return nil
}

// Delete removes the client pod; a pod that does not exist is not an error.
func Delete(ctx context.Context, r Runner, p Pod) error {
	if p.Namespace == "" || p.Name == "" {
		return errors.New("pod namespace and name are required")
	}
	if _, err := r.Run(ctx, "kubectl", "-n", p.Namespace, "delete", "pod", p.Name, "--ignore-not-found"); err != nil {
		return fmt.Errorf("delete pod %s/%s: %w", p.Namespace, p.Name, err)
	}
	return nil
}

// Exec runs argv inside the pod and returns its standard output. argv is
// passed to `kubectl exec … --` untouched: there is no shell between the
// caller and the process, so nothing needs quoting and nothing can be
// interpolated.
func Exec(ctx context.Context, r Runner, p Pod, argv ...string) (string, error) {
	if err := checkExec(p, argv); err != nil {
		return "", err
	}
	args := append([]string{"-n", p.Namespace, "exec", p.Name, "-c", ContainerName, "--"}, argv...)
	return r.Run(ctx, "kubectl", args...)
}

// ExecInput is Exec with stdin supplied to the process (`kubectl exec -i`),
// which is how a console producer receives its records: from Go, never from
// a here-document.
func ExecInput(ctx context.Context, r Runner, p Pod, stdin string, argv ...string) (string, error) {
	if err := checkExec(p, argv); err != nil {
		return "", err
	}
	args := append([]string{"-n", p.Namespace, "exec", "-i", p.Name, "-c", ContainerName, "--"}, argv...)
	return r.RunInput(ctx, stdin, "kubectl", args...)
}

// WriteFile writes content to path inside the pod by piping it through tee
// (no shell, no redirect, no here-document). The path must be writable by the
// container's user; /tmp always is. The value never appears on a command
// line, which is what makes this the right way to place a client.properties
// holding a password.
func WriteFile(ctx context.Context, r Runner, p Pod, path, content string) error {
	if path == "" {
		return errors.New("file path is required")
	}
	echo, err := ExecInput(ctx, r, p, content, "tee", path)
	if err != nil {
		return fmt.Errorf("write %s in pod %s/%s: %w", path, p.Namespace, p.Name, err)
	}
	// tee echoes what it wrote; a mismatch is a short or altered write.
	if echo != content {
		return fmt.Errorf("write %s in pod %s/%s: wrote %d bytes, expected %d", path, p.Namespace, p.Name, len(echo), len(content))
	}
	return nil
}

func checkExec(p Pod, argv []string) error {
	if p.Namespace == "" || p.Name == "" {
		return errors.New("pod namespace and name are required")
	}
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("exec needs a command")
	}
	return nil
}

func formatTimeout(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
