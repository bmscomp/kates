package cmd

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"github.com/spf13/pflag"
)

func TestIsClusterAdmin_ReadsTheAnswer(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })

	for out, want := range map[string][2]bool{"yes\n": {true, true}, "no\n": {false, true}, "": {false, false}} {
		runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }
		if admin, ok := isClusterAdmin(context.Background()); admin != want[0] || ok != want[1] {
			t.Errorf("can-i %q: got admin=%v ok=%v, want %v", out, admin, ok, want)
		}
	}
}

// resetDeployFlagsChanged makes the deploy command's flags look unset, since
// other tests Set them and pflag never forgets.
func resetDeployFlagsChanged(t *testing.T) {
	t.Helper()
	deployCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

func TestRunDeploy_NonAdminIsToldAboutTheAdminlessProfile(t *testing.T) {
	origAdmin, origHelm := clusterAdminFn, runHelmFn
	t.Cleanup(func() { clusterAdminFn, runHelmFn = origAdmin, origHelm })
	setDeployFlags(t, func() { deployAdminless = false })
	resetDeployFlagsChanged(t)

	clusterAdminFn = func(context.Context) (bool, bool) { return false, true }
	helmCalls := 0
	runHelmFn = func(context.Context, ...string) error { helmCalls++; return nil }

	err := runDeploy(deployCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "kates deploy --adminless") {
		t.Fatalf("want the admin-less suggestion, got %v", err)
	}
	if helmCalls != 0 {
		t.Errorf("%d helm calls before the profile was settled", helmCalls)
	}
}

// adminlessClusterRead is a team account on a cluster with Strimzi: every
// create in its namespace allowed, nothing cluster-wide.
func adminlessClusterRead(refuse string) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "auth can-i * *"):
			return []byte("no\n"), nil
		case strings.Contains(joined, "auth can-i "):
			if refuse != "" && strings.Contains(joined, refuse) {
				return []byte("no\n"), nil
			}
			return []byte("yes\n"), nil
		case strings.Contains(joined, "api-resources --api-group=kafka.strimzi.io"):
			return []byte("kafkas.kafka.strimzi.io\nkafkanodepools.kafka.strimzi.io\n"), nil
		case strings.Contains(joined, "config view --minify"):
			return []byte("team"), nil
		}
		return stubClusterRead(ctx, name, args...)
	}
}

// setAdminlessNamespaces points the isolated topology at a team's namespaces.
func setAdminlessNamespaces(t *testing.T) {
	t.Helper()
	kafka, connect, db, app, ui, mm2 := deployKafkaNS, deployConnectNS, deployDbNS, deployAppNS, deployKafkaUINS, deployMM2NS
	t.Cleanup(func() {
		deployKafkaNS, deployConnectNS, deployDbNS, deployAppNS, deployKafkaUINS, deployMM2NS = kafka, connect, db, app, ui, mm2
	})
	deployKafkaNS, deployConnectNS, deployDbNS, deployAppNS = "team-kafka", "team-connect", "team-db", "team-kates"
	deployKafkaUINS, deployMM2NS = "team-kafka", "team-mm2"
}

