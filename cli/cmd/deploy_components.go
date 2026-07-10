package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/klster/kates-cli/output"
	"golang.org/x/sync/errgroup"
)

// deployGroupA deploys Group A components (Operators & CRDs) in parallel:
// Strimzi, Cert-Manager, and Kyverno.
func deployGroupA(dc *deployContext) error {
	ctx := dc.ctx

	// ---------------------------------------------------------
	// GROUP A: Operators & CRDs (Parallel)
	// ---------------------------------------------------------
	deployStrimzi := false
	if deployWithStrimzi {
		deployStrimzi = !isHelmReleaseDeployedFn(ctx, "strimzi-operator", "strimzi-operator")
	}
	deployCertMgr := false
	if deployWithCertManager {
		deployCertMgr = !isHelmReleaseDeployedFn(ctx, "cert-manager", "cert-manager")
	}
	deployKyvernoFlag := false
	if deployWithKyverno {
		deployKyvernoFlag = !isHelmReleaseDeployedFn(ctx, "kyverno", "kyverno")
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Deploy Strimzi Operator
	if deployWithStrimzi {
		g.Go(func() error {
			if !deployStrimzi {
				dl.Println("⏭️  Strimzi Operator already deployed. Skipping.")
				dl.FinishComponent("strimzi", true)
				dc.advanceStep()
				return nil
			}
			dl.Println("\n📦 Deploying Strimzi Operator (Namespace: strimzi-operator)...")
			// Create namespace properly
			runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, `apiVersion: v1
kind: Namespace
metadata:
  name: strimzi-operator`)
			clusterDomain := dc.resolveClusterDomain()
			err := runHelmFn(gCtx, "upgrade", "--install", "strimzi-operator", "oci://quay.io/strimzi-helm/strimzi-kafka-operator", "--version", "1.0.0", "-n", "strimzi-operator",
				"--set", "watchAnyNamespace=true",
				"--set", "kubernetesServiceDnsDomain="+clusterDomain,
				"--set", "replicas=1",
				"--set", "resources.limits.memory=768Mi",
				"--set", "resources.requests.memory=768Mi",
				"--set", "leaderElection.enabled=false",
				"--set", "operationTimeoutMs=900000",
				"--timeout", "5m")
			if err != nil {
				return err
			}
			return nil
		})
	}

	// Deploy Cert-Manager
	if deployWithCertManager {
		g.Go(func() error {
			if !deployCertMgr {
				dl.Println("⏭️  Cert-Manager already deployed. Skipping.")
				dl.FinishComponent("cert-manager", true)
				dc.advanceStep()
				return nil
			}
			dl.Printf("\n📦 Deploying Cert-Manager (Namespace: %s)...\n", "cert-manager")
			runHelmFn(gCtx, "repo", "add", "jetstack", "https://charts.jetstack.io")
			runHelmFn(gCtx, "repo", "update", "jetstack")
			// global.clusterDomain ensures cert-manager generates webhook TLS certificates
			// and service references using the actual cluster DNS domain (not always cluster.local).
			// report.Network.ClusterDomain is detected from the live cluster by kates detect.
			clusterDomain := dc.resolveClusterDomain()
			err := runHelmFn(gCtx, "upgrade", "--install", "cert-manager", "jetstack/cert-manager",
				"--version", "v1.13.3",
				"-n", "cert-manager", "--create-namespace",
				"--set", "installCRDs=true",
				"--set", "startupapicheck.enabled=false",
				"--set", "global.clusterDomain="+clusterDomain,
				"--timeout", "10m")
			if err != nil {
				return err
			}

			dl.Println("    - Waiting for Cert-Manager CRDs to be established...")
			if err := runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
				"crd", "clusterissuers.cert-manager.io", "--timeout=180s"); err != nil {
				return err
			}

			// On re-deploys the cainjector may hold a stale CA from the previous
			// installation. Restart it to force fresh CA bundle generation and
			// re-injection into the MutatingWebhookConfiguration.
			dl.Println("    - Restarting Cert-Manager CA injector for fresh CA bundle...")
			runExecFn(gCtx, "kubectl", "rollout", "restart",
				"deployment/cert-manager-cainjector", "-n", "cert-manager")
			runExecFn(gCtx, "kubectl", "rollout", "status",
				"deployment/cert-manager-cainjector", "-n", "cert-manager", "--timeout=90s")

			// The definitive readiness signal: poll the MutatingWebhookConfiguration
			// until caBundle is non-empty. Only then can the API server verify the
			// webhook TLS certificate without x509: certificate signed by unknown authority.
			dl.Println("    - Polling for Cert-Manager CA bundle injection...")
			const caTimeout = 180 * time.Second
			const caPoll = 3 * time.Second
			caDeadline := time.Now().Add(caTimeout)
			for {
				out, err := exec.CommandContext(gCtx,
					"kubectl", "get",
					"mutatingwebhookconfiguration", "cert-manager-webhook",
					"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
				).Output()
				if err == nil && len(strings.TrimSpace(string(out))) > 0 {
					dl.Println("    - CA bundle injected. Applying ClusterIssuer...")
					break
				}
				if time.Now().After(caDeadline) {
					dl.Println("    ⚠ CA bundle injection timed out — proceeding with webhook bypass fallback")
					break
				}
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case <-time.After(caPoll):
				}
			}

			clusterIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}`

			// Try applying normally first (3 quick attempts).
			var lastErr error
			for attempt := 1; attempt <= 3; attempt++ {
				if lastErr = runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer); lastErr == nil {
					return nil
				}
				if attempt < 3 {
					time.Sleep(3 * time.Second)
				}
			}

			// Hard fallback: temporarily set failurePolicy: Ignore so the API server
			// allows the resource creation call through even if webhook TLS is still
			// not verifiable. cert-manager will reconcile the webhook config shortly.
			dl.Println("    ⚠ Webhook TLS not verifiable — temporarily setting failurePolicy: Ignore")
			runExecFn(gCtx, "kubectl", "patch",
				"mutatingwebhookconfiguration", "cert-manager-webhook",
				"--type=json", "-p",
				`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]`)
			time.Sleep(2 * time.Second) // let the API server pick up the patch

			applyErr := runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer)

			// Always restore failurePolicy: Fail regardless of outcome.
			runExecFn(gCtx, "kubectl", "patch",
				"mutatingwebhookconfiguration", "cert-manager-webhook",
				"--type=json", "-p",
				`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]`)

			if applyErr != nil {
				return fmt.Errorf("failed to apply cert-manager ClusterIssuer: %w", applyErr)
			}
			dl.Println("    - ClusterIssuer created. Webhook policy restored.")
			return nil
		})
	}

	// Deploy Kyverno
	if deployWithKyverno {
		g.Go(func() error {
			if !deployKyvernoFlag {
				dl.Println("⏭️  Kyverno already deployed. Skipping.")
				dl.FinishComponent("kyverno", true)
				dc.advanceStep()
				return nil
			}
			dl.Println("\n📦 Deploying Kyverno (Namespace: kyverno)...")
			runHelmFn(gCtx, "repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
			runHelmFn(gCtx, "repo", "update", "kyverno")

			// Kyverno v3.x splits into 4 controllers; replicaCount=1 is a v2.x flag.
			// Pinned to 3.6.4 — the last stable patch of the 3.6 minor series.
			// Chart versions: https://kyverno.github.io/kyverno/
			// global.clusterDomain ensures Kyverno webhook certificates use the
			// correct cluster DNS domain — same value detected for all other components.
			kyvernoDomain := dc.resolveClusterDomain()
			err := runHelmFn(gCtx, "upgrade", "--install", "kyverno", "kyverno/kyverno",
				"--version", "3.6.4",
				"-n", "kyverno", "--create-namespace",
				"--set", "admissionController.replicas=1",
				"--set", "backgroundController.replicas=1",
				"--set", "cleanupController.replicas=1",
				"--set", "reportsController.replicas=1",
				"--set", "global.clusterDomain="+kyvernoDomain,
				"--timeout", "5m")
			if err != nil {
				return err
			}

			// Wait for Kyverno CRDs before anything downstream tries to use them.
			// `kubectl wait --for=condition=Established` can fail early on some
			// clusters with transient "no .status.conditions" races right after CRD
			// creation. Poll explicitly and tolerate empty status until established.
			dl.Println("    - Waiting for Kyverno CRDs to be established...")
			kyvernoCRDs := []string{
				"clusterpolicies.kyverno.io",
				"policies.kyverno.io",
			}
			deadline := time.Now().Add(180 * time.Second)
			for _, crd := range kyvernoCRDs {
				for {
					out, checkErr := exec.CommandContext(gCtx,
						"kubectl", "get", "crd", crd,
						"-o", "jsonpath={.status.conditions[?(@.type==\"Established\")].status}",
					).Output()
					if checkErr == nil && strings.Contains(string(out), "True") {
						break
					}
					if time.Now().After(deadline) {
						if checkErr != nil {
							return fmt.Errorf("kyverno CRD %s did not become Established: %w", crd, checkErr)
						}
						return fmt.Errorf("kyverno CRD %s did not become Established before timeout", crd)
					}
					select {
					case <-gCtx.Done():
						return gCtx.Err()
					case <-time.After(2 * time.Second):
					}
				}
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed during Group A (Operators) deployments: %w", err)
	}

	// ── Sequential Readiness Waits for Group A ──
	if deployWithStrimzi {
		if !deployStrimzi {
			// Already skipped above, but ensure progress isn't blocked
		} else {
			if err := dc.deployComponent("strimzi", "strimzi-operator", "name=strimzi-cluster-operator", 5*time.Minute, "Strimzi operator readiness failed"); err != nil {
				return err
			}
		}
	}
	if deployWithCertManager {
		if !deployCertMgr {
			// already handled
		} else {
			if err := dc.deployComponent("cert-manager", "cert-manager", "app.kubernetes.io/instance=cert-manager", 10*time.Minute, "Cert-Manager readiness failed"); err != nil {
				return err
			}
		}
	}
	if deployWithKyverno {
		if !deployKyvernoFlag {
			// already handled
		} else {
			if err := dc.deployComponent("kyverno", "kyverno", "app.kubernetes.io/instance=kyverno", 5*time.Minute, "Kyverno readiness failed"); err != nil {
				return err
			}
		}
	}

	// Bust Kubernetes Discovery Cache so Helm knows about the newly created CRDs
	dl.Println("    - Refreshing API server schema cache...")
	if home, err := os.UserHomeDir(); err == nil {
		os.RemoveAll(fmt.Sprintf("%s/.kube/cache/discovery", home))
		os.RemoveAll(fmt.Sprintf("%s/.cache/helm", home))
	}

	return nil
}

// deployGroupB deploys Group B components (Core Infrastructure) in parallel:
// Kafka, Monitoring, PostgreSQL, followed by sequential readiness waits,
// entity operator wait, Kafka Connect, and CDC connectors.
func deployGroupB(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	connectNS := dc.ns.connect
	jaegerNS := dc.ns.jaeger

	// ---------------------------------------------------------
	// GROUP B: Core Infrastructure (Parallel)
	// ---------------------------------------------------------

	deployKafka := !isHelmReleaseDeployedFn(ctx, "krafter", kafkaNS)
	deployMon := false
	if deployWithMonitoring {
		deployMon = !isHelmReleaseDeployedFn(ctx, "monitoring", jaegerNS)
	}
	deployPG := false
	if deployWithKafkaConnect {
		deployPG = !isHelmReleaseDeployedFn(ctx, "postgresql", deployDbNS)
	}

	if deployKafka {
		dl.Printf("\n📦 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)
	} else {
		dl.Println("⏭️  Kafka Cluster already deployed. Skipping.")
		dl.FinishComponent("kafka", true)
		dc.advanceStep()
		dl.FinishComponent("kafka-users", true)
		dc.advanceStep()
	}

	if deployWithMonitoring {
		if deployMon {
			dl.Printf("\n📦 Deploying Monitoring (Prometheus + Grafana) (Namespace: %s)...\n", jaegerNS)
		} else {
			dl.Println("⏭️  Monitoring stack already deployed. Skipping.")
			dl.FinishComponent("monitoring", true)
			dc.advanceStep()
		}
	}

	if deployWithKafkaConnect {
		if deployPG {
			dl.Printf("\n📦 Deploying PostgreSQL CDC Database (Namespace: %s)...\n", deployDbNS)
		} else {
			dl.Println("⏭️  PostgreSQL already deployed. Skipping.")
			dl.FinishComponent("postgres", true)
			dc.advanceStep()
		}
	}

	g2, g2Ctx := errgroup.WithContext(ctx)

	// Deploy Monitoring (Prometheus + Grafana via charts/monitoring)
	if deployWithMonitoring && deployMon {
		g2.Go(func() error {
			// Update chart dependencies (kube-prometheus-stack subchart).
			runHelmFn(g2Ctx, "dependency", "update", "charts/monitoring")

			return runHelmFn(g2Ctx, "upgrade", "--install", "monitoring",
				"charts/monitoring",
				"-n", jaegerNS, "--create-namespace",
				"-f", dc.chartOverlay("charts/monitoring"),
				"--set", "kube-prometheus-stack.global.clusterDomain="+dc.report.Network.ClusterDomain,
				"--timeout", "10m")
		})
	}

	if deployWithKafkaConnect && deployPG {
		g2.Go(func() error {
			dbNS := deployDbNS

			runExecStdinFn(g2Ctx, "kubectl", []string{"apply", "-f", "-"}, fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, dbNS))

			runHelmFn(g2Ctx, "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami")
			runHelmFn(g2Ctx, "repo", "update", "bitnami")

			if err := runHelmFn(g2Ctx, "upgrade", "--install", "postgresql", "bitnami/postgresql",
				"-n", dbNS, "--create-namespace",
				"--set", "auth.postgresPassword=postgres",
				"--set", "auth.username=debezium",
				"--set", "auth.password=debezium",
				"--set", "auth.database=orders",
				"--set", "primary.extendedConfiguration=wal_level = logical\nmax_wal_senders = 10\nmax_replication_slots = 10",
				"--timeout", "5m"); err != nil {
				return err
			}

			return nil
		})
	}

	// Deploy Kafka
	if deployKafka {
		g2.Go(func() error {

			runHelmFn(g2Ctx, "dependency", "update", "charts/kafka-cluster")
			// No --wait: Helm only needs to submit the manifests; the Strimzi
			// operator drives reconciliation asynchronously. waitKafkaReady()
			// below is the real readiness gate.
			clusterDomain := dc.resolveClusterDomain()
			kafkaArgs := []string{"upgrade", "--install", "krafter", "charts/kafka-cluster", "-n", kafkaNS, "--create-namespace"}

			kafkaArgs = append(kafkaArgs,
				"-f", dc.valuesFile,
				"--set", "global.clusterDomain="+clusterDomain,
				"--set", "networkPolicies.connectNamespace="+connectNS,
				"--timeout", "10m",
			)
			if dc.isKind {
				kafkaArgs = append(kafkaArgs, "-f", "charts/kafka-cluster/values-kind.yaml")
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

			// (Values files are already appended above so these overrides take precedence)

			if err := runHelmFn(g2Ctx, kafkaArgs...); err != nil {
				return err
			}

			return nil
		})
	}

	if err := g2.Wait(); err != nil {
		return fmt.Errorf("failed during Group B (Core Infra) deployments: %w", err)
	}

	// ---------------------------------------------------------
	// ── Sequential Readiness Waits for Group B ───────────────
	// ---------------------------------------------------------
	// 1. Kafka Cluster
	if deployKafka {
		if err := dc.deployComponent("kafka", kafkaNS, "strimzi.io/cluster=krafter", 15*time.Minute, ""); err != nil {
			return fmt.Errorf("kafka readiness failed: %w", err)
		}
	}

	// 2. Monitoring Stack
	if deployWithMonitoring && deployMon {
		if err := dc.deployComponent("monitoring", jaegerNS, "release=monitoring", 10*time.Minute, ""); err != nil {
			return fmt.Errorf("monitoring readiness failed: %w", err)
		}
	}

	// 2. PostgreSQL
	if deployWithKafkaConnect {
		if deployPG {
			err := dc.deployComponent("postgres", deployDbNS, "app.kubernetes.io/instance=postgresql", 5*time.Minute, "")
			if err != nil {
				dl.Printf("    %s PostgreSQL not ready: %v\n", output.WarningStyle.Render("⚠"), err)
			} else if !isTesting {
				// Grant superuser and replication to debezium after DB is ready
				for i := 0; i < 5; i++ {
					err := exec.CommandContext(ctx, "kubectl", "exec", "-n", deployDbNS, "postgresql-0", "--",
						"env", "PGPASSWORD=postgres", "psql", "-U", "postgres", "-c",
						"ALTER ROLE debezium SUPERUSER REPLICATION;").Run()
					if err == nil {
						break
					}
					time.Sleep(2 * time.Second)
				}
			}
		}
	}

	// Kafka users and topics are managed declaratively by the Helm chart
	// (charts/kafka-cluster templates/users.yaml + templates/topics.yaml).
	// Applying them again via raw manifests causes MethodNotAllowed (HTTP 405)
	// field-ownership conflicts with Helm's server-side apply.
	dl.Println("    - Kafka users and topics managed by Helm chart")

	if !isTesting {
		dl.Println("    - Waiting for Entity Operator to start...")
		eoDeadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(eoDeadline) {
			eoOut, _ := exec.CommandContext(ctx,
				"kubectl", "get", "pods", "-n", kafkaNS,
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
		err := waitKafkaUsersReadySilent(ctx, kafkaNS, 8*time.Minute)
		if err != nil {
			dl.Printf("    %s KafkaUsers not all ready after 5m — downstream deploys will retry secret lookup\n", output.WarningStyle.Render("⚠"))
		}
	}

	// 3. Kafka Connect (separate chart — deployed in connectNS)
	if deployWithKafkaConnect {
		if err := deployKafkaConnectStack(dc); err != nil {
			return err
		}
	}

	return nil
}

// deployKafkaConnectStack handles all Kafka Connect related deployments:
// namespace setup, secret copying, Helm install, readiness wait, and CDC connectors.
func deployKafkaConnectStack(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	connectNS := dc.ns.connect

	// Ensure connect namespace exists (idempotent — won't fail if already present)
	dl.Println("    - Ensuring connect namespace exists...")
	connectNsYaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, connectNS)
	runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectNsYaml)

	connectDomain := dc.resolveClusterDomain()
	bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, connectDomain)

	// Copy kates-connect secret and kafka-metrics ConfigMap from kafka namespace to connect namespace (cross-namespace)
	if connectNS != kafkaNS {
		// Copy kafka-metrics ConfigMap (required by KafkaConnect metricsConfig)
		dl.Println("    - Copying kafka-metrics ConfigMap to connect namespace...")
		metricsData, metricsErr := exec.CommandContext(ctx, "kubectl", "get", "configmap", "kafka-metrics",
			"-n", kafkaNS, "-o", "jsonpath={.data.kafka-metrics-config\\.yml}").Output()
		if metricsErr == nil && len(metricsData) > 0 {
			// Build a clean ConfigMap without Helm ownership annotations
			// to avoid field-manager conflicts on kubectl apply.
			var indented strings.Builder
			for _, line := range strings.Split(string(metricsData), "\n") {
				indented.WriteString("    " + line + "\n")
			}
			metricsYaml := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kafka-metrics\n  namespace: %s\ndata:\n  kafka-metrics-config.yml: |\n%s", connectNS, indented.String())
			if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, metricsYaml); err != nil {
				// Fallback: force ownership with server-side apply (handles leftover
				// field-manager conflicts from prior broken deploys)
				dl.Println("    - Retrying with server-side apply (--force-conflicts)...")
				runExecStdinFn(ctx, "kubectl", []string{"apply", "--server-side", "--force-conflicts", "-f", "-"}, metricsYaml)
			}
		} else {
			dl.Printf("    ⚠️  ConfigMap 'kafka-metrics' not found in namespace %s\n", kafkaNS)
		}

		dl.Println("    - Copying kates-connect credentials to connect namespace...")
		var pwBytes []byte
		var jaasBytes []byte
		secretDeadline := time.Now().Add(2 * time.Minute)
		for {
			pwOut, pwErr := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-connect",
				"-n", kafkaNS, "-o", "jsonpath={.data.password}").Output()
			if pwErr == nil && len(strings.TrimSpace(string(pwOut))) > 0 {
				pwBytes = bytes.TrimSpace(pwOut)
				jaasOut, jaasErr := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-connect",
					"-n", kafkaNS, "-o", "jsonpath={.data.sasl\\.jaas\\.config}").Output()
				if jaasErr == nil {
					jaasBytes = bytes.TrimSpace(jaasOut)
				}
				break
			}
			if time.Now().After(secretDeadline) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		if len(pwBytes) > 0 {
			var dataLines strings.Builder
			dataLines.WriteString(fmt.Sprintf("  password: %s\n", string(pwBytes)))
			if len(jaasBytes) > 0 {
				dataLines.WriteString(fmt.Sprintf("  sasl.jaas.config: %s\n", string(jaasBytes)))
			}

			connectSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-connect
  namespace: %s
type: Opaque
data:
%s`, connectNS, dataLines.String())
			runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectSecretYaml)
		} else {
			dl.Printf("    ⚠️  Secret 'kates-connect' not found in namespace %s after waiting — KafkaUser may not be ready\n", kafkaNS)
		}
	}

	dl.Println("    - Creating PostgreSQL credentials secret for Kafka Connect...")
	pgSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: connect-pg-credentials
  namespace: %s
