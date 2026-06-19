package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

// Simple deploy configuration flags (registered in deploy.go init())
var (
	deploySimplePgUser         string
	deploySimplePgPassword     string
	deploySimpleUpgrade        bool
	deploySimpleWithConnectors bool
	deploySimpleWithBackend    bool
	deploySimpleDev            bool
)

// validateSimplePrerequisites runs pre-flight checks for simple deploy mode.
// It verifies: namespace exists, Strimzi CRDs installed, Strimzi operator
// running (warn-only), and helm binary available.
func validateSimplePrerequisites(ctx context.Context, namespace string) error {
	// 1. Check namespace exists
	if err := exec.CommandContext(ctx, "kubectl", "get", "namespace", namespace).Run(); err != nil {
		return fmt.Errorf(
			"namespace %q does not exist.\n\n"+
				"  Simple deploy mode requires a pre-existing namespace.\n"+
				"  Create it first:\n\n"+
				"    kubectl create namespace %s\n",
			namespace, namespace)
	}

	// 2. Check Strimzi CRDs exist
	if err := exec.CommandContext(ctx, "kubectl", "get", "crd", "kafkas.kafka.strimzi.io").Run(); err != nil {
		return fmt.Errorf(
			"Strimzi CRDs not found.\n\n"+
				"  Simple deploy mode requires the Strimzi operator to be pre-installed.\n"+
				"  Install it with:\n\n"+
				"    helm repo add strimzi https://strimzi.io/charts/\n"+
				"    helm install strimzi-operator strimzi/strimzi-kafka-operator -n strimzi-operator --create-namespace --set watchAnyNamespace=true\n")
	}

	// 3. Check Strimzi operator is running (warn only — labels may differ)
	out, err := exec.CommandContext(ctx, "kubectl", "get", "pods",
		"-l", "name=strimzi-cluster-operator",
		"--all-namespaces", "--no-headers").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		fmt.Fprintf(os.Stderr, "⚠️  Could not find Strimzi operator pods (label: name=strimzi-cluster-operator).\n"+
			"   The operator may be running with different labels. Continuing...\n\n")
	}

	// 4. Check helm is available
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf(
			"helm not found on PATH.\n\n"+
				"  Simple deploy mode requires Helm. Install it from:\n"+
				"    https://helm.sh/docs/intro/install/\n")
	}

	// 5. Check user has sufficient RBAC in the namespace
	rbacChecks := []struct{ resource, verb string }{
		{"deployments", "create"},
		{"services", "create"},
		{"secrets", "create"},
		{"configmaps", "create"},
	}
	for _, check := range rbacChecks {
		out, err := exec.CommandContext(ctx, "kubectl", "auth", "can-i", check.verb, check.resource, "-n", namespace).Output()
		if err != nil || strings.TrimSpace(string(out)) != "yes" {
			return fmt.Errorf(
				"insufficient permissions: cannot %s %s in namespace %q.\n\n"+
					"  Simple deploy requires 'edit' role in the target namespace.\n"+
					"  Ask your cluster admin to grant it:\n\n"+
					"    kubectl create rolebinding <name> --clusterrole=edit --user=<you> -n %s\n",
				check.verb, check.resource, namespace, namespace)
		}
	}

	// 6. Check Strimzi CRD version compatibility
	versionOut, vErr := exec.CommandContext(ctx, "kubectl", "get", "crd", "kafkas.kafka.strimzi.io",
		"-o", "jsonpath={.spec.versions[*].name}").Output()
	if vErr == nil {
		versions := strings.Fields(strings.TrimSpace(string(versionOut)))
		hasV1 := false
		for _, v := range versions {
			if v == "v1beta2" || v == "v1" {
				hasV1 = true
				break
			}
		}
		if !hasV1 {
			return fmt.Errorf(
				"Strimzi CRD version incompatible.\n\n"+
					"  Kates requires Strimzi CRDs with v1beta2 or v1 API version.\n"+
					"  Found versions: %s\n"+
					"  Please upgrade your Strimzi operator.\n",
				strings.Join(versions, ", "))
		}
	}

	return nil
}