func TestRunDeploy_AdminlessInstallsTheStack(t *testing.T) {
	for _, tc := range []struct {
		topology string
		want     map[string]string // release → namespace
	}{
		{"isolated", map[string]string{
			"krafter": "team-kafka", "apicurio": "team-kafka", "kafka-ui": "team-kafka",
			"postgresql": "team-db", "connect-cluster": "team-connect", "mm2": "team-mm2", "kates": "team-kates",
		}},
		// One namespace, the kube context's.
		{"single", map[string]string{
			"krafter": "team", "apicurio": "team", "kafka-ui": "team",
			"postgresql": "team", "connect-cluster": "team", "mm2": "team", "kates": "team",
		}},
	} {
		t.Run(tc.topology, func(t *testing.T) {
			origOutput, origCombined, origHelm, origExec := runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn
			origStdin, origDeployed := runExecStdinFn, isHelmReleaseDeployedFn
			t.Cleanup(func() {
				runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn = origOutput, origCombined, origHelm, origExec
				runExecStdinFn, isHelmReleaseDeployedFn = origStdin, origDeployed
			})
			setDeployFlags(t, func() {
				deployAdminless, deployWithSchemaRegistry, deployDryRun = true, "apicurio", false
				deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithStrimzi = true, true, true, true
				deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = false, false, false
				deployTopology = tc.topology
			})
			setAdminlessNamespaces(t)
			resetDeployFlagsChanged(t)
			t.Chdir(t.TempDir())

			runExecOutputFn, runExecCombinedFn = adminlessClusterRead(""), adminlessClusterRead("")
			var mu sync.Mutex
			var installs []string
			runHelmFn = func(_ context.Context, args ...string) error {
				if len(args) > 2 && args[0] == "upgrade" {
					mu.Lock()
					installs = append(installs, strings.Join(args, " "))
					mu.Unlock()
				}
				return nil
			}
			runExecFn = func(context.Context, string, ...string) error { return nil }
			runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
			isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }

			if err := runDeploy(deployCmd, nil); err != nil {
				t.Fatalf("runDeploy: %v", err)
			}

			installed := map[string]string{}
			for _, c := range installs {
				installed[strings.Fields(c)[2]] = c
			}
			for release, namespace := range tc.want {
				c, ok := installed[release]
				if !ok {
					t.Errorf("release %s not installed; installs: %v", release, installs)
					continue
				}
				if !strings.Contains(c, "-n "+namespace+" ") {
					t.Errorf("release %s not in namespace %s: %s", release, namespace, c)
				}
			}
			for _, release := range []string{"strimzi-operator", "cert-manager", "kyverno", "monitoring", "chaos"} {
				if _, ok := installed[release]; ok {
					t.Errorf("cluster-scoped release %s installed admin-less", release)
				}
			}
			// Helm 4 patches the namespace for it, which a team account may not.
			for _, c := range installs {
				if strings.Contains(c, "--create-namespace") {
					t.Errorf("admin-less install asks helm to create its namespace: %s", c)
				}
			}
			if !strings.Contains(installed["kates"], "--set rbac.scope=namespace") {
				t.Errorf("kates without rbac.scope=namespace: %s", installed["kates"])
			}
		})
	}
}

func TestDetectAdminlessPlatform(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })

	for _, tc := range []struct {
		name, context, cluster string
		nodes                  []detect.NodeInfo
		kind                   bool
		wantContext            string
		wantLocal              bool
	}{
		{"the gate's team context on kind", "fanon", "kind-fanon", nil, true, "kind-fanon", true},
		{"kind's own context", "kind-lab", "kind-lab", nil, true, "kind-lab", true},
		{"a kind node", "lab", "lab", []detect.NodeInfo{{ProviderID: "kind://docker/lab/lab-control-plane"}}, true, "lab", true},
		{"a shared cluster", "team", "prod-eu", []detect.NodeInfo{{ProviderID: "aws:///eu-west-1a/i-1"}}, false, "team", false},
		{"kind inside a name", "mankind", "mankind", nil, false, "mankind", false},
		{"a team's context on k3d", "team", "k3d-lab", nil, false, "team", true},
		{"minikube", "minikube", "minikube", nil, false, "minikube", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.cluster + "\n"), nil }
			report := &detect.DetectReport{Context: tc.context, Provider: "unknown", Nodes: tc.nodes}
			detectAdminlessPlatform(context.Background(), report)
			if isKindReport(report) != tc.kind || report.Context != tc.wantContext || localCluster(report) != tc.wantLocal {
				t.Errorf("kind=%v context=%q local=%v, want %v %q %v", isKindReport(report), report.Context, localCluster(report), tc.kind, tc.wantContext, tc.wantLocal)
			}
		})
	}
}

