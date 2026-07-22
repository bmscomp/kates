package com.bmscomp.kates.engine;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.Statement;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.Secret;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.TestResult;

@ApplicationScoped
public class CdcIntegrationService {

    private static final Logger LOG = Logger.getLogger(CdcIntegrationService.class);
    private static final String SOURCE_CONNECTOR_PREFIX = "integration-test-source-";
    private static final String SINK_CONNECTOR_PREFIX = "integration-test-sink-";
    private static final String SOURCE_TABLE_PREFIX = "connect_test_src_";
    private static final String SINK_TABLE_PREFIX = "connect_test_sink_";
    private static final String SLOT_PREFIX = "connect_test_src_slot_";
    private static final String TOPIC_RESOURCE_PREFIX = "cdc.public.connect-test-src-";
    private static final String TOPIC_NAME_PREFIX = "cdc.public.";
    private static final int K8S_NAME_MAX_LENGTH = 63;
    private static final int POSTGRES_IDENTIFIER_MAX_LENGTH = 63;
    private static final int RUN_TOKEN_MAX_LENGTH = 32;
    private static final CustomResourceDefinitionContext CONNECTOR_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withScope("Namespaced")
            .withPlural("kafkaconnectors")
            .build();

    private static final CustomResourceDefinitionContext TOPIC_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withScope("Namespaced")
            .withPlural("kafkatopics")
            .build();

    private static final CustomResourceDefinitionContext KAFKA_CONNECT_CRD =
            new CustomResourceDefinitionContext.Builder()
                    .withGroup("kafka.strimzi.io")
                    .withVersion("v1")
                    .withScope("Namespaced")
                    .withPlural("kafkaconnects")
                    .build();

    @Inject
    KubernetesClient kubernetesClient;

    @org.eclipse.microprofile.config.inject.ConfigProperty(name = "kates.kafka.sasl.username")
    java.util.Optional<String> kafkaUsername;

    @org.eclipse.microprofile.config.inject.ConfigProperty(name = "kates.kafka.sasl.password")
    java.util.Optional<String> kafkaPassword;

    @org.eclipse.microprofile.config.inject.ConfigProperty(name = "kates.cdc.topic-namespace")
    Optional<String> configuredCdcTopicNamespace;

    @org.eclipse.microprofile.config.inject.ConfigProperty(
            name = "kates.topology.kafka-namespace",
            defaultValue = "kafka")
    String kafkaNamespace;

    @jakarta.inject.Inject
    KatesExecutor executor;

