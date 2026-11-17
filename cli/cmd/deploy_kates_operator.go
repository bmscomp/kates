package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// --kates-mode: how deploy installs Kates. Standalone is the kates chart with
// its own PostgreSQL. Operator installs the Kates operator, a PostgreSQL for
// Kates (the kates-db release of charts/cdc-postgresql, unless
// --kates-db-secret names one) and a Kates resource the operator turns into
// one backend, or a quorum of three or five.
const (
	katesModeStandalone = "standalone"
	katesModeOperator   = "operator"

	// The Kates resource's name: its Service is then kates, as the chart's
	// is, so ports, the API key and the dashboards find it unchanged.
	katesResourceName = "kates"
	katesDBRelease    = "kates-db"
	// The Secret the Kates resource reads its database from, with --kates-db-secret unset.
	katesDBConnection = "kates-db-connection"
)

func katesOperatorMode() bool { return deployKatesMode == katesModeOperator }

// validateKatesMode refuses flag combinations the chosen mode cannot honour,
// before anything reaches the cluster.
func validateKatesMode() error {
	switch deployKatesMode {
	case katesModeStandalone, katesModeOperator:
	default:
		return fmt.Errorf("--kates-mode %q: use 'standalone' or 'operator'", deployKatesMode)
	}
	if !slices.Contains([]int{1, 3, 5}, deployKatesReplicas) {
		return fmt.Errorf("--kates-replicas %d: use 1, or 3 or 5 for a quorum", deployKatesReplicas)
	}
	if katesOperatorMode() {
		if deployAdminless {
			return errors.New("--kates-mode operator installs the Kates operator and its CRD, which needs cluster admin: drop --adminless, or keep --kates-mode standalone")
		}
		return nil
	}
	if deployKatesReplicas != 1 {
		return errors.New("--kates-replicas needs --kates-mode operator: the kates chart runs one backend")
	}
	if deployKatesDBSecret != "" {
		return errors.New("--kates-db-secret needs --kates-mode operator: the kates chart brings its own PostgreSQL")
	}
	return nil
}

// checkKatesModeSwitch refuses to install Kates one way over an install made
// the other way: both would claim the Service kates.
func checkKatesModeSwitch(ctx context.Context, appNS string) error {
	if !deployWithKates {
		return nil
	}
	if katesOperatorMode() && isHelmReleaseDeployedFn(ctx, "kates", appNS) {
		return fmt.Errorf("kates is installed standalone in %s (Helm release kates): uninstall it first (kates clean, or helm uninstall kates -n %s), or keep --kates-mode standalone", appNS, appNS)
	}
	if !katesOperatorMode() && katesResourceExists(ctx, appNS) {
		return fmt.Errorf("kates is installed by the Kates operator in %s (Kates resource %s): use --kates-mode operator, or delete it first (kubectl delete kates %s -n %s)", appNS, katesResourceName, katesResourceName, appNS)
	}
	return nil
}