func TestSizeAdminless(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	one := []detect.NodeInfo{{Name: "n1"}}
	for _, tc := range []struct {
		name    string
		report  detect.DetectReport
		ha      bool
		haGiven bool
		pools   string
		wantHA  bool
	}{
		{"kind with nodes refused", detect.DetectReport{Provider: "kind"}, true, false, "", false},
		{"k3d with nodes refused", detect.DetectReport{Provider: "k3d"}, true, false, "", false},
		{"k3d with several nodes", detect.DetectReport{Provider: "k3d", Nodes: append(one, detect.NodeInfo{Name: "n2"})}, true, false, "", true},
		{"one readable node", detect.DetectReport{Nodes: one}, true, false, "", false},
		{"several nodes", detect.DetectReport{Nodes: append(one, detect.NodeInfo{Name: "n2"})}, true, false, "", true},
		{"nodes refused, not kind", detect.DetectReport{}, true, false, "", true},
		{"--ha on one node is the user's", detect.DetectReport{Nodes: one}, true, true, "", true},
		{"--ha=false on several nodes", detect.DetectReport{Nodes: append(one, detect.NodeInfo{Name: "n2"})}, false, true, "", false},
		{"three brokers stay on kind", detect.DetectReport{Provider: "kind"}, true, false, "3 [\"controller\"]\n3 [\"broker\"]\n", true},
		{"one broker stays on several nodes", detect.DetectReport{Nodes: append(one, detect.NodeInfo{Name: "n2"})}, true, false, "1 [\"controller\",\"broker\"]\n", false},
		{"--ha over one broker is the user's", detect.DetectReport{Provider: "kind"}, true, true, "1 [\"broker\"]\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDeployFlags(t, func() { deployAdminless, deployHA = true, tc.ha })
			runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.pools), nil }
			defaultAdminlessSize(&tc.report, tc.haGiven)
			sizeAdminless(context.Background(), &tc.report, tc.haGiven)
			if deployHA != tc.wantHA {
				t.Errorf("deployHA = %v, want %v", deployHA, tc.wantHA)
			}
		})
	}
}

// The wizard runs between the two: its yes on kind is not undone by the second.
func TestSizeAdminless_WizardYesStands(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	setDeployFlags(t, func() { deployAdminless, deployHA = true, true })
	report := &detect.DetectReport{Provider: "kind"}

	defaultAdminlessSize(report, false)
	if deployHA {
		t.Fatal("the wizard's question does not start from one broker on kind")
	}
	deployHA = true
	sizeAdminless(context.Background(), report, false)
	if !deployHA {
		t.Fatal("the wizard's yes was overridden")
	}
}

func TestBundleKindTarget(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("daemonsets.apps is forbidden")
	}

	if got, isKind := bundleKindTarget("fanon", &detect.DetectReport{Context: "kind-fanon", Provider: "kind"}); got != "kind-fanon" || !isKind {
		t.Errorf("got %q %v, want kind's own context", got, isKind)
	}
	if got, isKind := bundleKindTarget("team", &detect.DetectReport{Context: "team", Provider: "unknown"}); got != "team" || isKind {
		t.Errorf("got %q %v, want the target, not kind", got, isKind)
	}
	if got, isKind := bundleKindTarget("team", nil); got != "team" || isKind {
		t.Errorf("full profile got %q %v", got, isKind)
	}
}