    public CompletableFuture<BenchmarkStatus> runCdcTest(
            BenchmarkTask task, java.util.function.BiConsumer<String, Map<String, Long>> progressCallback) {
        return CompletableFuture.supplyAsync(
                () -> {
                    TestResult.TaskStatus endState = TestResult.TaskStatus.RUNNING;
                    String errorMsg = null;
                    int recordsProcessed = 0;
                    Map<String, Long> phaseDurations = new LinkedHashMap<>();
                    List<String> topicNamespacesForCleanup = new ArrayList<>();
                    CdcRunResources runResources =
                            createRunResources(UUID.randomUUID().toString());

                    String connectNamespace = "kafka"; // Default
                    String testDbNS = "database"; // Default
                    String connectName = "krafter-connect"; // Default
                    String kafkaClusterName = "krafter"; // Default

                    try {
                        // ── Phase: DB_SETUP ──────────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("DB_SETUP", phaseDurations);
                        Instant phaseStart = Instant.now();

                        // Auto-discover PostgreSQL namespace (best effort).
                        // Some clusters grant namespace-scoped RBAC only; in that case keep the default namespace.
                        try {
                            var pgServices = kubernetesClient
                                    .services()
                                    .inAnyNamespace()
                                    .withLabel("app.kubernetes.io/name", "postgresql")
                                    .list()
                                    .getItems();
                            if (!pgServices.isEmpty()) {
                                testDbNS = pgServices.get(0).getMetadata().getNamespace();
                                LOG.info("Auto-discovered PostgreSQL namespace: " + testDbNS);
                            }
                        } catch (Exception e) {
                            LOG.warn(
                                    "Failed to auto-discover PostgreSQL namespace; using default namespace " + testDbNS,
                                    e);
                        }

                        // Auto-discover KafkaConnect namespace and cluster name
                        try {
                            var connectCRs = kubernetesClient
                                    .genericKubernetesResources(KAFKA_CONNECT_CRD)
                                    .inAnyNamespace()
                                    .list()
                                    .getItems();
                            if (connectCRs.size() > 0) {
                                var connectCr = connectCRs.get(0);
                                connectName = connectCr.getMetadata().getName();
                                connectNamespace = connectCr.getMetadata().getNamespace();
                                Map<String, String> labels =
                                        connectCr.getMetadata().getLabels();
                                if (labels != null && labels.containsKey("strimzi.io/cluster")) {
                                    kafkaClusterName = labels.get("strimzi.io/cluster");
                                }
                                LOG.info("Auto-discovered KafkaConnect cluster: " + connectName + " (Kafka: "
                                        + kafkaClusterName + ") in namespace: " + connectNamespace);
                            }
                        } catch (Exception e) {
                            LOG.warn("Failed to auto-discover KafkaConnect CRs, using defaults", e);
                        }

                        // Get PostgreSQL password
                        LOG.info("Extracting PostgreSQL credentials from secret in namespace: " + testDbNS);
                        Secret secret = kubernetesClient
                                .secrets()
                                .inNamespace(testDbNS)
                                .withName("postgresql")
                                .get();

                        if (secret == null) {
                            throw new IllegalStateException("PostgreSQL secret not found in namespace " + testDbNS);
                        }

                        String encodedPassword = secret.getData().get("postgres-password");
                        if (encodedPassword == null) {
                            throw new IllegalStateException("postgres-password not found in secret");
                        }
                        String dbPassword =
                                new String(java.util.Base64.getDecoder().decode(encodedPassword));

                        // Setup test tables (record insert happens after connectors are ready to avoid sink offset
                        // race).
                        String uuidStr = "test-" + UUID.randomUUID().toString();
                        String jdbcUrl = "jdbc:postgresql://postgresql." + testDbNS + ".svc:5432/postgres";
                        LOG.infof(
                                "CDC test run token=%s sourceConnector=%s sinkConnector=%s topic=%s sourceTable=%s sinkTable=%s slot=%s",
                                runResources.runToken(),
                                runResources.sourceConnectorName(),
                                runResources.sinkConnectorName(),
                                runResources.topicName(),
                                runResources.sourceTable(),
                                runResources.sinkTable(),
                                runResources.slotName());

                        LOG.info("Connecting to PostgreSQL at " + jdbcUrl);
                        try (Connection conn = DriverManager.getConnection(jdbcUrl, "postgres", dbPassword);
                                Statement stmt = conn.createStatement()) {

                            // Reset test tables on each run to avoid stale rows/constraints from prior failed runs.
                            stmt.execute("DROP TABLE IF EXISTS " + runResources.sourceTable());
                            stmt.execute("DROP TABLE IF EXISTS " + runResources.sinkTable());
                            stmt.execute("CREATE TABLE " + runResources.sourceTable()
                                    + " (id serial primary key, test_uuid varchar(255))");
                            // Keep sink table non-unique so at-least-once/replayed events never fail the sink task.
                            stmt.execute("CREATE TABLE " + runResources.sinkTable()
                                    + " (id integer, test_uuid varchar(255))");
                        }
                        phaseDurations.put(
                                "DB_SETUP",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // ── Phase: TOPIC_CREATE ──────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("TOPIC_CREATE", phaseDurations);
                        phaseStart = Instant.now();
                        LOG.info("Creating KafkaTopic for CDC...");
                        final List<String> topicNamespaceCandidates = resolveTopicNamespaceCandidates(connectNamespace);
                        topicNamespacesForCleanup = new ArrayList<>(topicNamespaceCandidates);

                        String selectedTopicNamespace = null;
                        for (String topicNamespace : topicNamespaceCandidates) {
                            String topicYaml = String.format(
                                    """
                            apiVersion: kafka.strimzi.io/v1
                            kind: KafkaTopic
                            metadata:
                              name: %s
                              namespace: %s
                              labels:
                                strimzi.io/cluster: %s
                            spec:
                              topicName: %s
                              partitions: 1
                              replicas: 1
                              config:
                                retention.ms: 7200000
                                segment.bytes: 1073741824
                            """,
                                    runResources.topicResourceName(),
                                    topicNamespace,
                                    kafkaClusterName,
                                    runResources.topicName());

                            kubernetesClient
                                    .genericKubernetesResources(TOPIC_CRD)
                                    .inNamespace(topicNamespace)
                                    .load(new java.io.ByteArrayInputStream(topicYaml.getBytes()))
                                    .createOrReplace();

                            if (waitForTopicReady(
                                    topicNamespace, runResources.topicResourceName(), Duration.ofSeconds(20))) {
                                selectedTopicNamespace = topicNamespace;
                                break;
                            }

                            LOG.warnf(
                                    "KafkaTopic %s did not become Ready in namespace %s; trying next candidate",
                                    runResources.topicName(), topicNamespace);
                        }

                        if (selectedTopicNamespace == null) {
                            throw new IllegalStateException("KafkaTopic " + runResources.topicName()
                                    + " did not become Ready in any namespace. Tried: " + topicNamespaceCandidates);
                        }
                        LOG.infof(
                                "KafkaTopic %s resolved in namespace %s",
                                runResources.topicName(), selectedTopicNamespace);
                        phaseDurations.put(
                                "TOPIC_CREATE",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // ── Phase: SOURCE_DEPLOY ─────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("SOURCE_DEPLOY", phaseDurations);
                        phaseStart = Instant.now();
                        LOG.info("Deploying Kafka Source Connector...");
                        // Note: database password is embedded in the connector config. This is acceptable
                        // for short-lived integration test connectors that are deleted within minutes.
                        // For production connectors, use KafkaConnect externalConfiguration with mounted secrets.
                        String sourceYaml = String.format(
                                """
                        apiVersion: kafka.strimzi.io/v1
                        kind: KafkaConnector
                        metadata:
                          name: %s
                          namespace: %s
                          labels:
                            strimzi.io/cluster: %s
                        spec:
                          class: io.debezium.connector.postgresql.PostgresConnector
                          tasksMax: 1
                          config:
                            database.hostname: postgresql.%s.svc
                            database.port: "5432"
                            database.user: postgres
                            database.password: %s
                            database.dbname: postgres
                            topic.prefix: cdc
                            plugin.name: pgoutput
                            slot.name: %s
                            table.include.list: public.%s
                            transforms: unwrap
                            transforms.unwrap.type: io.debezium.transforms.ExtractNewRecordState
                            key.converter: org.apache.kafka.connect.json.JsonConverter
                            key.converter.schemas.enable: true
                            value.converter: org.apache.kafka.connect.json.JsonConverter
                            value.converter.schemas.enable: true
                        """,
                                runResources.sourceConnectorName(),
                                connectNamespace,
                                connectName,
                                testDbNS,
                                dbPassword,
                                runResources.slotName(),
                                runResources.sourceTable());

                        kubernetesClient
                                .genericKubernetesResources(CONNECTOR_CRD)
                                .inNamespace(connectNamespace)
                                .load(new java.io.ByteArrayInputStream(sourceYaml.getBytes()))
                                .createOrReplace();
                        phaseDurations.put(
                                "SOURCE_DEPLOY",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // ── Phase: SINK_DEPLOY ───────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("SINK_DEPLOY", phaseDurations);
                        phaseStart = Instant.now();
                        LOG.info("Deploying Kafka Sink Connector...");
                        String sinkYaml = String.format(
                                """
                        apiVersion: kafka.strimzi.io/v1
                        kind: KafkaConnector
                        metadata:
                          name: %s
                          namespace: %s
                          labels:
                            strimzi.io/cluster: %s
                        spec:
                          class: io.debezium.connector.jdbc.JdbcSinkConnector
                          tasksMax: 1
                          config:
                            connection.url: jdbc:postgresql://postgresql.%s.svc:5432/postgres
                            connection.username: postgres
                            connection.password: %s
                            topics: %s
                            table.name.format: %s
                            insert.mode: insert
                            auto.create: false
                            auto.evolve: false
                            consumer.override.auto.offset.reset: latest
                            transforms: dropDeleted
                            transforms.dropDeleted.type: org.apache.kafka.connect.transforms.ReplaceField$Value
                            transforms.dropDeleted.blacklist: __deleted
                            key.converter: org.apache.kafka.connect.json.JsonConverter
                            key.converter.schemas.enable: true
                            value.converter: org.apache.kafka.connect.json.JsonConverter
                            value.converter.schemas.enable: true
                        """,
                                runResources.sinkConnectorName(),
                                connectNamespace,
                                connectName,
                                testDbNS,
                                dbPassword,
                                runResources.topicName(),
                                runResources.sinkTable());

                        kubernetesClient
                                .genericKubernetesResources(CONNECTOR_CRD)
                                .inNamespace(connectNamespace)
                                .load(new java.io.ByteArrayInputStream(sinkYaml.getBytes()))
                                .createOrReplace();
                        phaseDurations.put(
                                "SINK_DEPLOY",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // ── Phase: CONNECTOR_READY ───────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("CONNECTOR_READY", phaseDurations);
                        phaseStart = Instant.now();
                        LOG.info("Waiting for connectors to be ready...");
                        boolean sourceReady = false;
                        boolean sinkReady = false;
                        for (int i = 0; i < 60; i++) {
                            if (!sourceReady) {
                                GenericKubernetesResource srcCr = kubernetesClient
                                        .genericKubernetesResources(CONNECTOR_CRD)
                                        .inNamespace(connectNamespace)
                                        .withName(runResources.sourceConnectorName())
                                        .get();
                                // Check for permanent failure first
                                String srcFailure = extractConnectorFailure(srcCr);
                                if (srcFailure != null) {
                                    throw new IllegalStateException("Source connector FAILED: " + srcFailure);
                                }
                                sourceReady = isConnectorReady(srcCr);
                            }
                            if (!sinkReady) {
                                GenericKubernetesResource sinkCr = kubernetesClient
                                        .genericKubernetesResources(CONNECTOR_CRD)
                                        .inNamespace(connectNamespace)
                                        .withName(runResources.sinkConnectorName())
                                        .get();
                                String sinkFailure = extractConnectorFailure(sinkCr);
                                if (sinkFailure != null) {
                                    throw new IllegalStateException("Sink connector FAILED: " + sinkFailure);
                                }
                                sinkReady = isConnectorReady(sinkCr);
                            }
                            if (sourceReady && sinkReady) break;
                            if (i > 0 && i % 10 == 0) {
                                LOG.infof(
                                        "Still waiting for connectors (source=%s, sink=%s) after %ds",
                                        sourceReady, sinkReady, i * 2);
                            }
                            Thread.sleep(2000);
                        }

                        if (!sourceReady || !sinkReady) {
                            throw new IllegalStateException("Connectors failed to become Ready within timeout (Source: "
                                    + sourceReady + ", Sink: " + sinkReady + ")");
                        }
                        phaseDurations.put(
                                "CONNECTOR_READY",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // Insert a fresh row after both connectors are Ready so sink consumers with latest offsets
                        // never miss it.
                        try (Connection conn = DriverManager.getConnection(jdbcUrl, "postgres", dbPassword);
                                PreparedStatement ps = conn.prepareStatement(
                                        "INSERT INTO " + runResources.sourceTable() + " (test_uuid) VALUES (?)")) {
                            ps.setString(1, uuidStr);
                            ps.executeUpdate();
                            LOG.info("Inserted test record with UUID after connectors ready: " + uuidStr);
                        }

                        // ── Phase: KAFKA_VERIFY ──────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("KAFKA_VERIFY", phaseDurations);
                        phaseStart = Instant.now();
                        LOG.info("Waiting for CDC event in Kafka...");
                        boolean foundInKafka = false;
                        Properties props = getKafkaConsumerProperties(kafkaClusterName);

                        try (KafkaConsumer<String, String> consumer = new KafkaConsumer<>(props)) {
                            consumer.subscribe(Collections.singletonList(runResources.topicName()));

                            Instant start = Instant.now();
                            while (Duration.between(start, Instant.now()).getSeconds() < 90) {
                                ConsumerRecords<String, String> records = consumer.poll(Duration.ofMillis(1000));
                                for (ConsumerRecord<String, String> record : records) {
                                    if (record.value() != null) {
                                        LOG.info("Received Kafka Record: " + record.value());
                                        if (record.value().contains(uuidStr)) {
                                            foundInKafka = true;
                                            break;
                                        }
                                    }
                                }
                                if (foundInKafka) break;
                            }
                        }

                        if (!foundInKafka) {
                            throw new IllegalStateException("CDC Event not found in Kafka topic after 90 seconds");
                        }
                        LOG.info("CDC Event successfully verified in Kafka!");
                        phaseDurations.put(
                                "KAFKA_VERIFY",
                                Duration.between(phaseStart, Instant.now()).toMillis());

                        // ── Phase: SINK_VERIFY ───────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("SINK_VERIFY", phaseDurations);
                        phaseStart = Instant.now();
                        boolean foundInDb = false;
                        try (Connection conn = DriverManager.getConnection(jdbcUrl, "postgres", dbPassword)) {
                            // Use parameterized query to prevent SQL injection
                            try (PreparedStatement ps = conn.prepareStatement(
                                    "SELECT test_uuid FROM " + runResources.sinkTable() + " WHERE test_uuid = ?")) {
                                ps.setString(1, uuidStr);

                                Instant start = Instant.now();
                                while (Duration.between(start, Instant.now()).getSeconds() < 90) {
                                    try (ResultSet rs = ps.executeQuery()) {
                                        if (rs.next()) {
                                            foundInDb = true;
                                            break;
                                        }
                                    }
                                    Thread.sleep(2000);
                                }
                            }
                        }

                        if (!foundInDb) {
                            throw new IllegalStateException("CDC Event not found in Sink table after 90 seconds");
                        }

                        LOG.info("CDC Event successfully verified in Sink table!");
                        phaseDurations.put(
                                "SINK_VERIFY",
                                Duration.between(phaseStart, Instant.now()).toMillis());
                        endState = TestResult.TaskStatus.DONE;
                        recordsProcessed = 1;

                    } catch (Exception e) {
                        LOG.error("Integration CDC test failed", e);
                        endState = TestResult.TaskStatus.FAILED;
                        errorMsg = e.getMessage();
                    } finally {
                        // ── Phase: CLEANUP ───────────────────────────────────────────
                        if (progressCallback != null) progressCallback.accept("CLEANUP", phaseDurations);
                        Instant cleanupStart = Instant.now();
                        cleanupTestResources(connectNamespace, testDbNS, topicNamespacesForCleanup, runResources);
                        phaseDurations.put(
                                "CLEANUP",
                                Duration.between(cleanupStart, Instant.now()).toMillis());
                    }

                    BenchmarkStatus.Builder builder = BenchmarkStatus.builder(endState)
                            .recordsProcessed(recordsProcessed)
                            .phaseDurations(phaseDurations);
                    if (errorMsg != null) {
                        builder.error(errorMsg);
                    }
                    return builder.build();
                },
                executor.get());
    }

    /**
     * Cleans up all test resources: connectors, topics, and database tables.
     * Each cleanup step is isolated so a failure in one does not prevent others.
     */
    private void cleanupTestResources(
            String connectNamespace, String testDbNS, List<String> topicNamespaces, CdcRunResources runResources) {
        LOG.info("Cleaning up Integration CDC test resources");
        try {
            kubernetesClient
                    .genericKubernetesResources(CONNECTOR_CRD)
                    .inNamespace(connectNamespace)
                    .withName(runResources.sourceConnectorName())
                    .delete();
        } catch (Exception e) {
            LOG.warn("Failed to delete source connector", e);
        }
        try {
            kubernetesClient
                    .genericKubernetesResources(CONNECTOR_CRD)
                    .inNamespace(connectNamespace)
                    .withName(runResources.sinkConnectorName())
                    .delete();
        } catch (Exception e) {
            LOG.warn("Failed to delete sink connector", e);
        }
        LinkedHashSet<String> topicNamespacesToCleanup = new LinkedHashSet<>();
        if (topicNamespaces != null) {
            for (String ns : topicNamespaces) {
                if (ns != null && !ns.isBlank()) {
                    topicNamespacesToCleanup.add(ns.trim());
                }
            }
        }
        if (topicNamespacesToCleanup.isEmpty()) {
            topicNamespacesToCleanup.add(connectNamespace);
        }
        for (String topicNamespace : topicNamespacesToCleanup) {
            try {
                kubernetesClient
                        .genericKubernetesResources(TOPIC_CRD)
                        .inNamespace(topicNamespace)
                        .withName(runResources.topicResourceName())
                        .delete();
            } catch (Exception e) {
                LOG.warn("Failed to delete CDC KafkaTopic from namespace " + topicNamespace, e);
            }
        }

        try {
            Secret secret = kubernetesClient
                    .secrets()
                    .inNamespace(testDbNS)
                    .withName("postgresql")
                    .get();
            if (secret != null) {
                String dbPassword = new String(
                        java.util.Base64.getDecoder().decode(secret.getData().get("postgres-password")));
                String jdbcUrl = "jdbc:postgresql://postgresql." + testDbNS + ".svc:5432/postgres";
                try (Connection conn = DriverManager.getConnection(jdbcUrl, "postgres", dbPassword);
                        Statement stmt = conn.createStatement()) {
                    stmt.execute("DROP TABLE IF EXISTS " + runResources.sourceTable());
                    stmt.execute("DROP TABLE IF EXISTS " + runResources.sinkTable());
                }
            }
        } catch (Exception e) {
            LOG.warn("Failed to drop test tables", e);
        }
    }

    static CdcRunResources createRunResources(String tokenSeed) {
        String runToken = sanitizeRunToken(tokenSeed);
        String sourceTable = buildName(SOURCE_TABLE_PREFIX, runToken, POSTGRES_IDENTIFIER_MAX_LENGTH);
        return new CdcRunResources(
                runToken,
                buildName(SOURCE_CONNECTOR_PREFIX, runToken, K8S_NAME_MAX_LENGTH),
                buildName(SINK_CONNECTOR_PREFIX, runToken, K8S_NAME_MAX_LENGTH),
                sourceTable,
                buildName(SINK_TABLE_PREFIX, runToken, POSTGRES_IDENTIFIER_MAX_LENGTH),
                buildName(SLOT_PREFIX, runToken, POSTGRES_IDENTIFIER_MAX_LENGTH),
                TOPIC_NAME_PREFIX + sourceTable,
                buildName(TOPIC_RESOURCE_PREFIX, runToken, K8S_NAME_MAX_LENGTH));
    }

    private static String sanitizeRunToken(String tokenSeed) {
        StringBuilder sanitized = new StringBuilder(RUN_TOKEN_MAX_LENGTH);
        String source = tokenSeed != null ? tokenSeed : "";
        for (int i = 0; i < source.length() && sanitized.length() < RUN_TOKEN_MAX_LENGTH; i++) {
            char c = Character.toLowerCase(source.charAt(i));
            if ((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
                sanitized.append(c);
            }
        }

        if (sanitized.length() > 0) {
            return sanitized.toString();
        }
        return UUID.randomUUID().toString().replace("-", "");
    }

    private static String buildName(String prefix, String token, int maxLength) {
        if (prefix.length() + token.length() <= maxLength) {
            return prefix + token;
        }
        int remaining = Math.max(1, maxLength - prefix.length());
        return prefix + token.substring(0, remaining);
    }

    static record CdcRunResources(
            String runToken,
            String sourceConnectorName,
            String sinkConnectorName,
            String sourceTable,
            String sinkTable,
            String slotName,
            String topicName,
            String topicResourceName) {}

    private List<String> resolveTopicNamespaceCandidates(String connectNamespace) {
        String configuredNamespace = configuredCdcTopicNamespace
                .map(String::trim)
                .filter(value -> !value.isEmpty())
                .orElse("");
        if (!configuredNamespace.isEmpty()) {
            LOG.infof("Using configured CDC topic namespace: %s", configuredNamespace);
            return List.of(configuredNamespace);
        }

        LinkedHashSet<String> candidates = new LinkedHashSet<>();
        if (connectNamespace != null && !connectNamespace.isBlank()) {
            candidates.add(connectNamespace.trim());
        }
        if (kafkaNamespace != null && !kafkaNamespace.isBlank()) {
            candidates.add(kafkaNamespace.trim());
        }
        if (candidates.isEmpty()) {
            candidates.add("kafka");
        }
        return new ArrayList<>(candidates);
    }

    private boolean waitForTopicReady(String namespace, String topicResourceName, Duration timeout)
            throws InterruptedException {
        Instant deadline = Instant.now().plus(timeout);
        while (Instant.now().isBefore(deadline)) {
            GenericKubernetesResource topicCr = kubernetesClient
                    .genericKubernetesResources(TOPIC_CRD)
                    .inNamespace(namespace)
                    .withName(topicResourceName)
                    .get();
            if (isTopicReady(topicCr)) {
                return true;
            }
            Thread.sleep(1000);
        }
        return false;
    }

    @SuppressWarnings("unchecked")
    private boolean isTopicReady(GenericKubernetesResource cr) {
        if (cr != null && cr.getAdditionalProperties().containsKey("status")) {
            java.util.Map<String, Object> crStatus =
                    (java.util.Map<String, Object>) cr.getAdditionalProperties().get("status");
            if (crStatus.containsKey("conditions")) {
                java.util.List<java.util.Map<String, Object>> conditions =
                        (java.util.List<java.util.Map<String, Object>>) crStatus.get("conditions");
                for (java.util.Map<String, Object> cond : conditions) {
                    if ("Ready".equals(cond.get("type")) && "True".equals(cond.get("status"))) {
                        return true;
                    }
                }
            }
        }
        return false;
    }

    @SuppressWarnings("unchecked")
    private boolean isConnectorReady(GenericKubernetesResource cr) {
        if (cr != null && cr.getAdditionalProperties().containsKey("status")) {
            java.util.Map<String, Object> crStatus =
                    (java.util.Map<String, Object>) cr.getAdditionalProperties().get("status");
            if (crStatus.containsKey("conditions")) {
                java.util.List<java.util.Map<String, Object>> conditions =
                        (java.util.List<java.util.Map<String, Object>>) crStatus.get("conditions");
                for (java.util.Map<String, Object> cond : conditions) {
                    if ("Ready".equals(cond.get("type")) && "True".equals(cond.get("status"))) {
                        return true;
                    }
                }
            }
        }
        return false;
    }

    /**
     * Extracts connector failure reason from status conditions.
     * Returns null if the connector is not in a permanent failure state.
     */
    @SuppressWarnings("unchecked")
    private String extractConnectorFailure(GenericKubernetesResource cr) {
        if (cr == null || !cr.getAdditionalProperties().containsKey("status")) {
            return null;
        }
        java.util.Map<String, Object> crStatus =
                (java.util.Map<String, Object>) cr.getAdditionalProperties().get("status");
        if (!crStatus.containsKey("conditions")) {
            return null;
        }
        java.util.List<java.util.Map<String, Object>> conditions =
                (java.util.List<java.util.Map<String, Object>>) crStatus.get("conditions");
        for (java.util.Map<String, Object> cond : conditions) {
            if ("Ready".equals(cond.get("type")) && "False".equals(cond.get("status"))) {
                String reason = (String) cond.get("reason");
                String message = (String) cond.get("message");
                // Only treat certain reasons as permanent failures
                if (reason != null && reason.contains("Error")) {
                    return reason + (message != null ? ": " + message : "");
                }
            }
        }
        return null;
    }

    private Properties getKafkaConsumerProperties(String clusterName) {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, clusterName + "-kafka-bootstrap.kafka.svc:9092");
        props.put(
                ConsumerConfig.GROUP_ID_CONFIG,
                "integration-cdc-test-" + UUID.randomUUID().toString());
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");

        if (kafkaPassword.isPresent() && !kafkaPassword.get().isEmpty()) {
            props.put("security.protocol", "SASL_PLAINTEXT");
            props.put("sasl.mechanism", "SCRAM-SHA-512");
            String jaasTemplate =
                    "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"%s\" password=\"%s\";";
            props.put(
                    "sasl.jaas.config",
                    String.format(jaasTemplate, kafkaUsername.orElse("kates-backend"), kafkaPassword.get()));
        }

        return props;
    }
}