func katesResourceExists(ctx context.Context, namespace string) bool {
	out, err := runExecOutputFn(ctx, "kubectl", "get", "kates.kates.io", katesResourceName, "-n", namespace,
		"--ignore-not-found", "-o", "name")
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// operatorChartPath is the operator chart: operator/chart in a checkout,
// charts/kates-operator among the published packages.
func operatorChartPath() string {
	if chartsPublished {
		return chartRootPath("charts/kates-operator")
	}
	return chartRootPath("operator/chart")
}

// katesOperatorComponent is the Kates operator, watching the Kates namespace
// and binding the backend's cluster role to the Kates pods there.
func (dc *deployContext) katesOperatorComponent() component {
	operatorNS := dc.ns.operator
	appNS := dc.ns.app
	return component{
		id: "kates-operator", name: "Kates Operator", namespace: operatorNS, timeout: 5 * time.Minute,
		enabled: func() bool { return componentSelected("kates-operator") },
		banner: func(context.Context) string {
			return fmt.Sprintf("\n🤖 Deploying the Kates Operator (Namespace: %s, watching %s)...", operatorNS, appNS)
		},
		install: func(ctx context.Context) error {
			// Deploy makes each namespace itself: Helm's --create-namespace is dropped.
			if err := ensureConfiguredDeployNamespace(ctx, operatorNS); err != nil {
				return err
			}
			args := []string{"upgrade", "--install", "kates-operator", operatorChartPath(),
				"-n", operatorNS, "--create-namespace",
				"--set-json", "watchNamespaces=" + jsonList(appNS),
				"--set-json", "rbac.backendNamespaces=" + jsonList(appNS),
				"--wait", "--timeout", "5m"}
			if mirror := imageRegistry(); mirror != "" {
				args = append(args, "--set-string", "global.imageRegistry="+mirror)
			}
			args = append(args, dc.policyArgs("kates-operator")...)
			return runHelmFn(ctx, args...)
		},
		wait: func(ctx context.Context) error {
			return dc.waitPods(ctx, operatorNS, "app.kubernetes.io/name=kates-operator", 5*time.Minute, "")
		},
	}
}

// katesDBComponent is the PostgreSQL the Kates resource stores in: this
// repository's cdc-postgresql chart as release kates-db, open to the Kates
// pods alone, and the Secret kates-db-connection that names it.
func (dc *deployContext) katesDBComponent() component {
	appNS := dc.ns.app
	return component{
		id: "kates-db", name: "PostgreSQL (Kates)", namespace: appNS, timeout: 5 * time.Minute,
		enabled: func() bool { return componentSelected("kates-db") },
		banner: func(context.Context) string {
			return fmt.Sprintf("\n🐘 Deploying PostgreSQL for Kates (Namespace: %s)...", appNS)
		},
		install: func(ctx context.Context) error {
			if err := ensureConfiguredDeployNamespace(ctx, appNS); err != nil {
				return err
			}
			// Kept across runs: a new password would lock Kates out of its data.
			password, err := secretValueOrNew(ctx, appNS, katesDBRelease, "password")
			if err != nil {
				return err
			}
			admin, err := secretValueOrNew(ctx, appNS, katesDBRelease, "postgres-password")
			if err != nil {
				return err
			}
			// In a 0600 file, not --set: --verbose prints every Helm command.
			values, err := writePrivateValues(dc.valuesFile, "kates-db-values.yaml", map[string]any{
				"auth": map[string]any{
					"username": "kates", "database": "kates",
					"password": password, "postgresPassword": admin,
				},
				"networkPolicy": map[string]any{
					"allowPods": []map[string]string{{"app.kubernetes.io/instance": katesResourceName}},
				},
			})
			if err != nil {
				return err
			}
			defer os.Remove(values)
			args := append([]string{"upgrade", "--install", katesDBRelease, chartRootPath("charts/cdc-postgresql"),
				"-n", appNS, "--create-namespace", "-f", values, "--timeout", "5m"},
				dc.policyArgs("cdc-postgresql")...)
			if err := runHelmFn(ctx, args...); err != nil {
				return err
			}
			return applySecret(ctx, appNS, katesDBConnection, map[string]string{
				"url":      fmt.Sprintf("jdbc:postgresql://%s.%s.svc:5432/kates", katesDBRelease, appNS),
				"username": "kates",
				"password": password,
			})
		},
		wait: func(ctx context.Context) error {
			return dc.waitPods(ctx, appNS, "app.kubernetes.io/instance="+katesDBRelease, 5*time.Minute, "")
		},
	}
}

// installKatesResource is the Kates backend under the operator: its API key,
// then the Kates resource, which the operator turns into a StatefulSet and a
// Service named kates.
func (dc *deployContext) installKatesResource(ctx context.Context) error {
	appNS := dc.ns.app
	if err := ensureConfiguredDeployNamespace(ctx, appNS); err != nil {
		return err
	}
	database := katesDBConnection
	if deployKatesDBSecret != "" {
		database = deployKatesDBSecret
		if out, err := runExecOutputFn(ctx, "kubectl", "get", "secret", database, "-n", appNS,
			"--ignore-not-found", "-o", "name"); err != nil || len(bytes.TrimSpace(out)) == 0 {
			return fmt.Errorf("--kates-db-secret %s: no such Secret in %s; it needs keys url (jdbc:postgresql://...), username and password", database, appNS)
		}
	}
	// The key Secret the kates chart makes, under the name and key the CLI and
	// the operator's drain read; kept across runs, as the chart keeps it.
	key, err := secretValueOrNew(ctx, appNS, apiKeySecretName, "api-key")
	if err != nil {
		return err
	}
	if err := applySecret(ctx, appNS, apiKeySecretName, map[string]string{"api-key": key}); err != nil {
		return err
	}
	version, image, err := dc.katesImage(ctx)
	if err != nil {
		return err
	}
	manifest, err := katesResourceManifest(katesResourceSpec{
		namespace: appNS, version: version, image: image, replicas: deployKatesReplicas,
		database:  database,
		bootstrap: fmt.Sprintf("%s-kafka-bootstrap.%s.svc:9092", dc.primary.Name, dc.primary.Namespace),
		kafkaNS:   dc.ns.kafka, cluster: dc.primary.Name, oneBroker: oneBroker(),
	})
	if err != nil {
		return err
	}
	dl.Printf("    - Kates resource %s/%s: %d member(s), image %s:%s\n", appNS, katesResourceName, deployKatesReplicas, image, version)
	return runExecStdinFn(ctx, "kubectl", []string{"apply", "--server-side", "--field-manager", "kates-deploy", "-f", "-"}, manifest)
}

// How long, and how often, deploy reads the Kates resource's Ready condition.
var katesReadyTimeout, katesReadyPoll = 10 * time.Minute, 5 * time.Second

// waitKatesResource waits for the operator to report the backend Ready: every
// member up and, with a quorum, a leader. It stops at once on NoQuorum, which
// only another image ends, rather than waiting out the timeout.
func waitKatesResource(ctx context.Context, appNS string) error {
	const ready = `{.status.conditions[?(@.type=="Ready")]`
	deadline := time.Now().Add(katesReadyTimeout)
	why := ""
	for {
		out, err := runExecOutputFn(ctx, "kubectl", "get", "kates.kates.io", katesResourceName, "-n", appNS, "-o",
			"jsonpath={.metadata.generation}|"+ready+".observedGeneration}|"+ready+".status}|"+ready+".reason}|"+ready+".message}")
		// A condition from before the spec this deploy applied says nothing about it.
		if fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 5); err == nil && len(fields) == 5 && fields[0] == fields[1] {
			if fields[2] == "True" {
				return nil
			}
			why = fields[3] + ": " + fields[4]
			if fields[3] == "NoQuorum" {
				return fmt.Errorf("the Kates resource %s/%s cannot become Ready (%s)", appNS, katesResourceName, why)
			}
		}
		if time.Now().After(deadline) {
			if why == "" {
				return fmt.Errorf("the Kates resource %s/%s is not Ready after %s", appNS, katesResourceName, katesReadyTimeout)
			}
			return fmt.Errorf("the Kates resource %s/%s is not Ready after %s (%s)", appNS, katesResourceName, katesReadyTimeout, why)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(katesReadyPoll):
		}
	}
}