// The chart's rails refuse a replication factor above the brokers, so one
// broker must reach every chart: each of these keeps the deploy from failing,
// or hanging, on one broker.
func TestRunDeploy_AdminlessOnKindSizesEveryChart(t *testing.T) {
	origOutput, origCombined, origHelm, origExec := runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn
	origStdin, origDeployed := runExecStdinFn, isHelmReleaseDeployedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn = origOutput, origCombined, origHelm, origExec
		runExecStdinFn, isHelmReleaseDeployedFn = origStdin, origDeployed
	})
	setDeployFlags(t, func() {
		deployAdminless, deployWithSchemaRegistry, deployDryRun, deployHA = true, "apicurio", false, true
		deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithStrimzi = true, true, true, true
		deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = true, true, true
		deployTopology = "isolated"
	})
	setAdminlessNamespaces(t)
	resetDeployFlagsChanged(t)
	t.Chdir(t.TempDir())

	read := adminlessClusterRead("")
	stub := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "config view --minify -o jsonpath={.clusters[0].name}"):
			return []byte("kind-fanon"), nil
		case name == "docker" || name == "kind":
			return nil, errors.New("no local image")
		}
		return read(ctx, name, args...)
	}
	runExecOutputFn, runExecCombinedFn = stub, stub
	var mu sync.Mutex
	installed := map[string]string{}
	runHelmFn = func(_ context.Context, args ...string) error {
		if len(args) > 2 && args[0] == "upgrade" {
			mu.Lock()
			installed[args[2]] = strings.Join(args, " ")
			mu.Unlock()
		}
		return nil
	}
	runExecFn = func(context.Context, string, ...string) error { return nil }
	runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }

	if err := runDeploy(deployCmd, nil); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	for release, wants := range map[string][]string{
		"krafter": {
			"charts/kafka-cluster/values-kind.yaml",
			"--set nodePools.roleDefaults.broker.replicas=1",
			"--set nodePools.roleDefaults.controller.replicas=1",
			`--set kafka.config.offsets\.topic\.replication\.factor=1`,
			"--set kafka.rack.topologyKey=kubernetes.io/hostname",
		},
		"connect-cluster": {
			"charts/connect-cluster/values-kind.yaml", "--set replicas=1", "--set config.replicationFactor=1",
			"--set kafka.clusterName=krafter",
			"--set networkPolicy.restApi.clients[0].namespace=team-kates",
			"--set networkPolicy.restApi.clients[1].namespace=team-kafka",
			"--set networkPolicy.restApi.clients[1].podSelector.app=kafka-ui",
		},
		"mm2":   {"--set replicas=1", `--set target.config.config\.storage\.replication\.factor=1`},
		"kates": {"--set-string defaults.replicationFactor=1", "--set-string defaults.minInsyncReplicas=1", "--set service.type=ClusterIP"},
		"kafka-ui": {
			"http://connect-cluster-rest-api.team-connect.svc:8083", "apicurio-registry.team-kafka.svc:80",
			"--set service.type=ClusterIP", "--set networkPolicy.extraEgress[0].ports[0].port=8080",
		},
	} {
		for _, want := range wants {
			if !strings.Contains(installed[release], want) {
				t.Errorf("%s: want %q in %s", release, want, installed[release])
			}
		}
	}
	if c := installed["krafter"]; strings.Contains(c, "kafka.replicas") || strings.Contains(c, "zookeeper.replicas") {
		t.Errorf("krafter sets keys the chart ignores: %s", c)
	}
}

