package com.bmscomp.kates.service;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.common.acl.AclBinding;
import org.apache.kafka.common.acl.AclBindingFilter;
import org.apache.kafka.common.acl.AclOperation;
import org.apache.kafka.common.acl.AclPermissionType;
import org.apache.kafka.common.config.ConfigResource;
import org.apache.kafka.common.resource.PatternType;
import org.apache.kafka.common.resource.ResourceType;
import org.eclipse.microprofile.faulttolerance.Retry;
import org.eclipse.microprofile.faulttolerance.Timeout;
import org.jboss.logging.Logger;

@ApplicationScoped
public class SecurityService {

    private static final Logger LOG = Logger.getLogger(SecurityService.class);
    private static final int TIMEOUT_SECONDS = 30;

    private final KafkaAdminService adminService;
    private final ClusterHealthService clusterHealthService;

    private volatile Map<String, Object> savedBaseline;
    private volatile String baselineTimestamp;

    @Inject
    public SecurityService(KafkaAdminService adminService, ClusterHealthService clusterHealthService) {
        this.adminService = adminService;
        this.clusterHealthService = clusterHealthService;
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> securityAudit() {
        AdminClient client = adminService.getClient();
        Map<String, Object> report = new LinkedHashMap<>();
        List<Map<String, Object>> checks = new ArrayList<>();
        int passed = 0;
        int warnings = 0;
        int failures = 0;

        try {
            var clusterInfo = clusterHealthService.describeCluster();
            int brokerCount = (int) clusterInfo.getOrDefault("brokerCount", 0);

            @SuppressWarnings("unchecked")
            List<Map<String, Object>> brokers = (List<Map<String, Object>>) clusterInfo.getOrDefault("brokers", List.of());
            int brokerId = brokers.isEmpty() ? 0 : (int) brokers.get(0).get("id");

            Map<String, String> brokerConfig = fetchBrokerConfig(client, brokerId);
            List<AclBinding> acls = fetchAcls(client);

            // 1. SASL/Authentication check
            String saslEnabled = brokerConfig.getOrDefault("sasl.enabled.mechanisms", "");
            boolean hasSasl = !saslEnabled.isEmpty();
            checks.add(check("SASL Authentication", "auth",
                    hasSasl ? "PASS" : "FAIL",
                    hasSasl ? "SASL mechanisms: " + saslEnabled : "No SASL mechanisms configured — any client can connect",
                    "HIGH", "CIS-4.1",
                    "Set listener.security.protocol.map and sasl.enabled.mechanisms in broker config"));

            // 2. Plaintext listener detection
            String listeners = brokerConfig.getOrDefault("listeners", "");
            String advertisedListeners = brokerConfig.getOrDefault("advertised.listeners", "");
            boolean hasPlaintext = listeners.contains("PLAINTEXT://") || advertisedListeners.contains("PLAINTEXT://");
            checks.add(check("No Plaintext Listeners", "transport",
                    hasPlaintext ? "WARN" : "PASS",
                    hasPlaintext ? "Plaintext listeners detected — credentials sent unencrypted" : "All listeners use TLS or SASL",
                    "HIGH", "CIS-4.2",
                    "Change listener protocol to SASL_SSL in kafka.listeners configuration"));

            // 3. ACL authorization active
            String authorizerClass = brokerConfig.getOrDefault("authorizer.class.name", "");
            boolean hasAuthorizer = !authorizerClass.isEmpty()
                    && !authorizerClass.equals("org.apache.kafka.metadata.authorizer.StandardAuthorizer")
                    || !authorizerClass.isEmpty();
            checks.add(check("ACL Authorization", "authz",
                    hasAuthorizer ? "PASS" : "FAIL",
                    hasAuthorizer ? "Authorizer: " + authorizerClass : "No authorizer configured — all authenticated users have full access",
                    "HIGH", "CIS-5.1",
                    "Set kafka.authorization.type: simple in values.yaml"));

            // 4. ACL count
            int aclCount = acls.size();
            checks.add(check("ACL Rules Defined", "authz",
                    aclCount > 0 ? "PASS" : "WARN",
                    aclCount > 0 ? aclCount + " ACL rules active" : "No ACL rules — all operations allowed",
                    "MEDIUM", "CIS-5.2",
                    "Define KafkaUser resources with acls in values.yaml users section"));

            // 5. Wildcard ACL detection
            long wildcardAllow = acls.stream()
                    .filter(a -> a.entry().permissionType() == AclPermissionType.ALLOW
                            && a.pattern().patternType() == PatternType.LITERAL
                            && a.pattern().name().equals("*")
                            && a.pattern().resourceType() == ResourceType.TOPIC)
                    .count();
            checks.add(check("No Wildcard Topic ACLs", "authz",
                    wildcardAllow == 0 ? "PASS" : "WARN",
                    wildcardAllow == 0 ? "No wildcard ALLOW on all topics" : wildcardAllow + " wildcard ALLOW rules — overly permissive",
                    "MEDIUM", "CIS-5.3",
                    "Replace wildcard ACLs with topic-specific ALLOW rules"));

            // 6. Unclean leader election
            String unclean = brokerConfig.getOrDefault("unclean.leader.election.enable", "false");
            checks.add(check("Unclean Election Disabled", "config",
                    "false".equals(unclean) ? "PASS" : "FAIL",
                    "false".equals(unclean) ? "Unclean leader election disabled — data integrity protected"
                            : "DANGER: unclean election enabled — out-of-sync replica can become leader, causing data loss",
                    "CRITICAL", "CIS-3.1",
                    "Set unclean.leader.election.enable: false in kafka.config"));

            // 7. Auto-create topics
            String autoCreate = brokerConfig.getOrDefault("auto.create.topics.enable", "true");
            checks.add(check("Auto-Create Topics Disabled", "config",
                    "false".equals(autoCreate) ? "PASS" : "WARN",
                    "false".equals(autoCreate) ? "Topic auto-creation disabled"
                            : "Topic auto-creation enabled — any producer can create unlimited topics",
                    "MEDIUM", "CIS-3.2",
                    "Set auto.create.topics.enable: false in kafka.config"));

            // 8. Message size bounded
            String maxBytes = brokerConfig.getOrDefault("message.max.bytes", "1048588");
            long maxBytesVal = Long.parseLong(maxBytes);
            boolean bounded = maxBytesVal <= 20_971_520; // 20MB
            checks.add(check("Message Size Bounded", "config",
                    bounded ? "PASS" : "WARN",
                    "message.max.bytes = " + formatBytes(maxBytesVal) + (bounded ? "" : " — large messages can cause broker OOM"),
                    "LOW", "CIS-3.3",
                    "Set message.max.bytes to ≤20MB in kafka.config"));

            // 9. Min ISR >= 2
            String minIsr = brokerConfig.getOrDefault("min.insync.replicas", "1");
            int minIsrVal = Integer.parseInt(minIsr);
            checks.add(check("Min ISR ≥ 2", "durability",
                    minIsrVal >= 2 ? "PASS" : "WARN",
                    "min.insync.replicas = " + minIsrVal + (minIsrVal >= 2 ? "" : " — single-replica ack allows data loss"),
                    "HIGH", "CIS-3.4",
                    "Set min.insync.replicas: 2 in kafka.config"));

            // 10. Replication factor >= 3
            String defaultRf = brokerConfig.getOrDefault("default.replication.factor", "1");
            int rfVal = Integer.parseInt(defaultRf);
            checks.add(check("Replication Factor ≥ 3", "durability",
                    rfVal >= 3 ? "PASS" : "WARN",
                    "default.replication.factor = " + rfVal + (rfVal >= 3 ? "" : " — insufficient redundancy"),
                    "HIGH", "CIS-3.5",
                    "Set default.replication.factor: 3 in kafka.config"));

            // 11. Inter-broker protocol security
            String interBrokerProtocol = brokerConfig.getOrDefault("security.inter.broker.protocol", "");
            boolean secureInterBroker = interBrokerProtocol.contains("SSL") || interBrokerProtocol.contains("SASL");
            checks.add(check("Inter-Broker Encryption", "transport",
                    secureInterBroker || interBrokerProtocol.isEmpty() ? "PASS" : "WARN",
                    secureInterBroker ? "Inter-broker protocol: " + interBrokerProtocol
                            : interBrokerProtocol.isEmpty()
                                    ? "Using default inter-broker protocol (KRaft manages internally)"
                                    : "Inter-broker traffic is unencrypted: " + interBrokerProtocol,
                    "HIGH", "CIS-4.3",
                    "Set security.inter.broker.protocol: SASL_SSL in kafka.config"));

            // 12. Log cleaner compaction for sensitive topics
            String cleanupPolicy = brokerConfig.getOrDefault("log.cleanup.policy", "delete");
            checks.add(check("Default Cleanup Policy", "config",
                    "PASS",
                    "log.cleanup.policy = " + cleanupPolicy,
                    "LOW", "CIS-3.6",
                    "Use log.cleanup.policy=compact for changelog topics"));

            // 13. Connection rate limits
            String maxConnRate = brokerConfig.getOrDefault("max.connection.creation.rate", "");
            String maxConns = brokerConfig.getOrDefault("max.connections", "");
            boolean hasConnLimits = !maxConnRate.isEmpty() || !maxConns.isEmpty();
            checks.add(check("Connection Rate Limiting", "dos",
                    hasConnLimits ? "PASS" : "WARN",
                    hasConnLimits ? "Connection limits configured" : "No connection rate limits — vulnerable to connection flood",
                    "MEDIUM", "CIS-6.1",
                    "Set max.connections and max.connection.creation.rate per listener"));

            // 14. Quota enforcement
            String producerQuota = brokerConfig.getOrDefault("quota.producer.default", "");
            String consumerQuota = brokerConfig.getOrDefault("quota.consumer.default", "");
            boolean hasQuotas = !producerQuota.isEmpty() || !consumerQuota.isEmpty();
            checks.add(check("Default Quotas Set", "limits",
                    hasQuotas ? "PASS" : "WARN",
                    hasQuotas
                            ? "Producer: " + (producerQuota.isEmpty() ? "unlimited" : formatBytes(Long.parseLong(producerQuota)) + "/s")
                                    + ", Consumer: " + (consumerQuota.isEmpty() ? "unlimited"
                                            : formatBytes(Long.parseLong(consumerQuota)) + "/s")
                            : "No default quotas — one client can monopolize cluster bandwidth",
                    "MEDIUM", "CIS-6.2",
                    "Set quotas in KafkaUser spec: producerByteRate and consumerByteRate"));

            // 15. SSL protocol version
            String sslProtocol = brokerConfig.getOrDefault("ssl.protocol", "TLSv1.3");
            boolean modernTls = sslProtocol.contains("1.2") || sslProtocol.contains("1.3");
            checks.add(check("TLS Protocol Version", "transport",
                    modernTls ? "PASS" : "WARN",
                    "ssl.protocol = " + sslProtocol + (modernTls ? "" : " — upgrade to TLSv1.2 or TLSv1.3"),
                    "HIGH", "CIS-4.4",
                    "Set ssl.protocol: TLSv1.3 in kafka.config"));

            for (Map<String, Object> c : checks) {
                String status = (String) c.get("status");
                if ("PASS".equals(status)) passed++;
                else if ("WARN".equals(status)) warnings++;
                else failures++;
            }

            report.put("checks", checks);
            report.put("summary", Map.of(
                    "total", checks.size(),
                    "passed", passed,
                    "warnings", warnings,
                    "failures", failures));
            report.put("grade", computeGrade(passed, warnings, failures, checks.size()));
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Security audit failed", e);
            report.put("error", "Security audit failed: " + e.getMessage());
            report.put("grade", "F");
        }

        return report;
    }

    public Map<String, Object> securityCompliance() {
        Map<String, Object> audit = securityAudit();
        Map<String, Object> report = new LinkedHashMap<>();

        @SuppressWarnings("unchecked")
        List<Map<String, Object>> checks = (List<Map<String, Object>>) audit.getOrDefault("checks", List.of());

        Map<String, List<Map<String, Object>>> byFramework = new LinkedHashMap<>();
        byFramework.put("CIS Kafka Benchmark", new ArrayList<>());
        byFramework.put("SOC2 Type II", new ArrayList<>());
        byFramework.put("PCI-DSS v4.0", new ArrayList<>());

        for (Map<String, Object> check : checks) {
            String cis = (String) check.getOrDefault("compliance", "");
            String category = (String) check.getOrDefault("category", "");
            String status = (String) check.getOrDefault("status", "");

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("check", check.get("name"));
            entry.put("status", status);
            entry.put("detail", check.get("detail"));
            entry.put("fix", check.get("fix"));

            if (!cis.isEmpty()) {
                entry.put("controlId", cis);
                byFramework.get("CIS Kafka Benchmark").add(entry);
            }

            if ("auth".equals(category) || "authz".equals(category) || "transport".equals(category)) {
                Map<String, Object> soc2Entry = new LinkedHashMap<>(entry);
                soc2Entry.put("controlId", mapToSoc2(category));
                byFramework.get("SOC2 Type II").add(soc2Entry);
            }

            if ("transport".equals(category) || "auth".equals(category) || "limits".equals(category)) {
                Map<String, Object> pciEntry = new LinkedHashMap<>(entry);
                pciEntry.put("controlId", mapToPci(category));
                byFramework.get("PCI-DSS v4.0").add(pciEntry);
            }
        }

        for (var frameworkEntry : byFramework.entrySet()) {
            List<Map<String, Object>> frameworkChecks = frameworkEntry.getValue();
            long passed = frameworkChecks.stream().filter(c -> "PASS".equals(c.get("status"))).count();
            Map<String, Object> frameworkReport = new LinkedHashMap<>();
            frameworkReport.put("controls", frameworkChecks);
            frameworkReport.put("total", frameworkChecks.size());
            frameworkReport.put("passed", passed);
            frameworkReport.put("compliance",
                    frameworkChecks.isEmpty() ? "N/A" : String.format("%.0f%%", (passed * 100.0) / frameworkChecks.size()));
            report.put(frameworkEntry.getKey(), frameworkReport);
        }

        report.put("grade", audit.get("grade"));
        report.put("timestamp", Instant.now().toString());
        return report;
    }

    public Map<String, Object> saveBaseline() {
        Map<String, Object> audit = securityAudit();
        savedBaseline = audit;
        baselineTimestamp = Instant.now().toString();
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("status", "saved");
        result.put("grade", audit.get("grade"));
        result.put("checks", ((List<?>) audit.getOrDefault("checks", List.of())).size());
        result.put("timestamp", baselineTimestamp);
        return result;
    }

    public Map<String, Object> securityDrift() {
        if (savedBaseline == null) {
            return Map.of("error", "No baseline saved. Run 'kates security baseline --save' first.",
                    "hasBaseline", false);
        }

        Map<String, Object> current = securityAudit();
        Map<String, Object> report = new LinkedHashMap<>();

        @SuppressWarnings("unchecked")
        List<Map<String, Object>> baselineChecks = (List<Map<String, Object>>) savedBaseline.getOrDefault("checks", List.of());
        @SuppressWarnings("unchecked")
        List<Map<String, Object>> currentChecks = (List<Map<String, Object>>) current.getOrDefault("checks", List.of());

        Map<String, String> baselineMap = new LinkedHashMap<>();
        for (Map<String, Object> c : baselineChecks) {
            baselineMap.put((String) c.get("name"), (String) c.get("status"));
        }

        List<Map<String, Object>> drifts = new ArrayList<>();
        int improved = 0;
        int degraded = 0;
        int unchanged = 0;

        for (Map<String, Object> c : currentChecks) {
            String name = (String) c.get("name");
            String currentStatus = (String) c.get("status");
            String baselineStatus = baselineMap.getOrDefault(name, "UNKNOWN");

            String change;
            if (currentStatus.equals(baselineStatus)) {
                change = "UNCHANGED";
                unchanged++;
            } else if (statusRank(currentStatus) > statusRank(baselineStatus)) {
                change = "IMPROVED";
                improved++;
            } else {
                change = "DEGRADED";
                degraded++;
            }

            Map<String, Object> drift = new LinkedHashMap<>();
            drift.put("check", name);
            drift.put("baseline", baselineStatus);
            drift.put("current", currentStatus);
            drift.put("change", change);
            if (!"UNCHANGED".equals(change)) {
                drift.put("detail", c.get("detail"));
                drift.put("fix", c.get("fix"));
            }
            drifts.add(drift);
        }

        report.put("hasBaseline", true);
        report.put("baselineTimestamp", baselineTimestamp);
        report.put("baselineGrade", savedBaseline.get("grade"));
        report.put("currentGrade", current.get("grade"));
        report.put("drifts", drifts);
        report.put("summary", Map.of(
                "improved", improved,
                "degraded", degraded,
                "unchanged", unchanged,
                "total", drifts.size()));
        report.put("timestamp", Instant.now().toString());
        return report;
    }

    public Map<String, Object> securityGate(String minGrade) {
        Map<String, Object> audit = securityAudit();
        String currentGrade = (String) audit.getOrDefault("grade", "F");

        boolean passed = gradeRank(currentGrade) >= gradeRank(minGrade);

        Map<String, Object> result = new LinkedHashMap<>();
        result.put("passed", passed);
        result.put("currentGrade", currentGrade);
        result.put("requiredGrade", minGrade);
        result.put("summary", audit.get("summary"));

        if (!passed) {
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> checks = (List<Map<String, Object>>) audit.getOrDefault("checks", List.of());
            List<Map<String, Object>> failingChecks = checks.stream()
                    .filter(c -> !"PASS".equals(c.get("status")))
                    .map(c -> {
                        Map<String, Object> f = new LinkedHashMap<>();
                        f.put("check", c.get("name"));
                        f.put("status", c.get("status"));
                        f.put("fix", c.get("fix"));
                        return f;
                    })
                    .toList();
            result.put("failingChecks", failingChecks);
        }

        result.put("timestamp", Instant.now().toString());
        return result;
    }

    private int statusRank(String status) {
        return switch (status) {
            case "FAIL" -> 0;
            case "WARN" -> 1;
            case "PASS" -> 2;
            default -> -1;
        };
    }

    private int gradeRank(String grade) {
        return switch (grade) {
            case "A" -> 5;
            case "B" -> 4;
            case "C" -> 3;
            case "D" -> 2;
            case "F" -> 1;
            default -> 0;
        };
    }

    private String mapToSoc2(String category) {
        return switch (category) {
            case "auth" -> "CC6.1";
            case "authz" -> "CC6.3";
            case "transport" -> "CC6.7";
            default -> "CC6.0";
        };
    }

    private String mapToPci(String category) {
        return switch (category) {
            case "transport" -> "Req-4.1";
            case "auth" -> "Req-8.3";
            case "limits" -> "Req-6.5";
            default -> "Req-2.2";
        };
    }

    @Timeout(30_000)
    public Map<String, Object> tlsInspect() {
        AdminClient client = adminService.getClient();
        Map<String, Object> report = new LinkedHashMap<>();
        List<Map<String, Object>> checks = new ArrayList<>();

        try {
            var clusterInfo = clusterHealthService.describeCluster();
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> brokers = (List<Map<String, Object>>) clusterInfo.getOrDefault("brokers", List.of());
            int brokerId = brokers.isEmpty() ? 0 : (int) brokers.get(0).get("id");

            Map<String, String> config = fetchBrokerConfig(client, brokerId);

            String sslProtocol = config.getOrDefault("ssl.protocol", "TLSv1.3");
            checks.add(check("TLS Protocol", "tls",
                    sslProtocol.contains("1.2") || sslProtocol.contains("1.3") ? "PASS" : "FAIL",
                    "ssl.protocol = " + sslProtocol,
                    "HIGH", "CIS-4.4",
                    "Set ssl.protocol: TLSv1.3 in kafka.config"));

            String keystoreType = config.getOrDefault("ssl.keystore.type", "PKCS12");
            checks.add(check("Keystore Type", "tls", "PASS",
                    "ssl.keystore.type = " + keystoreType, "LOW", "CIS-4.5",
                    "Use PKCS12 keystore format"));

            String truststoreType = config.getOrDefault("ssl.truststore.type", "PKCS12");
            checks.add(check("Truststore Type", "tls", "PASS",
                    "ssl.truststore.type = " + truststoreType, "LOW", "CIS-4.5",
                    "Use PKCS12 truststore format"));

            String clientAuth = config.getOrDefault("ssl.client.auth", "none");
            checks.add(check("mTLS Client Auth", "tls",
                    "required".equals(clientAuth) ? "PASS" : "WARN",
                    "ssl.client.auth = " + clientAuth
                            + ("required".equals(clientAuth) ? "" : " — consider enabling mTLS for mutual authentication"),
                    "MEDIUM", "CIS-4.6",
                    "Set ssl.client.auth: required in kafka.config for mutual TLS"));

            String endpointId = config.getOrDefault("ssl.endpoint.identification.algorithm", "https");
            checks.add(check("Hostname Verification", "tls",
                    "https".equals(endpointId) || !endpointId.isEmpty() ? "PASS" : "FAIL",
                    "ssl.endpoint.identification.algorithm = "
                            + (endpointId.isEmpty() ? "<empty> — MITM vulnerable" : endpointId),
                    "CRITICAL", "CIS-4.7",
                    "Set ssl.endpoint.identification.algorithm: https"));

            String enabledProtocols = config.getOrDefault("ssl.enabled.protocols", "TLSv1.2,TLSv1.3");
            boolean hasLegacy = enabledProtocols.contains("TLSv1,") || enabledProtocols.contains("SSLv3")
                    || enabledProtocols.contains("TLSv1.1");
            checks.add(check("No Legacy Protocols", "tls",
                    hasLegacy ? "FAIL" : "PASS",
                    "ssl.enabled.protocols = " + enabledProtocols
                            + (hasLegacy ? " — DISABLE SSLv3/TLSv1.0/TLSv1.1" : ""),
                    "HIGH", "CIS-4.8",
                    "Set ssl.enabled.protocols: TLSv1.2,TLSv1.3"));

            String cipherSuites = config.getOrDefault("ssl.cipher.suites", "");
            boolean hasWeakCiphers = cipherSuites.contains("RC4") || cipherSuites.contains("DES")
                    || cipherSuites.contains("EXPORT") || cipherSuites.contains("NULL");
            checks.add(check("No Weak Ciphers", "tls",
                    cipherSuites.isEmpty() || !hasWeakCiphers ? "PASS" : "FAIL",
                    cipherSuites.isEmpty() ? "Using JVM default cipher suites (secure)"
                            : hasWeakCiphers ? "Weak ciphers detected: " + cipherSuites : "ssl.cipher.suites = " + cipherSuites,
                    "HIGH", "CIS-4.9",
                    "Remove RC4, DES, EXPORT, NULL from ssl.cipher.suites"));

            report.put("checks", checks);
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("TLS inspection failed", e);
            report.put("error", "TLS inspection failed: " + e.getMessage());
        }

        return report;
    }

    @Timeout(60_000)
    public Map<String, Object> authTest(String username) {
        AdminClient client = adminService.getClient();
        Map<String, Object> report = new LinkedHashMap<>();
        List<Map<String, Object>> checks = new ArrayList<>();

        try {
            List<AclBinding> acls = fetchAcls(client);

            long userAcls = acls.stream()
                    .filter(a -> a.entry().principal().equals("User:" + username))
                    .count();

            checks.add(check("User ACLs Exist", "authz",
                    userAcls > 0 ? "PASS" : "WARN",
                    userAcls > 0 ? userAcls + " ACL rules for User:" + username
                            : "No ACL rules for User:" + username + " — may be a superUser or have no access",
                    "MEDIUM", "CIS-5.2",
                    "Create KafkaUser CR with explicit acls for this user"));

            boolean canProduceAll = acls.stream().anyMatch(a -> a.entry().principal().equals("User:" + username)
                    && a.entry().operation() == AclOperation.WRITE
                    && a.pattern().name().equals("*")
                    && a.pattern().resourceType() == ResourceType.TOPIC);
            checks.add(check("No Wildcard Write", "authz",
                    canProduceAll ? "WARN" : "PASS",
                    canProduceAll ? "User can write to ALL topics — overly broad"
                            : "Write access is scoped to specific topics",
                    "MEDIUM", "CIS-5.3",
                    "Replace wildcard Write ACL with topic-specific rules"));

            boolean canDeleteTopics = acls.stream().anyMatch(a -> a.entry().principal().equals("User:" + username)
                    && a.entry().operation() == AclOperation.DELETE
                    && a.pattern().resourceType() == ResourceType.TOPIC);
            checks.add(check("No Topic Delete Permission", "authz",
                    canDeleteTopics ? "WARN" : "PASS",
                    canDeleteTopics ? "User can delete topics — verify this is intended"
                            : "User cannot delete topics",
                    "HIGH", "CIS-5.4",
                    "Remove Delete ACL unless user is designated topic admin"));

            boolean canAlterCluster = acls.stream().anyMatch(a -> a.entry().principal().equals("User:" + username)
                    && a.entry().operation() == AclOperation.ALTER
                    && a.pattern().resourceType() == ResourceType.CLUSTER);
            checks.add(check("No Cluster Alter Permission", "authz",
                    canAlterCluster ? "FAIL" : "PASS",
                    canAlterCluster ? "DANGER: User can alter cluster configuration"
                            : "User cannot alter cluster configuration",
                    "CRITICAL", "CIS-5.5",
                    "Remove Alter CLUSTER ACL — this should only be granted to operator service accounts"));

            List<Map<String, String>> aclDetails = acls.stream()
                    .filter(a -> a.entry().principal().equals("User:" + username))
                    .map(a -> {
                        Map<String, String> detail = new LinkedHashMap<>();
                        detail.put("resource", a.pattern().resourceType().toString());
                        detail.put("name", a.pattern().name());
                        detail.put("pattern", a.pattern().patternType().toString());
                        detail.put("operation", a.entry().operation().toString());
                        detail.put("permission", a.entry().permissionType().toString());
                        detail.put("host", a.entry().host());
                        return detail;
                    })
                    .toList();

            report.put("username", username);
            report.put("checks", checks);
            report.put("acls", aclDetails);
            report.put("aclCount", aclDetails.size());
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Auth test failed for user: " + username, e);
            report.put("error", "Auth test failed: " + e.getMessage());
        }

        return report;
    }

    @Timeout(60_000)
    public Map<String, Object> pentest(String testName) {
        Map<String, Object> report = new LinkedHashMap<>();
        List<Map<String, Object>> results = new ArrayList<>();

        try {
            AdminClient client = adminService.getClient();
            var clusterInfo = clusterHealthService.describeCluster();
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> brokers = (List<Map<String, Object>>) clusterInfo.getOrDefault("brokers", List.of());
            int brokerId = brokers.isEmpty() ? 0 : (int) brokers.get(0).get("id");
            Map<String, String> config = fetchBrokerConfig(client, brokerId);

            if (testName == null || testName.isEmpty() || "all".equals(testName)) {
                results.add(pentestAutoCreateTopics(config));
                results.add(pentestLargeMessage(config));
                results.add(pentestMetadataLeak(config));
                results.add(pentestConnectionLimits(config));
                results.add(pentestUnencryptedAccess(config));
                results.add(pentestAclBypass(client));
            } else {
                switch (testName) {
                    case "auto-create" -> results.add(pentestAutoCreateTopics(config));
                    case "large-message" -> results.add(pentestLargeMessage(config));
                    case "metadata-leak" -> results.add(pentestMetadataLeak(config));
                    case "connection-flood" -> results.add(pentestConnectionLimits(config));
                    case "unencrypted" -> results.add(pentestUnencryptedAccess(config));
                    case "acl-bypass" -> results.add(pentestAclBypass(client));
                    default -> {
                        report.put("error", "Unknown pentest: " + testName
                                + ". Available: auto-create, large-message, metadata-leak, connection-flood, unencrypted, acl-bypass");
                        return report;
                    }
                }
            }

            long vulnerabilities = results.stream()
                    .filter(r -> "VULNERABLE".equals(r.get("result")))
                    .count();

            report.put("tests", results);
            report.put("summary", Map.of(
                    "total", results.size(),
                    "protected", results.size() - vulnerabilities,
                    "vulnerable", vulnerabilities));
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Pentest failed", e);
            report.put("error", "Pentest failed: " + e.getMessage());
        }

        return report;
    }

    private Map<String, Object> pentestAutoCreateTopics(Map<String, String> config) {
        String autoCreate = config.getOrDefault("auto.create.topics.enable", "true");
        boolean vulnerable = "true".equals(autoCreate);
        return pentestResult("Topic Auto-Creation", "auto-create", vulnerable,
                vulnerable ? "auto.create.topics.enable=true — any producer can create topics"
                        : "auto.create.topics.enable=false — topic creation requires explicit admin action",
                "HIGH");
    }

    private Map<String, Object> pentestLargeMessage(Map<String, String> config) {
        long maxBytes = Long.parseLong(config.getOrDefault("message.max.bytes", "1048588"));
        boolean vulnerable = maxBytes > 52_428_800; // 50MB
        return pentestResult("Large Message Bomb", "large-message", vulnerable,
                "message.max.bytes = " + formatBytes(maxBytes)
                        + (vulnerable ? " — unbounded, can cause broker OOM" : " — bounded within safe limits"),
                "MEDIUM");
    }

    private Map<String, Object> pentestMetadataLeak(Map<String, String> config) {
        String sasl = config.getOrDefault("sasl.enabled.mechanisms", "");
        boolean vulnerable = sasl.isEmpty();
        return pentestResult("Unauthenticated Metadata", "metadata-leak", vulnerable,
                vulnerable ? "No SASL — metadata request succeeds without credentials"
                        : "SASL required — unauthenticated metadata requests rejected",
                "HIGH");
    }

    private Map<String, Object> pentestConnectionLimits(Map<String, String> config) {
        String maxConns = config.getOrDefault("max.connections", "");
        String maxRate = config.getOrDefault("max.connection.creation.rate", "");
        boolean vulnerable = maxConns.isEmpty() && maxRate.isEmpty();
        return pentestResult("Connection Flood", "connection-flood", vulnerable,
                vulnerable ? "No connection limits — cluster vulnerable to connection exhaustion"
                        : "Connection limits: max=" + (maxConns.isEmpty() ? "unlimited" : maxConns)
                                + ", rate=" + (maxRate.isEmpty() ? "unlimited" : maxRate + "/s"),
                "MEDIUM");
    }

    private Map<String, Object> pentestUnencryptedAccess(Map<String, String> config) {
        String listeners = config.getOrDefault("listeners", "");
        boolean vulnerable = listeners.contains("PLAINTEXT://");
        return pentestResult("Unencrypted Access", "unencrypted", vulnerable,
                vulnerable ? "PLAINTEXT listener found — traffic can be sniffed"
                        : "All listeners use encrypted transport",
                "HIGH");
    }

    private Map<String, Object> pentestAclBypass(AdminClient client) {
        try {
            List<AclBinding> acls = fetchAcls(client);
            long wildcardAllows = acls.stream()
                    .filter(a -> a.entry().permissionType() == AclPermissionType.ALLOW
                            && a.pattern().name().equals("*")
                            && a.entry().operation() == AclOperation.ALL)
                    .count();
            boolean vulnerable = wildcardAllows > 0;
            return pentestResult("ACL Bypass via Wildcard", "acl-bypass", vulnerable,
                    vulnerable ? wildcardAllows + " wildcard ALLOW ALL rules — effectively disables authorization"
                            : "No wildcard ALLOW ALL rules found",
                    "CRITICAL");
        } catch (Exception e) {
            return pentestResult("ACL Bypass via Wildcard", "acl-bypass", false,
                    "Could not inspect ACLs: " + e.getMessage(), "CRITICAL");
        }
    }

    private Map<String, String> fetchBrokerConfig(AdminClient client, int brokerId) {
        try {
            ConfigResource resource = new ConfigResource(ConfigResource.Type.BROKER, String.valueOf(brokerId));
            Config config = client.describeConfigs(Collections.singleton(resource))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(resource);

            Map<String, String> result = new LinkedHashMap<>();
            for (ConfigEntry entry : config.entries()) {
                result.put(entry.name(), entry.value());
            }
            return result;
        } catch (Exception e) {
            LOG.warn("Failed to fetch broker config for broker " + brokerId, e);
            return Map.of();
        }
    }

    private List<AclBinding> fetchAcls(AdminClient client) {
        try {
            return new ArrayList<>(client.describeAcls(AclBindingFilter.ANY)
                    .values()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS));
        } catch (Exception e) {
            LOG.warn("Failed to fetch ACLs (authorizer may not be configured)", e);
            return List.of();
        }
    }

