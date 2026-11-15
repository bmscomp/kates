package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// setKatesMode sets the operator-mode flags for one test.
func setKatesMode(t *testing.T, mode string, replicas int, dbSecret string, adminless bool) {
	t.Helper()
	m, r, s, a, k := deployKatesMode, deployKatesReplicas, deployKatesDBSecret, deployAdminless, deployWithKates
	t.Cleanup(func() {
		deployKatesMode, deployKatesReplicas, deployKatesDBSecret, deployAdminless, deployWithKates = m, r, s, a, k
	})
	deployKatesMode, deployKatesReplicas, deployKatesDBSecret, deployAdminless, deployWithKates = mode, replicas, dbSecret, adminless, true
}

func TestKatesModeFlagsAreCheckedBeforeAnyInstall(t *testing.T) {
	for _, tc := range []struct {
		name, mode, secret string
		replicas           int
		adminless          bool
		wantErr            string
	}{
		{name: "standalone default", mode: "standalone", replicas: 1},
		{name: "operator, one member", mode: "operator", replicas: 1},
		{name: "operator quorum and own database", mode: "operator", replicas: 5, secret: "db"},
		{name: "unknown mode", mode: "helm", replicas: 1, wantErr: "--kates-mode"},
		{name: "even quorum", mode: "operator", replicas: 2, wantErr: "--kates-replicas 2"},
		{name: "replicas without the operator", mode: "standalone", replicas: 3, wantErr: "needs --kates-mode operator"},
		{name: "database without the operator", mode: "standalone", replicas: 1, secret: "db", wantErr: "--kates-db-secret needs"},
		{name: "operator admin-less", mode: "operator", replicas: 1, adminless: true, wantErr: "cluster admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setKatesMode(t, tc.mode, tc.replicas, tc.secret, tc.adminless)
			err := validateKatesMode()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

func TestOperatorModeSelectsTheOperatorAndADatabaseUnlessOneIsNamed(t *testing.T) {
	setKatesMode(t, "standalone", 1, "", false)
	if componentSelected("kates-operator") || componentSelected("kates-db") || slices.Contains(deployChartDirs(), "kates-operator") {
		t.Fatal("standalone selects the operator, its database or its chart")
	}
	setKatesMode(t, "operator", 3, "", false)
	if !componentSelected("kates-operator") || !componentSelected("kates-db") || !slices.Contains(deployChartDirs(), "kates-operator") {
		t.Fatal("operator mode lacks the operator, the database or the operator chart")
	}
	setKatesMode(t, "operator", 3, "mine", false)
	if componentSelected("kates-db") {
		t.Fatal("--kates-db-secret still installs PostgreSQL")
	}
}

func TestTheKatesResourceCarriesTheChartsKafkaSettings(t *testing.T) {
	manifest, err := katesResourceManifest(katesResourceSpec{
		namespace: "kates", version: "1.41.0-native", image: "ghcr.io/bmscomp/kates", replicas: 3,
		database: katesDBConnection, bootstrap: "krafter-kafka-bootstrap.kafka.svc:9092",
		kafkaNS: "kafka", cluster: "krafter", oneBroker: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Kind     string `yaml:"kind"`
		Metadata struct{ Name, Namespace string }
		Spec     struct {
			Version  string `yaml:"version"`
			Replicas int    `yaml:"replicas"`
			Database struct {
				SecretRef struct{ Name string } `yaml:"secretRef"`
			} `yaml:"database"`
			Kafka struct {
				BootstrapServers string `yaml:"bootstrapServers"`
				Sasl             struct {
					Username          string                     `yaml:"username"`
					PasswordSecretRef struct{ Name, Key string } `yaml:"passwordSecretRef"`
				} `yaml:"sasl"`
			} `yaml:"kafka"`
			APIKeySecretRef struct{ Name, Key string } `yaml:"apiKeySecretRef"`
			Env             []struct{ Name, Value string }
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(manifest), &got); err != nil {
		t.Fatalf("%v\n%s", err, manifest)
	}
	if got.Kind != "Kates" || got.Metadata.Name != "kates" || got.Spec.Replicas != 3 || got.Spec.Version != "1.41.0-native" {
		t.Fatalf("resource = %+v", got)
	}
	if got.Spec.Database.SecretRef.Name != katesDBConnection || got.Spec.Kafka.Sasl.PasswordSecretRef.Name != "kates-backend" ||
		got.Spec.APIKeySecretRef.Name != "kates-api-key" || got.Spec.APIKeySecretRef.Key != "api-key" {
		t.Fatalf("secret references = %+v", got.Spec)
	}
	env := map[string]string{}
	for _, e := range got.Spec.Env {
		env[e.Name] = e.Value
	}
	if env["KATES_TOPOLOGY_KAFKA_CLUSTER"] != "krafter" || env["KATES_CHAOS_KAFKA_NAMESPACE"] != "kafka" || env["KATES_OUTBOX_TOPIC_REPLICATION_FACTOR"] != "1" {
		t.Fatalf("env = %v", env)
	}
}

// operatorModeCluster stubs the cluster calls operator mode makes: Secret
// reads answer from secrets, applies and helm calls are recorded.
func operatorModeCluster(t *testing.T, secrets map[string]string) (applied, helm *[]string) {
	t.Helper()
	origOut, origStdin, origHelm, origDeployed := runExecOutputFn, runExecStdinFn, runHelmFn, isHelmReleaseDeployedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecStdinFn, runHelmFn, isHelmReleaseDeployedFn = origOut, origStdin, origHelm, origDeployed
	})
	applied, helm = &[]string{}, &[]string{}
	runExecOutputFn = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		for key, value := range secrets {
			if strings.Contains(joined, key) {
				return []byte(value), nil
			}
		}
		return nil, nil
	}
	runExecStdinFn = func(_ context.Context, _ string, _ []string, stdin string) error {
		*applied = append(*applied, stdin)
		return nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		*helm = append(*helm, strings.Join(args, " "))
		return nil
	}
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }
	return applied, helm
}

func TestKatesDBKeepsItsPasswordOffTheCommandLineAndAcrossRuns(t *testing.T) {
	setKatesMode(t, "operator", 1, "", false)
	kept := base64.StdEncoding.EncodeToString([]byte("kept-password"))
	applied, helm := operatorModeCluster(t, map[string]string{
		"secret kates-db -n kates --ignore-not-found -o jsonpath={.data.password}": kept,
		"get namespace": `{"metadata":{"name":"kates"}}`,
	})
	dc := &deployContext{ctx: context.Background(), ns: resolvedNamespaces{app: "kates"},
		valuesFile: filepath.Join(t.TempDir(), "values-detected.yaml")}
	if err := dc.katesDBComponent().install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*helm) != 1 || !strings.Contains((*helm)[0], "upgrade --install kates-db") || strings.Contains((*helm)[0], "kept-password") {
		t.Fatalf("helm = %v, want one kates-db upgrade with no password in it", *helm)
	}
	values := (*helm)[0][strings.Index((*helm)[0], "-f ")+3:]
	values = strings.Fields(values)[0]
	if _, err := os.Stat(values); !os.IsNotExist(err) {
		t.Errorf("the values file %s was left behind", values)
	}
	connection := (*applied)[len(*applied)-1]
	for _, want := range []string{"name: kates-db-connection", "jdbc:postgresql://kates-db.kates.svc:5432/kates", "password: kept-password"} {
		if !strings.Contains(connection, want) {
			t.Fatalf("connection Secret lacks %q:\n%s", want, connection)
		}
	}
}