func TestCheckAdminlessPrerequisites(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	setDeployFlags(t, func() {
		deployTopology, deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = "isolated", true, true, true
		deployWithMonitoring, deployWithChaos = false, false
	})
	setAdminlessNamespaces(t)
	ns := resolveNamespaces()

	runExecOutputFn = adminlessClusterRead("")
	if err := checkAdminlessPrerequisites(context.Background(), ns); err != nil {
		t.Errorf("everything granted: %v", err)
	}

	runExecOutputFn = adminlessClusterRead("kafkamirrormaker2s")
	err := checkAdminlessPrerequisites(context.Background(), ns)
	if err == nil || !strings.Contains(err.Error(), "team-mm2: kafkamirrormaker2s.kafka.strimzi.io") || strings.Contains(err.Error(), "team-kafka") {
		t.Errorf("want only the refused grant named, in its namespace, got %v", err)
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err := checkAdminlessPrerequisites(context.Background(), ns); err == nil || !strings.Contains(err.Error(), "no Strimzi operator is installed") {
		t.Errorf("want the missing operator named, got %v", err)
	}
}

func TestAdminlessGrants_StrimziWhereEachIsMade(t *testing.T) {
	setDeployFlags(t, func() {
		deployTopology, deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = "isolated", true, true, true
		deployWithMonitoring, deployWithChaos = false, false
	})
	setAdminlessNamespaces(t)
	grants := adminlessGrants(resolveNamespaces())

	for namespace, want := range map[string]string{
		"team-kafka": "kafkas.kafka.strimzi.io", "team-connect": "kafkaconnects.kafka.strimzi.io",
		"team-mm2": "kafkamirrormaker2s.kafka.strimzi.io", "team-db": "statefulsets.apps", "team-kates": "roles.rbac.authorization.k8s.io",
	} {
		if !slices.Contains(grants[namespace], want) {
			t.Errorf("%s: want %s in %v", namespace, want, grants[namespace])
		}
	}
	if slices.Contains(grants["team-kates"], "kafkas.kafka.strimzi.io") {
		t.Errorf("team-kates asked for Kafka resources it never creates: %v", grants["team-kates"])
	}
}

func TestApplyAdminlessProfile_RefusesClusterScopedFlags(t *testing.T) {
	setDeployFlags(t, func() {})
	resetDeployFlagsChanged(t)
	t.Cleanup(func() { resetDeployFlagsChanged(t) })

	_ = deployCmd.Flags().Set("with-chaos", "true")
	if err := applyAdminlessProfile(context.Background(), deployCmd); err == nil || !strings.Contains(err.Error(), "--with-chaos") {
		t.Errorf("want --with-chaos refused, got %v", err)
	}
}

func TestRunDeploy_AdminlessComplianceRoleFollowsTheGrants(t *testing.T) {
	for _, tc := range []struct {
		name, refuse string
		monitoring   string // what discovery lists for monitoring.coreos.com
		discovery    error
		withRole     bool
		ruleInBundle bool // the admin's command renders the PodMonitor rule
	}{
		{"all reads held", "", podMonitorResource, nil, true, false},
		{"no PodMonitor reads", "podmonitors", podMonitorResource, nil, false, true},
		{"no PodMonitor reads, API not served", "podmonitors", "", nil, true, false},
		{"no PodMonitor reads, discovery failed", "podmonitors", "", errors.New("connection refused"), false, true},
		{"no pod reads, API not served", "can-i list pods", "", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origOutput, origCombined, origHelm, origExec := runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn
			origStdin, origDeployed := runExecStdinFn, isHelmReleaseDeployedFn
			t.Cleanup(func() {
				runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn = origOutput, origCombined, origHelm, origExec
				runExecStdinFn, isHelmReleaseDeployedFn = origStdin, origDeployed
			})
			setDeployFlags(t, func() {
				deployAdminless, deployDryRun, deployTopology = true, false, "isolated"
				deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = false, false, false
			})
			setAdminlessNamespaces(t)
			resetDeployFlagsChanged(t)
			t.Chdir(t.TempDir())

			read := adminlessClusterRead(tc.refuse)
			runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if slices.Contains(args, "--api-group=monitoring.coreos.com") {
					return []byte(tc.monitoring), tc.discovery
				}
				return read(ctx, name, args...)
			}
			runExecCombinedFn = read
			var mu sync.Mutex
			var kates string
			runHelmFn = func(_ context.Context, args ...string) error {
				if len(args) > 2 && args[0] == "upgrade" && args[2] == "kates" {
					mu.Lock()
					kates = strings.Join(args, " ")
					mu.Unlock()
				}
				return nil
			}
			runExecFn = func(context.Context, string, ...string) error { return nil }
			runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
			isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }

			var buf bytes.Buffer
			origOut := output.Out
			output.Out = &buf
			t.Cleanup(func() { output.Out = origOut })
			if err := runDeploy(deployCmd, nil); err != nil {
				t.Fatalf("runDeploy: %v", err)
			}
			out := buf.String()
			if got := !strings.Contains(kates, "rbac.kafkaNamespaceRole=false"); got != tc.withRole {
				t.Errorf("Role installed = %v, want %v: %s", got, tc.withRole, kates)
			}
			handoff := "--set networkPolicy.kafka.namespace=team-kafka --show-only templates/rbac.yaml | kubectl apply -f -"
			if printed := strings.Contains(out, handoff); printed == tc.withRole {
				t.Errorf("admin command printed = %v, want %v:\n%s", printed, !tc.withRole, out)
			}
			if tc.refuse == "podmonitors" && !tc.withRole && !strings.Contains(out, "list "+podMonitorResource) {
				t.Errorf("the missing read is not named:\n%s", out)
			}
			if got := strings.Contains(out, "--api-versions monitoring.coreos.com/v1/PodMonitor"); got != tc.ruleInBundle {
				t.Errorf("admin command renders the PodMonitor rule = %v, want %v:\n%s", got, tc.ruleInBundle, out)
			}
		})
	}
}