// katesImage is the image the backend runs, as the chart path picks it: a
// local build on kind when one is there, else the published native image.
func (dc *deployContext) katesImage(ctx context.Context) (version, image string, err error) {
	if dc.isKind {
		args, err := kindBackendImageArgs(ctx, dc.report.Context)
		if err != nil {
			return "", "", err
		}
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "image.tag="); ok {
				version = v
			}
			if v, ok := strings.CutPrefix(a, "image.repository="); ok {
				image = v
			}
		}
	}
	if version == "" {
		if version, err = valuesImageTag(chartRootPath(publishedNativeValues)); err != nil {
			return "", "", err
		}
	}
	if image == "" {
		image = "ghcr.io/bmscomp/kates"
		if mirror := imageRegistry(); mirror != "" {
			image = mirror + "/bmscomp/kates"
		}
	}
	return version, image, nil
}

// valuesImageTag reads image.tag from a values file.
func valuesImageTag(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var values struct {
		Image struct {
			Tag string `yaml:"tag"`
		} `yaml:"image"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if values.Image.Tag == "" {
		return "", fmt.Errorf("%s sets no image.tag", path)
	}
	return values.Image.Tag, nil
}

type katesResourceSpec struct {
	namespace, version, image, database, bootstrap, kafkaNS, cluster string
	replicas                                                         int
	oneBroker                                                        bool
}

// katesResourceManifest is the Kates resource, with the Kafka settings the
// kates chart's values give the backend.
func katesResourceManifest(s katesResourceSpec) (string, error) {
	env := []map[string]string{
		// What the chart's ConfigMap sets from kafka.clusterName and the Kafka namespace.
		{"name": "KATES_CHAOS_KAFKA_NAMESPACE", "value": s.kafkaNS},
		{"name": "KATES_TOPOLOGY_KAFKA_NAMESPACE", "value": s.kafkaNS},
		{"name": "KATES_CHAOS_KAFKA_CLUSTER", "value": s.cluster},
		{"name": "KATES_TOPOLOGY_KAFKA_CLUSTER", "value": s.cluster},
		{"name": "KATES_CDC_TOPIC_NAMESPACE", "value": s.kafkaNS},
	}
	if s.oneBroker {
		for _, name := range []string{"KATES_DEFAULTS_REPLICATION_FACTOR", "KATES_DEFAULTS_MIN_INSYNC_REPLICAS", "KATES_OUTBOX_TOPIC_REPLICATION_FACTOR"} {
			env = append(env, map[string]string{"name": name, "value": "1"})
		}
	}
	resource := map[string]any{
		"apiVersion": "kates.io/v1alpha1",
		"kind":       "Kates",
		"metadata":   map[string]any{"name": katesResourceName, "namespace": s.namespace},
		"spec": map[string]any{
			"version":  s.version,
			"image":    s.image,
			"replicas": s.replicas,
			"database": map[string]any{"secretRef": map[string]any{"name": s.database}},
			"kafka": map[string]any{
				"bootstrapServers": s.bootstrap,
				"securityProtocol": "SASL_PLAINTEXT",
				"sasl": map[string]any{
					"mechanism":         "SCRAM-SHA-512",
					"username":          "kates-backend",
					"passwordSecretRef": map[string]any{"name": "kates-backend", "key": "password"},
				},
			},
			"apiKeySecretRef": map[string]any{"name": apiKeySecretName, "key": "api-key"},
			"env":             env,
		},
	}
	out, err := yaml.Marshal(resource)
	return string(out), err
}

// secretValueOrNew is key of an existing Secret, or a new random value.
func secretValueOrNew(ctx context.Context, namespace, name, key string) (string, error) {
	out, err := runExecOutputFn(ctx, "kubectl", "get", "secret", name, "-n", namespace,
		"--ignore-not-found", "-o", "jsonpath={.data."+jsonpathKey(key)+"}")
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		raw, decodeErr := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(out)))
		if decodeErr != nil {
			return "", fmt.Errorf("secret %s/%s key %s is not base64", namespace, name, key)
		}
		return string(raw), nil
	}
	return randomAlnum(32)
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomAlnum(n int) (string, error) {
	var b strings.Builder
	for range n {
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(alnum))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alnum[i.Int64()])
	}
	return b.String(), nil
}

// adoptAPIKeySecret hands the API key Secret an operator-mode install wrote
// to the kates chart: Helm refuses to install over an object without its
// ownership metadata, and its lookup then keeps the key clients already hold.
// A Secret Helm already manages is left as it is.
func adoptAPIKeySecret(ctx context.Context, namespace string) error {
	out, err := runExecOutputFn(ctx, "kubectl", "get", "secret", apiKeySecretName, "-n", namespace, "--ignore-not-found",
		"-o", `jsonpath={.metadata.name}|{.metadata.labels.app\.kubernetes\.io/managed-by}`)
	if err != nil {
		return fmt.Errorf("secret %s in %s could not be read: %w", apiKeySecretName, namespace, err)
	}
	name, managedBy, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	if name == "" || managedBy != "" {
		return nil
	}
	dl.Printf("    - Handing Secret %s/%s, written for the Kates operator, to Helm release kates\n", namespace, apiKeySecretName)
	for _, args := range [][]string{
		{"label", "secret", apiKeySecretName, "-n", namespace, "--overwrite", "app.kubernetes.io/managed-by=Helm"},
		{"annotate", "secret", apiKeySecretName, "-n", namespace, "--overwrite",
			"meta.helm.sh/release-name=kates", "meta.helm.sh/release-namespace=" + namespace},
	} {
		if out, err := runExecCombinedFn(ctx, "kubectl", args...); err != nil {
			return fmt.Errorf("secret %s in %s could not be handed to Helm: %w: %s", apiKeySecretName, namespace, err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// applySecret writes Secret name in namespace, through stdin so no value is
// on a command line.
func applySecret(ctx context.Context, namespace, name string, data map[string]string) error {
	secret := map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"stringData": data,
	}
	out, err := yaml.Marshal(secret)
	if err != nil {
		return err
	}
	if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, string(out)); err != nil {
		return fmt.Errorf("secret %s could not be written in %s: %w", name, namespace, err)
	}
	return nil
}

// writePrivateValues writes values beside the deploy's values file, readable
// by this user alone.
func writePrivateValues(beside, name string, values map[string]any) (string, error) {
	out, err := yaml.Marshal(values)
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(beside), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, out, 0o600)
}

func jsonList(items ...string) string {
	out, _ := json.Marshal(items)
	return string(out)
}