// runSimpleDeploy orchestrates a namespace-scoped deployment where all
// components (PostgreSQL, Kafka, Connect, connectors, Apicurio) are
// deployed into a single pre-existing namespace.
func runSimpleDeploy(cmd *cobra.Command, namespace string) error {
	deployStartTime := time.Now()

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrAccent).
		Render("⎈ Kates Simple Deploy (namespace-scoped)"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 45)))

	// ── Detect cluster domain from DNS (no admin required) ─────────
	clusterDomain := "cluster.local"
	if out, err := exec.CommandContext(context.Background(), "kubectl", "exec", "-n", namespace,
		"--", "cat", "/etc/resolv.conf").Output(); err == nil {
		// This will fail if no pods exist yet, that's fine — use default
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "search") && strings.Contains(line, "svc.") {
				for _, field := range strings.Fields(line) {
					if strings.HasPrefix(field, "svc.") {
						clusterDomain = strings.TrimPrefix(field, "svc.")
						break
					}
				}
			}
		}
	}

	if deploySimpleDev {
		fmt.Println(lipgloss.NewStyle().Foreground(clrAccent).
			Render("🧪 Dev mode: minimal resources (1 broker, 1 controller)"))
	}

	// ── Step count ──────────────────────────────────────────────────────
	totalSteps := 0
	totalSteps++ // postgresql
	totalSteps++ // kafka
	totalSteps++ // kafka-users
	totalSteps++ // kafka-connect
	if deploySimpleWithConnectors {
		totalSteps++ // kafka-connector
	}
	if deployWithSchemaRegistry == "apicurio" {
		totalSteps++ // apicurio
	}
	if deploySimpleWithBackend {
		totalSteps++ // kates
	}

	// ── Dashboard setup ────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dashboard := NewDeployDashboard(ctx, totalSteps)
	dashboard.RegisterComponent("postgres", "PostgreSQL", "A",
		Target{namespace, "app.kubernetes.io/instance=postgresql"})
	dashboard.RegisterComponent("kafka", "Kafka Cluster", "A",
		Target{namespace, "strimzi.io/cluster=krafter"})
	dashboard.RegisterComponent("kafka-users", "Kafka Users", "A",
		Target{namespace, "app.kubernetes.io/name=entity-operator"})
	dashboard.RegisterComponent("kafka-connect", "Kafka Connect", "B",
		Target{namespace, "strimzi.io/kind=KafkaConnect"})
	if deploySimpleWithConnectors {
		dashboard.RegisterComponent("kafka-connector", "CDC Connector", "B",
			Target{namespace, "strimzi.io/cluster=connect-cluster"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		dashboard.RegisterComponent("apicurio", "Apicurio Registry", "C",
			Target{namespace, "app.kubernetes.io/instance=apicurio"})
	}
	if deploySimpleWithBackend {
		dashboard.RegisterComponent("kates", "Kates Backend", "D",
			Target{namespace, "app.kubernetes.io/instance=kates"})
	}

	var pOptions []tea.ProgramOption
	if isTesting {
		pOptions = append(pOptions, tea.WithoutRenderer(), tea.WithInput(nil))
	} else {
		pOptions = append(pOptions, tea.WithAltScreen())
	}
	p := tea.NewProgram(dashboard, pOptions...)
	dl = &DashboardController{p: p}

	var step int32
	advanceStep := func() {
		current := atomic.AddInt32(&step, 1)
		dl.UpdateProgress(int(current), totalSteps)
	}

	// ── Shared entries for summary ─────────────────────────────────────
	var sharedEntries []DeploySummaryEntry
	sharedEntries = append(sharedEntries,
		DeploySummaryEntry{Icon: "🐘", Name: "PostgreSQL (CDC)", Release: "postgresql", Namespace: namespace, Group: "A"},
		DeploySummaryEntry{Icon: "📨", Name: "Kafka (krafter)", Release: "krafter", Namespace: namespace, Group: "A"},
		DeploySummaryEntry{Icon: "🔗", Name: "Kafka Connect", Release: "connect-cluster", Namespace: namespace, Group: "B"},
	)
	if deployWithSchemaRegistry == "apicurio" {
		sharedEntries = append(sharedEntries,
			DeploySummaryEntry{Icon: "📋", Name: "Apicurio Registry", Release: "apicurio", Namespace: namespace, Group: "C"})
	}
	if deploySimpleWithBackend {
		sharedEntries = append(sharedEntries,
			DeploySummaryEntry{Icon: "🚀", Name: "Kates Backend", Release: "kates", Namespace: namespace, Group: "D"})
	}

	// ── Dry-run: show preview and exit ──
	if deployDryRun {
		dryCtx, dryCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dryCancel()
		existingReleases := make(map[string]bool)
		for _, e := range sharedEntries {
			key := e.Release + "/" + e.Namespace
			if isHelmReleaseDeployedFn(dryCtx, e.Release, e.Namespace) {
				existingReleases[key] = true
			}
		}
		renderDeployPreview(sharedEntries, existingReleases)
		return nil
	}

	// ── Deploy goroutine ───────────────────────────────────────────────
	var deployErr error
	var finalEntries []DeploySummaryEntry
	var deployElapsed time.Duration
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		defer p.Quit()

		deployErr = func() error {
			// ── Phase 1: Deploy PG + Kafka + Apicurio in parallel ──
			var pgErr, kafkaErr, apicurioErr error
			deployPG := !isHelmReleaseDeployedFn(ctx, "postgresql", namespace) || deploySimpleUpgrade
			deployKafka := !isSimpleComponentDeployed(ctx, namespace, "kafka", "krafter") || deploySimpleUpgrade

			var phase1WG sync.WaitGroup

			// ── goroutine 1: PostgreSQL ──
			phase1WG.Add(1)
			go func() {
				defer phase1WG.Done()
				if deployPG {
					dl.Println("\n📦 Deploying PostgreSQL (Namespace: " + namespace + ")...")

					runHelmFn(ctx, "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami")
					runHelmFn(ctx, "repo", "update", "bitnami")

					pgArgs := []string{
						"upgrade", "--install", "postgresql", "bitnami/postgresql",
						"-n", namespace,
						"--set", "auth.postgresPassword=postgres",
						"--set", "auth.username=" + deploySimplePgUser,
						"--set", "auth.password=" + deploySimplePgPassword,
						"--set", "auth.database=orders",
						"--set", "primary.extendedConfiguration=wal_level = logical\nmax_wal_senders = 10\nmax_replication_slots = 10",
						"--timeout", "5m",
					}
					if deploySimpleDev {
						pgArgs = append(pgArgs,
							"--set", "primary.resources.requests.memory=128Mi",
							"--set", "primary.resources.requests.cpu=100m",
							"--set", "primary.resources.limits.memory=256Mi",
							"--set", "primary.resources.limits.cpu=250m",
							"--set", "primary.persistence.size=1Gi",
						)
					}
					pgErr = runHelmFn(ctx, pgArgs...)
				} else {
					dl.Println("⏭️  PostgreSQL already deployed. Skipping.")
					dl.FinishComponent("postgres", true)
					advanceStep()
				}
			}()

			// ── goroutine 2: Kafka CR + NodePools ──
			phase1WG.Add(1)
			go func() {
				defer phase1WG.Done()
				if deployKafka {
					dl.Println("\n📦 Deploying Kafka Cluster (Namespace: " + namespace + ")...")

					minISR := 1
					replicationFactor := 1
					if deployHA {
						minISR = 2
						replicationFactor = 3
					}

					kafkaCR := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: Kafka
metadata:
  name: krafter
  namespace: %s
  annotations:
    strimzi.io/node-pools: enabled
    strimzi.io/kraft: enabled
spec:
  kafka:
    version: 4.2.0
    listeners:
      - name: plain
        port: 9092
        type: internal
        tls: false
        authentication:
          type: scram-sha-512
      - name: tls
        port: 9093
        type: internal
        tls: true
        authentication:
          type: tls
    authorization:
      type: simple
      superUsers:
        - kates-backend
    config:
      auto.create.topics.enable: false
      default.replication.factor: %d
      min.insync.replicas: %d
      offsets.topic.replication.factor: %d
      transaction.state.log.replication.factor: %d
      transaction.state.log.min.isr: %d
      log.retention.hours: 24
      log.cleanup.policy: delete
      log.segment.bytes: 1073741824
      message.max.bytes: 10485760
      unclean.leader.election.enable: false
      group.share.enable: true
    template:
      pod:
        terminationGracePeriodSeconds: 45
  entityOperator:
    topicOperator:
      reconciliationIntervalMs: 10000
    userOperator:
      reconciliationIntervalMs: 10000`, namespace, replicationFactor, minISR, replicationFactor, replicationFactor, minISR)

					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, kafkaCR); err != nil {
						kafkaErr = fmt.Errorf("Kafka deploy failed: %w", err)
						return
					}

					// Apply KafkaNodePool CRs
					controllerReplicas := 1
					controllerMemReq := "1Gi"
					controllerMemLim := "1Gi"
					controllerCPUReq := "500m"
					controllerCPULim := "1000m"
					controllerJvmXms := "512m"
					controllerJvmXmx := "512m"
					controllerStorage := "5Gi"

					if deploySimpleDev {
						controllerMemReq = "512Mi"
						controllerMemLim = "512Mi"
						controllerCPUReq = "100m"
						controllerCPULim = "500m"
						controllerJvmXms = "256m"
						controllerJvmXmx = "256m"
						controllerStorage = "1Gi"
					}
					if deployHA {
						controllerReplicas = 3
					}

					controllerPoolCR := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaNodePool
metadata:
  name: controllers
  namespace: %s
  labels:
    strimzi.io/cluster: krafter
spec:
  replicas: %d
  roles:
    - controller
  storage:
    type: jbod
    volumes:
      - id: 0
        type: persistent-claim
        size: %s
        deleteClaim: true
  resources:
    requests:
      memory: %s
      cpu: %s
    limits:
      memory: %s
      cpu: %s
  jvmOptions:
    -Xms: %s
    -Xmx: %s`, namespace, controllerReplicas, controllerStorage,
						controllerMemReq, controllerCPUReq, controllerMemLim, controllerCPULim,
						controllerJvmXms, controllerJvmXmx)

					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, controllerPoolCR); err != nil {
						kafkaErr = fmt.Errorf("KafkaNodePool controllers deploy failed: %w", err)
						return
					}

					// Broker NodePool
					brokerReplicas := 1
					brokerMemReq := "2Gi"
					brokerMemLim := "2Gi"
					brokerCPUReq := "500m"
					brokerCPULim := "1000m"
					brokerJvmXms := "1024m"
					brokerJvmXmx := "1024m"
					brokerStorage := "10Gi"

					if deploySimpleDev {
						brokerMemReq = "1Gi"
						brokerMemLim = "1Gi"
						brokerCPUReq = "250m"
						brokerCPULim = "500m"
						brokerJvmXms = "512m"
						brokerJvmXmx = "512m"
						brokerStorage = "5Gi"
					}
					if deployHA {
						brokerReplicas = 3
					}

					brokerPoolCR := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaNodePool
metadata:
  name: brokers
  namespace: %s
  labels:
    strimzi.io/cluster: krafter
spec:
  replicas: %d
  roles:
    - broker
  storage:
    type: jbod
    volumes:
      - id: 0
        type: persistent-claim
        size: %s
        deleteClaim: true
  resources:
    requests:
      memory: %s
      cpu: %s
    limits:
      memory: %s
      cpu: %s
  jvmOptions:
    -Xms: %s
    -Xmx: %s`, namespace, brokerReplicas, brokerStorage,
						brokerMemReq, brokerCPUReq, brokerMemLim, brokerCPULim,
						brokerJvmXms, brokerJvmXmx)

					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, brokerPoolCR); err != nil {
						kafkaErr = fmt.Errorf("KafkaNodePool brokers deploy failed: %w", err)
						return
					}

					dl.Printf("    %s KafkaNodePools applied (controllers: %d, brokers: %d)\n",
						output.SuccessStyle.Render("✔"), controllerReplicas, brokerReplicas)
				} else {
					dl.Println("⏭️  Kafka Cluster already deployed. Skipping.")
					dl.FinishComponent("kafka", true)
					advanceStep()
				}
			}()

			// ── goroutine 3: Apicurio (if schema registry enabled) ──
			if deployWithSchemaRegistry == "apicurio" {
				phase1WG.Add(1)
				go func() {
					defer phase1WG.Done()
					deployApicurio := !isHelmReleaseDeployedFn(ctx, "apicurio", namespace) || deploySimpleUpgrade
					if deployApicurio {
						dl.Printf("\n📦 Deploying Apicurio Schema Registry (Namespace: %s)...\n", namespace)
						dl.StartComponent("apicurio", 5*time.Minute)
						if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", "charts/apicurio-registry",
							"-n", namespace,
							"--timeout", "5m"); err != nil {
							dl.FinishComponent("apicurio", false)
							apicurioErr = fmt.Errorf("Apicurio deploy failed: %w", err)
							return
						}
						dl.FinishComponent("apicurio", true)
						advanceStep()
					} else {
						dl.Println("⏭️  Apicurio already deployed.")
						dl.FinishComponent("apicurio", true)
						advanceStep()
					}
				}()
			}

			phase1WG.Wait()

			if pgErr != nil {
				return fmt.Errorf("PostgreSQL deploy failed: %w", pgErr)
			}
			if kafkaErr != nil {
				return kafkaErr
			}
			if apicurioErr != nil {
				return apicurioErr
			}

			// ── Phase 2: Wait for Kafka + PG readiness in parallel ──
			if !isTesting {
				var kafkaWaitErr, pgWaitErr error
				var phase2WG sync.WaitGroup

				// Kafka wait + users/topics goroutine
				phase2WG.Add(1)
				go func() {
					defer phase2WG.Done()
					if deployKafka {
						dl.StartComponent("kafka", 10*time.Minute)
						if err := waitComponentReadySilent(ctx, namespace, "strimzi.io/cluster=krafter", 10*time.Minute); err != nil {
							kafkaWaitErr = fmt.Errorf("kafka readiness failed: %w", err)
							return
						}
						dl.FinishComponent("kafka", true)
						advanceStep()
					}

					// Apply users/topics (needs Kafka ready)
					dl.Println("    - Applying Kafka users and topics...")
					applyManifestWithNamespace(ctx, "config/kafka/kafka-users.yaml", namespace)
					applyManifestWithNamespace(ctx, "config/kafka/kafka-topics.yaml", namespace)

					// Wait for Entity Operator (reduced to 3min)
					dl.Println("    - Waiting for Entity Operator to start...")
					eoDeadline := time.Now().Add(3 * time.Minute)
					for time.Now().Before(eoDeadline) {
						eoOut, _ := exec.CommandContext(ctx,
							"kubectl", "get", "pods", "-n", namespace,
							"-l", "app.kubernetes.io/name=entity-operator",
							"--no-headers",
							"-o", "custom-columns=PHASE:.status.phase",
						).Output()
						if strings.Contains(string(eoOut), "Running") {
							dl.Printf("    %s Entity Operator running\n", output.AccentStyle.Render("✔"))
							break
						}
						select {
						case <-ctx.Done():
							kafkaWaitErr = ctx.Err()
							return
						case <-time.After(5 * time.Second):
						}
					}

					// Wait for KafkaUsers (reduced to 3min)
					dl.StartComponent("kafka-users", 3*time.Minute)
					err := waitKafkaUsersReadySilent(ctx, namespace, 3*time.Minute)
					if err != nil {
						dl.Printf("    %s KafkaUsers not all ready — downstream deploys will retry\n", output.WarningStyle.Render("⚠"))
					}
					dl.FinishComponent("kafka-users", true)
					advanceStep()
				}()

				// PostgreSQL wait goroutine
				phase2WG.Add(1)
				go func() {
					defer phase2WG.Done()
					if deployPG {
						dl.StartComponent("postgres", 5*time.Minute)
						if err := waitComponentReadySilent(ctx, namespace, "app.kubernetes.io/instance=postgresql", 5*time.Minute); err != nil {
							dl.FinishComponent("postgres", false)
							pgWaitErr = fmt.Errorf("PostgreSQL not ready: %w", err)
							return
						}
						dl.FinishComponent("postgres", true)
						// Grant superuser and replication to debezium after DB is ready
						for i := 0; i < 5; i++ {
							err := exec.CommandContext(ctx, "kubectl", "exec", "-n", namespace, "postgresql-0", "--",
								"env", "PGPASSWORD=postgres", "psql", "-U", "postgres", "-c",
								"ALTER ROLE "+deploySimplePgUser+" SUPERUSER REPLICATION;").Run()
							if err == nil {
								break
							}
							time.Sleep(2 * time.Second)
						}
						advanceStep()
					}
				}()

				phase2WG.Wait()

				if kafkaWaitErr != nil {
					return kafkaWaitErr
				}
				// PG wait is non-fatal (just warn)
				if pgWaitErr != nil {
					dl.Printf("    %s %v\n", output.WarningStyle.Render("⚠"), pgWaitErr)
				}
			} else {
				// In test mode, skip waits but still apply users/topics
				dl.Println("    - Applying Kafka users and topics...")
				applyManifestWithNamespace(ctx, "config/kafka/kafka-users.yaml", namespace)
				applyManifestWithNamespace(ctx, "config/kafka/kafka-topics.yaml", namespace)
				dl.FinishComponent("kafka-users", true)
				advanceStep()
			}

			// ── Phase 3: PG secret + Connect (needs both Kafka + PG) ──

			// Create PG credentials secret
			dl.Println("    - Creating PostgreSQL credentials secret for Kafka Connect...")
			pgSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: connect-pg-credentials
  namespace: %s
type: Opaque
stringData:
  password: %s
  username: %s`, namespace, deploySimplePgPassword, deploySimplePgUser)
			runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, pgSecretYaml)

			// Kafka Connect CR deploy
			connectDeployed := isSimpleComponentDeployed(ctx, namespace, "kafkaconnect", "connect-cluster")
			if connectDeployed && !deploySimpleUpgrade {
				if !isTesting {
					podCheck, _ := exec.CommandContext(ctx, "kubectl", "get", "pods",
						"-n", namespace, "-l", "strimzi.io/kind=KafkaConnect",
						"-o", "jsonpath={.items}").Output()
					if string(podCheck) == "[]" || len(strings.TrimSpace(string(podCheck))) == 0 {
						dl.Println("    ⚠️  Kafka Connect CR exists but no pods — re-applying...")
						connectDeployed = false
					}
				}
			}
			if !connectDeployed || deploySimpleUpgrade {
				dl.Printf("\n📦 Deploying Kafka Connect (Namespace: %s)...\n", namespace)

				bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", namespace, clusterDomain)
				connectReplicas := 1
				if deployHA {
					connectReplicas = 3
				}

				registryURL := ""
				if deployWithSchemaRegistry == "apicurio" {
					registryURL = fmt.Sprintf("http://apicurio-apicurio-registry.%s.svc.%s:80/apis/ccompat/v7", namespace, clusterDomain)
				}

				extraConfig := ""
				if registryURL != "" {
					extraConfig = fmt.Sprintf(`
      key.converter: io.apicurio.registry.utils.converter.AvroConverter
      key.converter.apicurio.registry.url: %s
      key.converter.apicurio.registry.auto-register: "true"
      value.converter: io.apicurio.registry.utils.converter.AvroConverter
      value.converter.apicurio.registry.url: %s
      value.converter.apicurio.registry.auto-register: "true"
      schema.registry.url: %s`, registryURL, registryURL, registryURL)
				}

				connectResources := ""
				if deploySimpleDev {
					connectResources = `
  resources:
    requests:
      memory: 512Mi
      cpu: 250m
    limits:
      memory: 1Gi
      cpu: 500m
  jvmOptions:
    -Xms: 256m
    -Xmx: 512m`
				}

				connectCR := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnect
metadata:
  name: connect-cluster
  namespace: %s
  annotations:
    strimzi.io/use-connector-resources: "true"
spec:
  version: 4.2.0
  replicas: %d
  bootstrapServers: %s
  authentication:
    type: scram-sha-512
    username: kates-connect
    passwordSecret:
      secretName: kates-connect
      password: password
  config:
    group.id: kates-connect
    offset.storage.topic: connect-offsets
    config.storage.topic: connect-configs
    status.storage.topic: connect-status
    offset.storage.replication.factor: 1
    config.storage.replication.factor: 1
    status.storage.replication.factor: 1
    config.providers: secrets
    config.providers.secrets.class: io.strimzi.kafka.KubernetesSecretConfigProvider%s
  build:
    output:
      type: docker
      image: localhost:5001/kates-connect:latest
      pushSecret: ""
    plugins:
      - name: debezium-postgres
        artifacts:
          - type: maven
            group: io.debezium
            artifact: debezium-connector-postgres
            version: 3.1.1.Final
      - name: debezium-jdbc
        artifacts:
          - type: maven
            group: io.debezium
            artifact: debezium-connector-jdbc
            version: 3.1.1.Final%s`, namespace, connectReplicas, bootstrap, extraConfig, connectResources)

				if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectCR); err != nil {
					return fmt.Errorf("Kafka Connect deploy failed: %w", err)
				}
			} else {
				dl.Println("⏭️  Kafka Connect already deployed. Skipping.")
				dl.FinishComponent("kafka-connect", true)
				advanceStep()
			}

			// Wait for Kafka Connect readiness (reduced to 10min)
			if !isTesting {
				dl.StartComponent("kafka-connect", 10*time.Minute)
				if err := waitComponentReadySilent(ctx, namespace, "strimzi.io/kind=KafkaConnect", 10*time.Minute); err != nil {
					dl.FinishComponent("kafka-connect", false)
					return fmt.Errorf("Kafka Connect failed to become ready: %w", err)
				}
				dl.FinishComponent("kafka-connect", true)
				advanceStep()
			}

			// Deploy connectors (if enabled)
			if deploySimpleWithConnectors {
				bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", namespace, clusterDomain)

				// Debezium connector
				checkOut, checkErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "debezium-postgres-source", "-n", namespace, "--no-headers").CombinedOutput()
				if checkErr != nil || strings.Contains(string(checkOut), "not found") {
					dl.Println("    - Deploying Debezium PostgreSQL CDC connector...")
					connectorYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: debezium-postgres-source
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.postgresql.PostgresConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    database.hostname: postgresql.%s.svc
    database.port: "5432"
    database.user: debezium
    database.password: "${secrets:%s/connect-pg-credentials:password}"
    database.dbname: orders
    topic.prefix: cdc
    schema.include.list: public
    plugin.name: pgoutput
    slot.name: debezium_kates
    heartbeat.interval.ms: "10000"
    snapshot.mode: initial
    decimal.handling.mode: double
    tombstones.on.delete: "true"
    schema.history.internal.kafka.bootstrap.servers: %s
    schema.history.internal.kafka.security.protocol: SASL_PLAINTEXT
    schema.history.internal.kafka.sasl.mechanism: SCRAM-SHA-512
    schema.history.internal.kafka.sasl.jaas.config: "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-connect\" password=\"${secrets:%s/kates-connect:password}\";"
    schema.history.internal.kafka.topic: cdc-schema-history`, namespace, namespace, namespace, bootstrap, namespace)
					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectorYaml); err != nil {
						dl.Printf("    %s Failed to deploy Debezium connector: %v\n", output.WarningStyle.Render("⚠"), err)
						dl.FinishComponent("kafka-connector", false)
					} else {
						dl.Printf("    %s Debezium PostgreSQL CDC connector deployed\n", output.SuccessStyle.Render("✔"))
					}
				} else {
					dl.Printf("    %s Debezium connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
				}

				// JDBC Sink connector
				sinkCheckOut, sinkCheckErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "jdbc-sink-connector", "-n", namespace, "--no-headers").CombinedOutput()
				if sinkCheckErr != nil || strings.Contains(string(sinkCheckOut), "not found") {
					dl.Println("    - Deploying JDBC Sink connector...")
					sinkYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: jdbc-sink-connector
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.jdbc.JdbcSinkConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    topics: "test-sink-topic"
    connection.url: "jdbc:postgresql://postgresql.%s.svc:5432/orders"
    connection.username: "${secrets:%s/connect-pg-credentials:username}"
    connection.password: "${secrets:%s/connect-pg-credentials:password}"
    insert.mode: "insert"
    auto.create: "true"
    auto.evolve: "true"`, namespace, namespace, namespace, namespace)
					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, sinkYaml); err != nil {
						dl.Printf("    %s Failed to deploy JDBC Sink connector: %v\n", output.WarningStyle.Render("⚠"), err)
					} else {
						dl.Printf("    %s JDBC Sink connector deployed\n", output.SuccessStyle.Render("✔"))
					}
				} else {
					dl.Printf("    %s JDBC Sink connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
				}

				// Wait for connectors
				if !isTesting {
					dl.StartComponent("kafka-connector", 10*time.Minute)
					if err := waitConnectorReadySilent(ctx, namespace, 10*time.Minute); err != nil {
						dl.FinishComponent("kafka-connector", false)
						return fmt.Errorf("Kafka Connectors failed to become ready: %w", err)
					}
					dl.FinishComponent("kafka-connector", true)
				}
				advanceStep()
			}

			// ── Phase 4: Kates Backend (if enabled) ──
			if deploySimpleWithBackend {
				deployBackend := !isHelmReleaseDeployedFn(ctx, "kates", namespace) || deploySimpleUpgrade
				if deployBackend {
					dl.Printf("\n📦 Deploying Kates Backend (Namespace: %s)...\n", namespace)
					dl.StartComponent("kates", 8*time.Minute)

					bootstrapKates := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", namespace, clusterDomain)
					if err := runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates",
						"-n", namespace,
						"-f", "charts/kates/values-simple.yaml",
						"--set", "kafka.bootstrapServers="+bootstrapKates,
						"--set", "monitoring.enabled=false",
						"--timeout", "8m"); err != nil {
						dl.FinishComponent("kates", false)
						return fmt.Errorf("Kates Backend deploy failed: %w", err)
					}

					if !isTesting {
						if err := waitComponentReadySilent(ctx, namespace, "app.kubernetes.io/instance=kates", 8*time.Minute); err != nil {
							dl.FinishComponent("kates", false)
							return fmt.Errorf("Kates Backend failed to become ready: %w", err)
						}
					}
					dl.FinishComponent("kates", true)
					advanceStep()
				} else {
					dl.Println("⏭️  Kates Backend already deployed. Skipping.")
					dl.FinishComponent("kates", true)
					advanceStep()
				}
			}

			// ── Summary ────────────────────────────────────────────
			finalEntries = sharedEntries
			deployElapsed = time.Since(deployStartTime)
			return nil
		}()
	}()

	if _, err := p.Run(); err != nil {
		return err
	}

	<-doneCh // Wait for deployment goroutine to fully exit

	if deployErr == nil && len(finalEntries) > 0 {
		RenderDeployDashboard(ctx, finalEntries, deployElapsed)

		if deployPortForward {
			RunPortForwards(ctx, namespace, namespace, namespace)
		}
	}

	return deployErr
}

// isSimpleComponentDeployed checks if a Strimzi CR or Helm release exists in the namespace.
func isSimpleComponentDeployed(ctx context.Context, namespace, kind, name string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := exec.CommandContext(checkCtx, "kubectl", "get", kind, name, "-n", namespace).Run()
	return err == nil
}