// adminlessOperatorRead is adminlessClusterRead with a Strimzi operator in
// strimzi-operator watching watches, installed by the kates wrapper chart.
func adminlessOperatorRead(watches string) func(context.Context, string, ...string) ([]byte, error) {
	pods, deployment := operatorPodsJSON("strimzi-operator", "1.3.0", watches)
	read := adminlessClusterRead("")
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "get pods -A -l strimzi.io/kind=cluster-operator"):
			return []byte(pods), nil
		case strings.Contains(joined, "get deployment -n strimzi-operator"):
			return []byte(deployment), nil
		case strings.Contains(joined, "helm list -A"):
			return []byte(`[{"name":"strimzi-operator","namespace":"strimzi-operator","chart":"strimzi-operator-0.9.0","app_version":"1.3.0"}]`), nil
		}
		return read(ctx, name, args...)
	}
}

func TestCheckAdminlessStrimzi(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	setDeployFlags(t, func() {
		deployTopology, deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = "isolated", true, true, true
	})
	setAdminlessNamespaces(t)
	ns := resolveNamespaces()

	// Missing: the install names the operator's namespace and what it must watch.
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	err := checkAdminlessStrimzi(context.Background(), ns)
	if err == nil || !strings.Contains(err.Error(), "-n strimzi-operator") ||
		!strings.Contains(err.Error(), "watchNamespaces={team-connect,team-kafka,team-mm2}") {
		t.Errorf("want the install suggested in strimzi-operator, watching the namespaces, got %v", err)
	}
	// Helm's RoleBindings need them: --create-namespace covers only the release's.
	if !strings.Contains(err.Error(), "for n in team-connect team-kafka team-mm2; do kubectl create namespace") {
		t.Errorf("want the watched namespaces created before the install, got %v", err)
	}

	for _, watches := range []string{"*", "strimzi-operator,team-kafka,team-connect,team-mm2"} {
		runExecOutputFn = adminlessOperatorRead(watches)
		if err := checkAdminlessStrimzi(context.Background(), ns); err != nil {
			t.Errorf("operator watching %s: %v", watches, err)
		}
	}

	// Installed but not watching team-mm2: refused, with the watch list to set.
	runExecOutputFn = adminlessOperatorRead("strimzi-operator,team-kafka,team-connect")
	err = checkAdminlessStrimzi(context.Background(), ns)
	if err == nil || !strings.Contains(err.Error(), "does not watch team-mm2") ||
		!strings.Contains(err.Error(), "Strimzi 1.3.0 in namespace strimzi-operator") ||
		!strings.Contains(err.Error(), "watchNamespaces={team-connect,team-kafka,team-mm2}") {
		t.Errorf("want the unwatched namespace, the operator and the fix named, got %v", err)
	}
	if !strings.Contains(err.Error(), "for n in team-mm2; do kubectl create namespace") {
		t.Errorf("want the namespace to add created first, got %v", err)
	}

	// Watching the namespaces on paper, but crash-looping: an install that
	// failed before the namespaces existed left it without RoleBindings there.
	read := adminlessOperatorRead("*")
	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := read(ctx, name, args...)
		if strings.Contains(strings.Join(args, " "), "get deployment -n strimzi-operator") {
			out = []byte(strings.Replace(string(out), "{", `{"status":{"replicas":1},`, 1))
		}
		return out, err
	}
	err = checkAdminlessStrimzi(context.Background(), ns)
	if err == nil || !strings.Contains(err.Error(), "no available pod") ||
		!strings.Contains(err.Error(), "kubectl logs -n strimzi-operator deployment/") ||
		!strings.Contains(err.Error(), "helm upgrade strimzi-operator") {
		t.Errorf("want the down operator refused, with how to look and repair it, got %v", err)
	}

	// The account cannot list operator pods: the CRDs are enough to go on.
	runExecOutputFn = adminlessClusterRead("")
	if err := checkAdminlessStrimzi(context.Background(), ns); err != nil {
		t.Errorf("operator not visible: %v", err)
	}
}

