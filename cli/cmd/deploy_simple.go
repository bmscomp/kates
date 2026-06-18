package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
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

	return nil
}

// runSimpleDeploy orchestrates a namespace-scoped deployment where all
// components (PostgreSQL, Kafka, Connect, connectors, Apicurio) are
// deployed into a single pre-existing namespace.
func runSimpleDeploy(cmd *cobra.Command, report *detect.DetectReport, namespace string, valuesFile string, isKind bool) error {
	deployStartTime := time.Now()

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrAccent).
		Render("⎈ Kates Simple Deploy (namespace-scoped)"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 45)))

	clusterDomain := report.Network.ClusterDomain
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}

	// ── Step count ──────────────────────────────────────────────────────
	totalSteps := 0
	totalSteps++ // postgresql
	totalSteps++ // kafka
	totalSteps++ // kafka-users
	totalSteps++ // kafka-connect
	totalSteps++ // kafka-connector
	if deployWithSchemaRegistry == "apicurio" {
		totalSteps++ // apicurio
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
	dashboard.RegisterComponent("kafka-connector", "CDC Connector", "B",
		Target{namespace, "strimzi.io/cluster=connect-cluster"})
	if deployWithSchemaRegistry == "apicurio" {
		dashboard.RegisterComponent("apicurio", "Apicurio Registry", "C",
			Target{namespace, "app.kubernetes.io/instance=apicurio"})
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

	// ── Deploy goroutine ───────────────────────────────────────────────
	var deployErr error
	var finalEntries []DeploySummaryEntry
	var deployElapsed time.Duration
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		defer p.Quit()

		deployErr = func() error {
			// ─────────────────────────────────────────────────────────
			// (a) PostgreSQL
			// ─────────────────────────────────────────────────────────
			deployPG := !isHelmReleaseDeployedFn(ctx, "postgresql", namespace)
			if deployPG {
				dl.Println("\n📦 Deploying PostgreSQL (Namespace: " + namespace + ")...")

				runHelmFn(ctx, "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami")
				runHelmFn(ctx, "repo", "update", "bitnami")

				if err := runHelmFn(ctx, "upgrade", "--install", "postgresql", "bitnami/postgresql",
					"-n", namespace,
					"--set", "auth.postgresPassword=postgres",
					"--set", "auth.username=debezium",
					"--set", "auth.password=debezium",
					"--set", "auth.database=orders",
					"--set", "primary.extendedConfiguration=wal_level = logical\nmax_wal_senders = 10\nmax_replication_slots = 10",
					"--timeout", "5m"); err != nil {
					return fmt.Errorf("PostgreSQL deploy failed: %w", err)
				}
			} else {
				dl.Println("⏭️  PostgreSQL already deployed. Skipping.")
				dl.FinishComponent("postgres", true)
				advanceStep()
			}

			// ─────────────────────────────────────────────────────────
			// (b) Kafka cluster
			// ─────────────────────────────────────────────────────────
			deployKafka := !isHelmReleaseDeployedFn(ctx, "krafter", namespace)
			if deployKafka {
				dl.Println("\n📦 Deploying Kafka Cluster (Namespace: " + namespace + ")...")

				runHelmFn(ctx, "dependency", "update", "charts/kafka-cluster")

				kafkaArgs := []string{"upgrade", "--install", "krafter", "charts/kafka-cluster",
					"-n", namespace,
					"-f", "charts/kafka-cluster/values-simple.yaml",
					"-f", valuesFile,
					"--set", "global.clusterDomain=" + clusterDomain,
					"--set", "networkPolicies.connectNamespace=" + namespace,
					"--timeout", "10m",
				}

				if deployHA {
					kafkaArgs = append(kafkaArgs,
						"--set", "kafka.replicas=3",
						"--set", "kafka.config.default\\.replication\\.factor=3",
						"--set", "kafka.config.min\\.insync\\.replicas=2",
						"--set", "zookeeper.replicas=3",
					)
				} else {
					kafkaArgs = append(kafkaArgs,
						"--set", "kafka.replicas=1",
						"--set", "kafka.config.default\\.replication\\.factor=1",
						"--set", "kafka.config.min\\.insync\\.replicas=1",
						"--set", "zookeeper.replicas=1",
					)
				}

				if err := runHelmFn(ctx, kafkaArgs...); err != nil {
					return fmt.Errorf("Kafka deploy failed: %w", err)
				}
			} else {
				dl.Println("⏭️  Kafka Cluster already deployed. Skipping.")
				dl.FinishComponent("kafka", true)
				advanceStep()
			}

			// ─────────────────────────────────────────────────────────
			// (c) Wait for Kafka readiness
			// ─────────────────────────────────────────────────────────
			if !isTesting {
				if deployKafka {
					dl.StartComponent("kafka", 15*time.Minute)
					if err := waitComponentReadySilent(ctx, namespace, "strimzi.io/cluster=krafter", 15*time.Minute); err != nil {
						return fmt.Errorf("kafka readiness failed: %w", err)
					}
					dl.FinishComponent("kafka", true)
					advanceStep()
				}

				// Wait for PostgreSQL
				if deployPG {
					dl.StartComponent("postgres", 5*time.Minute)
					if err := waitComponentReadySilent(ctx, namespace, "app.kubernetes.io/instance=postgresql", 5*time.Minute); err != nil {
						dl.FinishComponent("postgres", false)
						dl.Printf("    %s PostgreSQL not ready: %v\n", output.WarningStyle.Render("⚠"), err)
					} else {
						dl.FinishComponent("postgres", true)
						// Grant superuser and replication to debezium after DB is ready
						for i := 0; i < 5; i++ {
							err := exec.CommandContext(ctx, "kubectl", "exec", "-n", namespace, "postgresql-0", "--",
								"env", "PGPASSWORD=postgres", "psql", "-U", "postgres", "-c",
								"ALTER ROLE debezium SUPERUSER REPLICATION;").Run()
							if err == nil {
								break
							}
							time.Sleep(2 * time.Second)
						}
					}
					advanceStep()
				}
			}

			// ─────────────────────────────────────────────────────────
			// (d) Apply kafka users and topics
			// ─────────────────────────────────────────────────────────
			dl.Println("    - Applying Kafka users and topics...")
			applyManifestWithNamespace(ctx, "config/kafka/kafka-users.yaml", namespace)
			applyManifestWithNamespace(ctx, "config/kafka/kafka-topics.yaml", namespace)

			// ─────────────────────────────────────────────────────────
			// (e) Wait for Entity Operator and KafkaUsers
			// ─────────────────────────────────────────────────────────
			if !isTesting {
				dl.Println("    - Waiting for Entity Operator to start...")
				eoDeadline := time.Now().Add(5 * time.Minute)
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
						return ctx.Err()
					case <-time.After(5 * time.Second):
					}
				}

				dl.StartComponent("kafka-users", 8*time.Minute)
				err := waitKafkaUsersReadySilent(ctx, namespace, 8*time.Minute)
				if err != nil {
					dl.Printf("    %s KafkaUsers not all ready after 8m — downstream deploys will retry secret lookup\n", output.WarningStyle.Render("⚠"))
				}
			}
			dl.FinishComponent("kafka-users", true)
			advanceStep()

			// ─────────────────────────────────────────────────────────
			// (f) Create PG credentials secret
			// ─────────────────────────────────────────────────────────
			dl.Println("    - Creating PostgreSQL credentials secret for Kafka Connect...")
			pgSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: connect-pg-credentials
  namespace: %s
type: Opaque
stringData:
  password: debezium
  username: debezium`, namespace)
			runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, pgSecretYaml)

			// ─────────────────────────────────────────────────────────
			// (g) Kafka Connect
			// ─────────────────────────────────────────────────────────
			connectDeployed := isHelmReleaseDeployedFn(ctx, "connect-cluster", namespace)
			if connectDeployed && !isTesting {
				// Verify pods actually exist
				podCheck, _ := exec.CommandContext(ctx, "kubectl", "get", "pods",
					"-n", namespace, "-l", "strimzi.io/kind=KafkaConnect",
					"-o", "jsonpath={.items}").Output()
				if string(podCheck) == "[]" || len(strings.TrimSpace(string(podCheck))) == 0 {
					dl.Println("    ⚠️  Kafka Connect release exists but no pods found — upgrading...")
					connectDeployed = false
				}
			}
			if !connectDeployed {
				dl.Printf("\n📦 Deploying Kafka Connect (Namespace: %s)...\n", namespace)

				bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", namespace, clusterDomain)
				registryFQDN := fmt.Sprintf("http://apicurio-apicurio-registry.%s.svc.%s:80/apis/ccompat/v7",
					namespace, clusterDomain)

				connectArgs := []string{"upgrade", "--install", "connect-cluster", "charts/connect-cluster",
					"-n", namespace,
					"-f", "charts/connect-cluster/values-simple.yaml",
					"--set", "clusterDomain=" + clusterDomain,
					"--set", "kafka.namespace=" + namespace,
					"--set", "kafka.bootstrapServers=" + bootstrap,
					"--set", "schemaRegistry.enabled=true",
					"--set", "extraConfig.schema\\.registry\\.url=" + registryFQDN,
					"--set", "databaseEgress[0].namespace=" + namespace,
					"--set", "databaseEgress[0].port=5432",
					"--set", "databaseEgress[0].podSelector.app\\.kubernetes\\.io/name=postgresql",
					"--timeout", "10m",
				}

				// Enable NetworkPolicy on non-Kind clusters
				if !isKind {
					connectArgs = append(connectArgs, "--set", "networkPolicy.enabled=true")
				}

				if deployHA {
					connectArgs = append(connectArgs, "--set", "replicas=3")
				} else {
					connectArgs = append(connectArgs, "--set", "replicas=1")
				}

				// Always disable monitoring in simple mode
				connectArgs = append(connectArgs,
					"--set", "alerts.enabled=false",
					"--set", "podMonitors.enabled=false",
					"--set", "dashboards.enabled=false",
				)

				if err := runHelmFn(ctx, connectArgs...); err != nil {
					return fmt.Errorf("Kafka Connect deploy failed: %w", err)
				}
			} else {
				dl.Println("⏭️  Kafka Connect already deployed. Skipping.")
				dl.FinishComponent("kafka-connect", true)
				advanceStep()
			}

			// ─────────────────────────────────────────────────────────
			// (h) Wait for Kafka Connect readiness
			// ─────────────────────────────────────────────────────────
			if !isTesting {
				dl.StartComponent("kafka-connect", 15*time.Minute)
				if err := waitComponentReadySilent(ctx, namespace, "strimzi.io/kind=KafkaConnect", 15*time.Minute); err != nil {
					dl.FinishComponent("kafka-connect", false)
					return fmt.Errorf("Kafka Connect failed to become ready: %w", err)
				}
				dl.FinishComponent("kafka-connect", true)
				advanceStep()
			}

			// ─────────────────────────────────────────────────────────
			// (i) Deploy Debezium and JDBC Sink connectors
			// ─────────────────────────────────────────────────────────
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

			// ─────────────────────────────────────────────────────────
			// (j) Wait for connectors
			// ─────────────────────────────────────────────────────────
			if !isTesting {
				dl.StartComponent("kafka-connector", 10*time.Minute)
				if err := waitConnectorReadySilent(ctx, namespace, 10*time.Minute); err != nil {
					dl.FinishComponent("kafka-connector", false)
					return fmt.Errorf("Kafka Connectors failed to become ready: %w", err)
				}
				dl.FinishComponent("kafka-connector", true)
			}
			advanceStep()

			// ─────────────────────────────────────────────────────────
			// (k) Apicurio Registry (optional)
			// ─────────────────────────────────────────────────────────
			if deployWithSchemaRegistry == "apicurio" {
				if !isHelmReleaseDeployedFn(ctx, "apicurio", namespace) {
					dl.Printf("\n📦 Deploying Apicurio Schema Registry (Namespace: %s)...\n", namespace)
					dl.StartComponent("apicurio", 5*time.Minute)
					if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", "charts/apicurio-registry",
						"-n", namespace,
						"--timeout", "5m"); err != nil {
						dl.FinishComponent("apicurio", false)
						return fmt.Errorf("Apicurio deploy failed: %w", err)
					}
					dl.FinishComponent("apicurio", true)
					advanceStep()
				} else {
					dl.Println("⏭️  Apicurio already deployed.")
					dl.FinishComponent("apicurio", true)
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