func TestTheKatesResourceGetsAKeyAndNamesAnExistingDatabase(t *testing.T) {
	setKatesMode(t, "operator", 3, "own-db", false)
	origRoot := chartRoot
	t.Cleanup(func() { chartRoot = origRoot })
	chartRoot = "../.."
	key := base64.StdEncoding.EncodeToString([]byte("existing-key"))
	applied, _ := operatorModeCluster(t, map[string]string{
		"secret own-db -n kates": "secret/own-db",
		"kates-api-key -n kates": key,
		"get namespace":          `{"metadata":{"name":"kates"}}`,
	})
	dc := &deployContext{ctx: context.Background(), ns: resolvedNamespaces{app: "kates", kafka: "kafka"},
		primary: primaryCluster{Name: "krafter", Namespace: "kafka"}}
	if err := dc.installKatesResource(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(*applied, "\n---\n")
	for _, want := range []string{"api-key: existing-key", "kind: Kates", "name: own-db", "replicas: 3"} {
		if !strings.Contains(all, want) {
			t.Fatalf("applied lacks %q:\n%s", want, all)
		}
	}
}

func TestAMissingDatabaseSecretIsNamed(t *testing.T) {
	setKatesMode(t, "operator", 1, "nope", false)
	operatorModeCluster(t, map[string]string{"get namespace": `{"metadata":{"name":"kates"}}`})
	dc := &deployContext{ctx: context.Background(), ns: resolvedNamespaces{app: "kates"}}
	err := dc.installKatesResource(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--kates-db-secret nope") {
		t.Fatalf("error = %v", err)
	}
}

func TestOneModeIsNotInstalledOverTheOther(t *testing.T) {
	setKatesMode(t, "operator", 1, "", false)
	operatorModeCluster(t, nil)
	isHelmReleaseDeployedFn = func(_ context.Context, release, _ string) bool { return release == "kates" }
	if err := checkKatesModeSwitch(context.Background(), "kates"); err == nil || !strings.Contains(err.Error(), "installed standalone") {
		t.Fatalf("operator over a standalone install: %v", err)
	}
	setKatesMode(t, "standalone", 1, "", false)
	operatorModeCluster(t, map[string]string{"kates.kates.io kates": "kates.kates.io/kates"})
	if err := checkKatesModeSwitch(context.Background(), "kates"); err == nil || !strings.Contains(err.Error(), "by the Kates operator") {
		t.Fatalf("standalone over an operator install: %v", err)
	}
}

func TestTheOperatorChartIsCheckedAgainstItsOwnSigner(t *testing.T) {
	if chartSigner("oci://ghcr.io/bmscomp/charts/kates-operator") != operatorChartSignerIdentity {
		t.Fatal("the operator chart is checked against publish-charts.yml")
	}
	if chartSigner("oci://ghcr.io/bmscomp/charts/kates") != chartSignerIdentity {
		t.Fatal("the kates chart is not checked against publish-charts.yml")
	}
}

func TestANotReadyKatesResourceSaysWhy(t *testing.T) {
	operatorModeCluster(t, map[string]string{"jsonpath={.status.conditions": "NoLeader: the quorum has no leader"})
	origExec := runExecFn
	t.Cleanup(func() { runExecFn = origExec })
	runExecFn = func(context.Context, string, ...string) error { return errors.New("timed out") }
	err := waitKatesResource(context.Background(), "kates")
	if err == nil || !strings.Contains(err.Error(), "NoLeader: the quorum has no leader") {
		t.Fatalf("error = %v", err)
	}
}