func TestRunDeploy_AdminlessMonitoringIsTheScrapeOnly(t *testing.T) {
	origOutput, origCombined, origHelm, origExec := runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn
	origStdin, origDeployed := runExecStdinFn, isHelmReleaseDeployedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn = origOutput, origCombined, origHelm, origExec
		runExecStdinFn, isHelmReleaseDeployedFn = origStdin, origDeployed
		deployAdminlessScrape = false
	})
	setDeployFlags(t, func() {
		deployAdminless, deployDryRun, deployTopology = true, false, "isolated"
		deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = false, true, false
	})
	setAdminlessNamespaces(t)
	resetDeployFlagsChanged(t)
	deployAdminlessScrape = true // the wizard's monitoring choice
	t.Chdir(t.TempDir())

	runExecOutputFn, runExecCombinedFn = adminlessClusterRead(""), adminlessClusterRead("")
	var mu sync.Mutex
	installs := map[string]string{}
	runHelmFn = func(_ context.Context, args ...string) error {
		if len(args) > 2 && args[0] == "upgrade" {
			mu.Lock()
			installs[args[2]] = strings.Join(args, " ")
			mu.Unlock()
		}
		return nil
	}
	runExecFn = func(context.Context, string, ...string) error { return nil }
	runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }

	if err := runDeploy(deployCmd, nil); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if _, ok := installs["monitoring"]; ok {
		t.Error("the monitoring stack installed admin-less")
	}
	for release, want := range map[string]string{
		"krafter": "--set monitoring.podMonitor.enabled=true",
		"mm2":     "--set podMonitors.enabled=true",
		"kates":   "--set metrics.serviceMonitor.enabled=true",
	} {
		if !strings.Contains(installs[release], want) {
			t.Errorf("%s without %s: %s", release, want, installs[release])
		}
	}
	if strings.Contains(installs["kates"], "prometheus.url=") {
		t.Errorf("kates pointed at a Prometheus this deploy did not install: %s", installs["kates"])
	}
}