type: Opaque
stringData:
  password: debezium
  username: debezium`, connectNS)
	runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, pgSecretYaml)

	connectDeployed := isHelmReleaseDeployedFn(ctx, "connect-cluster", connectNS)
	if connectDeployed && !isTesting {
		// Helm says deployed, but verify pods actually exist.
		// A previous deploy may have installed the chart but the
		// workload never started (e.g. missing ConfigMap).
		podCheck, _ := exec.CommandContext(ctx, "kubectl", "get", "pods",
			"-n", connectNS, "-l", "strimzi.io/kind=KafkaConnect",
			"-o", "jsonpath={.items}").Output()
		if string(podCheck) == "[]" || len(strings.TrimSpace(string(podCheck))) == 0 {
			dl.Println("    ⚠️  Kafka Connect release exists but no pods found — upgrading...")
			connectDeployed = false
		}
	}
	if !connectDeployed {
		dl.Printf("\n📦 Deploying Kafka Connect (Namespace: %s)...\n", connectNS)

		registryFQDN := fmt.Sprintf("http://apicurio-apicurio-registry.%s.svc.%s:80/apis/ccompat/v7",
			kafkaNS, connectDomain)

		// Use the same FQDN pattern as the kates backend for bootstrap servers
		bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, connectDomain)

		connectArgs := []string{"upgrade", "--install", "connect-cluster", "charts/connect-cluster",
			"-n", connectNS, "--create-namespace",
			"-f", dc.chartOverlay("charts/connect-cluster"),
			"--set", "clusterDomain=" + connectDomain,
			"--set", "kafka.namespace=" + kafkaNS,
			"--set", "kafka.bootstrapServers=" + bootstrap,
			"--set", "schemaRegistry.enabled=true",
			"--set", "extraConfig.schema\\.registry\\.url=" + registryFQDN,
			"--set", "databaseEgress[0].namespace=" + deployDbNS,
			"--set", "databaseEgress[0].port=5432",
			"--set", "databaseEgress[0].podSelector.app\\.kubernetes\\.io/name=postgresql",
			"--timeout", "10m",
		}

		// Enable NetworkPolicy on non-Kind clusters (same as kates backend)
		if !dc.isKind {
			connectArgs = append(connectArgs, "--set", "networkPolicy.enabled=true")
		}

		// When Connect is in a different namespace than Kafka, brokers advertise
		// short hostnames (e.g. krafter-brokers-0.krafter-kafka-brokers.kafka.svc)
		// that only resolve within the kafka namespace DNS search domain.
		// Add the kafka namespace to Connect pod DNS search list.
		if connectNS != kafkaNS {
			kafkaSvcDomain := fmt.Sprintf("%s.svc.%s", kafkaNS, connectDomain)
			connectArgs = append(connectArgs,
				"--set", "dnsConfig.searches[0]="+kafkaSvcDomain,
			)
		}

		if deployHA {
			connectArgs = append(connectArgs, "--set", "replicas=3")
		} else {
			connectArgs = append(connectArgs, "--set", "replicas=1")
		}

		monitoringEnabled := deployWithMonitoring
		if monitoringEnabled {
			// Cleverly detect if CRDs are actually present before enabling monitoring on Connect
			out, err := exec.CommandContext(ctx, "kubectl", "get", "crd", "podmonitors.monitoring.coreos.com", "--ignore-not-found").CombinedOutput()
			if err != nil || len(bytes.TrimSpace(out)) == 0 {
				monitoringEnabled = false
				dl.Printf("    ⚠️  Monitoring CRDs not found. Disabling monitoring for Kafka Connect.\n")
			}
		}

		if !monitoringEnabled {
			connectArgs = append(connectArgs,
				"--set", "alerts.enabled=false",
				"--set", "podMonitors.enabled=false",
				"--set", "dashboards.enabled=false",
			)
		}

		if err := runHelmFn(ctx, connectArgs...); err != nil {
			return err
		}
	} else {
		dl.Println("⏭️  Kafka Connect already deployed. Skipping.")
		dl.FinishComponent("kafka-connect", true)
		dc.advanceStep()
	}

	if err := dc.deployComponent("kafka-connect", connectNS, "strimzi.io/kind=KafkaConnect", 15*time.Minute, ""); err != nil {
		return fmt.Errorf("Kafka Connect failed to become ready: %w", err)
	}

	// Deploy Debezium connector
	checkOut, checkErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "debezium-postgres-source", "-n", connectNS, "--no-headers").CombinedOutput()
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
    schema.history.internal.kafka.topic: cdc-schema-history`, connectNS, deployDbNS, connectNS, bootstrap, connectNS)
		if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectorYaml); err != nil {
			dl.Printf("    %s Failed to deploy Debezium connector: %v\n", output.WarningStyle.Render("⚠"), err)
			dl.FinishComponent("kafka-connector", false)
		} else {
			dl.Printf("    %s Debezium PostgreSQL CDC connector deployed\n", output.SuccessStyle.Render("✔"))
		}
	} else {
		dl.Printf("    %s Debezium connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
	}

	// Deploy JDBC Sink connector
	sinkCheckOut, sinkCheckErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "jdbc-sink-connector", "-n", connectNS, "--no-headers").CombinedOutput()
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
    auto.evolve: "true"`, connectNS, deployDbNS, connectNS, connectNS)
		if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, sinkYaml); err != nil {
			dl.Printf("    %s Failed to deploy JDBC Sink connector: %v\n", output.WarningStyle.Render("⚠"), err)
		} else {
			dl.Printf("    %s JDBC Sink connector deployed\n", output.SuccessStyle.Render("✔"))
		}
	} else {
		dl.Printf("    %s JDBC Sink connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
	}

	if !isTesting {
		dl.StartComponent("kafka-connector", 10*time.Minute)
		if err := waitConnectorReadySilent(ctx, connectNS, 10*time.Minute); err != nil {
			dl.FinishComponent("kafka-connector", false)
			return fmt.Errorf("Kafka Connectors failed to become ready: %w", err)
		}
		dl.FinishComponent("kafka-connector", true)
	}
	dc.advanceStep()

	return nil
}

// deployGroupC deploys Group C components (Apps / Sequential):
// Apicurio Schema Registry, Kates Backend, Kafka UI, and Litmus Chaos.
func deployGroupC(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	appNS := dc.ns.app
	kafkaUINS := dc.ns.kafkaUI
	chaosNS := dc.ns.chaos

	// ---------------------------------------------------------
	// GROUP C (Apps / Sequential)
	// ---------------------------------------------------------
	// Deploy Schema Registry (if requested)
	if deployWithSchemaRegistry == "apicurio" {
		if !isHelmReleaseDeployedFn(ctx, "apicurio", kafkaNS) {
			dl.Printf("\n📦 Deploying Apicurio Schema Registry (Namespace: %s)...\n", kafkaNS)
			dl.StartComponent("apicurio", 5*time.Minute)
			if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", "charts/apicurio-registry", "-n", kafkaNS, "--create-namespace", "--timeout", "5m"); err != nil {
				dl.FinishComponent("apicurio", false)
				return err
			}
			dl.FinishComponent("apicurio", true)
			dc.advanceStep()
		} else {
			dl.Println("⏭️  Apicurio already deployed.")
			dl.FinishComponent("apicurio", true)
			dc.advanceStep()
		}
	}

	// Deploy Kates
	if !isHelmReleaseDeployedFn(ctx, "kates", appNS) {
		dl.Printf("\n📦 Deploying Kates Backend (Namespace: %s)...\n", appNS)
		// Auto-cleanup stale ClusterRole ownership from previous topology switches
		cleanupStaleClusterResource(ctx, "clusterrole", "kates", appNS)
		cleanupStaleClusterResource(ctx, "clusterrolebinding", "kates", appNS)

		if kafkaNS != appNS {
			// Ensure app namespace exists before copying secrets into it.
			nsYaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
spec: {}`, appNS)
			runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, nsYaml)

			// The KafkaUser was already waited on in Group B — just read the Secret.
			pwCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-backend",
				"-n", kafkaNS, "-o", "jsonpath={.data.password}")
			pwBytes, pwErr := pwCmd.Output()

			if pwErr == nil && len(pwBytes) > 0 {
				dl.Println("    - Copying Kafka SASL credentials to app namespace...")
				secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-backend
  namespace: %s
type: Opaque
data:
  password: %s`, appNS, string(pwBytes))
				runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
			} else {
				dl.Printf("    ⚠️  Secret 'kates-backend' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
			}
		}

		katesBootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, dc.report.Network.ClusterDomain)
		dl.Println("    - Waiting for Kates backend pods to become ready (this may take 2-3 minutes)...")
		if err := runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates",
			"-n", appNS, "--create-namespace",
			"-f", dc.valuesFile,
			"-f", dc.chartOverlay("charts/kates"),
			"--set", "kafka.bootstrapServers="+katesBootstrap,
			"--set", "kafka.topicNamespace="+kafkaNS,
			"--set", fmt.Sprintf("monitoring.enabled=%t", deployWithMonitoring),
			"--timeout", "8m"); err != nil {
			return err
		}

		if err := dc.deployComponent("kates", appNS, "app.kubernetes.io/instance=kates", 8*time.Minute, ""); err != nil {
			return err
		}
	} else {
		dl.Println("⏭️  Kates Backend already deployed.")
		dl.FinishComponent("kates", true)
		dc.advanceStep()
	}

	// Deploy Kafka UI
	if deployWithKafkaUI {
		if !isHelmReleaseDeployedFn(ctx, "kafka-ui", kafkaUINS) {
			dl.Printf("\n🖥️  Deploying Kafka UI (Namespace: %s)...\n", kafkaUINS)
			dl.StartComponent("kafka-ui", 5*time.Minute)

			// Cross-namespace secret copy (KafkaUser secret lives in Kafka NS)
			if kafkaUINS != kafkaNS {
				nsYaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
spec: {}`, kafkaUINS)
				runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, nsYaml)

				dl.Println("    - Copying kafka-ui SASL credentials to UI namespace...")
				pwCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "kafka-ui",
					"-n", kafkaNS, "-o", "jsonpath={.data.password}")
				pwBytes, pwErr := pwCmd.Output()
				if pwErr == nil && len(pwBytes) > 0 {
					secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kafka-ui
  namespace: %s
type: Opaque
data:
  password: %s`, kafkaUINS, string(pwBytes))
					runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
				} else {
					dl.Printf("    ⚠️  Secret 'kafka-ui' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
				}
			}

			helmArgs := []string{
				"upgrade", "--install", "kafka-ui", "charts/kafka-ui",
				"-n", kafkaUINS, "--create-namespace",
				"--set", "kafka.clusterName=" + clusterName,
				"--set", "kafka.namespace=" + kafkaNS,
				"--timeout", "5m",
			}
			if overlay := dc.chartOverlay("charts/kafka-ui"); dc.fileExists(overlay) {
				helmArgs = append(helmArgs, "-f", overlay)
			}
			if err := runHelmFn(ctx, helmArgs...); err != nil {
				dl.FinishComponent("kafka-ui", false)
				return err
			}
			dl.FinishComponent("kafka-ui", true)
			dc.advanceStep()
		} else {
			dl.Println("⏭️  Kafka UI already deployed.")
			dl.FinishComponent("kafka-ui", true)
			dc.advanceStep()
		}
	}

	// Deploy Chaos
	if deployWithChaos {
		if !isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
			dl.Printf("\n📦 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrole", "litmus", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrolebinding", "litmus", chaosNS)
			runHelmFn(ctx, "dependency", "update", "charts/kates-chaos")
			dl.Println("    - Waiting for Litmus Chaos pods to become ready (this may take a few minutes)...")
			if err := runHelmFn(ctx, "upgrade", "--install", "chaos", "charts/kates-chaos",
				"-n", chaosNS, "--create-namespace",
				"-f", dc.valuesFile,
				"-f", dc.chartOverlay("charts/kates-chaos"),
				"--set", "rbac.kafkaNamespace="+kafkaNS,
				"--timeout", "5m"); err != nil {
				return err
			}

			if err := dc.deployComponent("chaos", chaosNS, "app.kubernetes.io/instance=chaos", 5*time.Minute, ""); err != nil {
				return err
			}
		} else {
			dl.Println("⏭️  Litmus Chaos already deployed.")
			dl.FinishComponent("chaos", true)
			dc.advanceStep()
		}
	}

	return nil
}

// updateActiveContextAPIKey syncs the API key from the deployed cluster
// secret into the active CLI context configuration.
func updateActiveContextAPIKey(ctx context.Context, appNS string) {
	out, err := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-api-key", "-n", appNS, "-o", "jsonpath={.data.api-key}").Output()
	if err != nil {
		return
	}
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		apiKey := strings.TrimSpace(string(decoded))
		if apiKey != "" {
			cfg := loadConfig()
			ctxName := cfg.CurrentContext
			if contextFlag != "" {
				ctxName = contextFlag
			}
			if active, ok := cfg.Contexts[ctxName]; ok {
				active.APIKey = apiKey
				cfg.Contexts[ctxName] = active
				_ = saveConfig(cfg)
				dl.Printf("    ✓ Automatically synced API Key to context %q: %s****\n\n", ctxName, apiKey[:4])
			}
		}
	}
}

func (dc *deployContext) resolveClusterDomain() string {
	domain := dc.report.Network.ClusterDomain
	if domain == "" {
		return "cluster.local"
	}
	return domain
}

func (dc *deployContext) deployComponent(id, namespace, selector string, timeout time.Duration, errMsg string) error {
	dl.StartComponent(id, timeout)
	if !isTesting {
		if err := waitComponentReadySilent(dc.ctx, namespace, selector, timeout); err != nil {
			dl.FinishComponent(id, false)
			if errMsg != "" {
				output.Error(fmt.Sprintf("%s: %v", errMsg, err))
			}
			return err
		}
	}
	dl.FinishComponent(id, true)
	dc.advanceStep()
	return nil
}