    private Map<String, Object> check(String name, String category, String status, String detail,
            String severity, String compliance, String fix) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("name", name);
        c.put("category", category);
        c.put("status", status);
        c.put("detail", detail);
        c.put("severity", severity);
        c.put("compliance", compliance);
        c.put("fix", fix);
        return c;
    }

    private Map<String, Object> pentestResult(String name, String id, boolean vulnerable,
            String detail, String severity) {
        Map<String, Object> r = new LinkedHashMap<>();
        r.put("name", name);
        r.put("id", id);
        r.put("result", vulnerable ? "VULNERABLE" : "PROTECTED");
        r.put("detail", detail);
        r.put("severity", severity);
        return r;
    }

    private String computeGrade(int passed, int warnings, int failures, int total) {
        if (total == 0) return "F";
        if (failures == 0 && warnings == 0) return "A";
        if (failures == 0 && warnings <= 2) return "B";
        if (failures <= 1 && warnings <= 3) return "C";
        if (failures <= 2) return "D";
        return "F";
    }

    private String formatBytes(long bytes) {
        if (bytes >= 1_073_741_824) return String.format("%.1fGB", bytes / 1_073_741_824.0);
        if (bytes >= 1_048_576) return String.format("%.1fMB", bytes / 1_048_576.0);
        if (bytes >= 1_024) return String.format("%.1fKB", bytes / 1_024.0);
        return bytes + "B";
    }
}