func TestAdminlessGrants_MonitoringWhereEachScrapeIsMade(t *testing.T) {
	setDeployFlags(t, func() {
		deployTopology, deployWithKafkaConnect, deployWithMirrorMaker2 = "isolated", true, true
	})
	t.Cleanup(func() { deployAdminlessScrape = false })
	deployAdminlessScrape = true
	setAdminlessNamespaces(t)
	grants := adminlessGrants(resolveNamespaces())

	for namespace, want := range map[string]string{
		"team-kafka": "podmonitors.monitoring.coreos.com", "team-connect": "prometheusrules.monitoring.coreos.com",
		"team-mm2": "podmonitors.monitoring.coreos.com", "team-kates": "servicemonitors.monitoring.coreos.com",
	} {
		if !slices.Contains(grants[namespace], want) {
			t.Errorf("%s: want %s in %v", namespace, want, grants[namespace])
		}
	}
	if slices.Contains(grants["team-db"], "podmonitors.monitoring.coreos.com") {
		t.Errorf("team-db asked for PodMonitors nothing makes there: %v", grants["team-db"])
	}
}

func TestAdminlessOperatorAdvice(t *testing.T) {
	kates := strimzi.Operator{Namespace: "strimzi-operator", Version: "1.1.0", Release: "strimzi-operator"}
	if got := adminlessOperatorAdvice(kates, "1.2.0", nil); !strings.Contains(got, "helm upgrade strimzi-operator oci://ghcr.io/bmscomp/charts/strimzi-operator -n strimzi-operator --reuse-values --set strimziVersion=1.2.0") {
		t.Errorf("an older kates operator: want the upgrade command, got %q", got)
	}
	foreign := strimzi.Operator{Namespace: "operators", Version: "1.1.0"}
	if got := adminlessOperatorAdvice(foreign, "1.2.0", []string{"team-kafka"}); !strings.Contains(got, "not installed by kates") || !strings.Contains(got, "watchNamespaces={team-kafka}") {
		t.Errorf("an older foreign operator: want the replacement install, got %q", got)
	}
	for version, want := range map[string]string{"1.2.0": "pins", "1.3.0": "Newer than"} {
		if got := adminlessOperatorAdvice(strimzi.Operator{Version: version}, "1.2.0", nil); !strings.Contains(got, want) || strings.Contains(got, "helm") {
			t.Errorf("%s: want %q and no command, got %q", version, want, got)
		}
	}
}

func TestPrintComponentSelection_AdminlessNamesWhatIsLeftOut(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig; deployAdminlessScrape = false })
	setDeployFlags(t, func() {
		deployAdminless, deployWithStrimzi, deployWithKafkaUI = true, false, true
		deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithKyverno = false, false, false, false
	})

	for _, tc := range []struct {
		name, apiResources string
		scrape             bool
		want               []string
	}{
		{"no Prometheus operator", "", false, []string{
			"Not installed: Strimzi Operator — its CRDs and cluster roles need cluster admin; Kafka runs on the operator a cluster admin installed",
			"Not installed: Cert-Manager, Kyverno — cluster-wide CRDs and admission webhooks need cluster admin",
			"Not installed: Litmus Chaos — its CRDs and cluster roles need cluster admin, so this Kates runs no chaos experiments",
			"Not installed: Monitoring (Prometheus, Grafana) — needs cluster admin, and the cluster has no Prometheus operator for this stack's PodMonitors",
		}},
		{"a Prometheus operator", "podmonitors.monitoring.coreos.com\nprometheusrules.monitoring.coreos.com", false, []string{
			"Not installed: Monitoring (Prometheus, Grafana) — needs cluster admin; the wizard's admin-less Monitoring option adds PodMonitors for the cluster's own Prometheus instead",
		}},
		{"the wizard's scrape chosen", "", true, []string{
			"✓ Monitoring (PodMonitors and rules for the cluster's Prometheus)",
			"Not installed: Monitoring (Prometheus, Grafana) — needs cluster admin; the cluster's own Prometheus scrapes this stack instead",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deployAdminlessScrape = tc.scrape
			runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "kubectl" && len(args) > 0 && args[0] == "api-resources" {
					return []byte(tc.apiResources), nil
				}
				return nil, errors.New("unexpected")
			}
			out := stripStyle(captureStdout(t, printComponentSelection))
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}
