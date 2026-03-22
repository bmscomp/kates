package com.bmscomp.kates.service;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

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
    private final List<Map<String, Object>> scoreHistory = new CopyOnWriteArrayList<>();

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

            // 16. Super users audit
            String superUsers = brokerConfig.getOrDefault("super.users", "");
            int superUserCount = superUsers.isEmpty() ? 0 : superUsers.split(";").length;
            checks.add(check("Super Users Audited", "auth",
                    superUserCount <= 2 ? "PASS" : "WARN",
                    superUserCount == 0 ? "No super users configured"
                            : superUserCount + " super users: " + superUsers,
                    "HIGH", "CIS-5.6",
                    "Minimize super.users — use ACLs for fine-grained access instead"));

            // 17. SASL mechanism strength
            String saslMechanisms = brokerConfig.getOrDefault("sasl.enabled.mechanisms", "");
            boolean weakSasl = saslMechanisms.contains("PLAIN") && !saslMechanisms.contains("SCRAM");
            checks.add(check("SASL Mechanism Strength", "auth",
                    saslMechanisms.isEmpty() ? "WARN"
                            : weakSasl ? "WARN" : "PASS",
                    saslMechanisms.isEmpty() ? "No SASL mechanisms configured"
                            : weakSasl ? "PLAIN mechanism only — passwords sent in cleartext"
                                    : "Mechanisms: " + saslMechanisms,
                    "HIGH", "CIS-4.10",
                    "Use SCRAM-SHA-512 instead of PLAIN for password-based auth"));

            // 18. DENY rules exist (defense in depth)
            long denyRules = acls.stream()
                    .filter(a -> a.entry().permissionType() == AclPermissionType.DENY)
                    .count();
            checks.add(check("DENY Rules Defined", "authz",
                    denyRules > 0 ? "PASS" : "WARN",
                    denyRules > 0 ? denyRules + " DENY rules active — defense in depth"
                            : "No DENY rules — relying solely on ALLOW whitelist",
                    "LOW", "CIS-5.7",
                    "Add DENY rules for sensitive topics/operations as defense in depth"));

            // 19. SSL keystore configured
            String keystoreLocation = brokerConfig.getOrDefault("ssl.keystore.location", "");
            checks.add(check("SSL Keystore Configured", "transport",
                    !keystoreLocation.isEmpty() ? "PASS" : "WARN",
                    !keystoreLocation.isEmpty() ? "Keystore configured" : "No SSL keystore — TLS not available",
                    "HIGH", "CIS-4.11",
                    "Configure ssl.keystore.location with broker certificate"));

            // 20. SSL truststore configured
            String truststoreLocation = brokerConfig.getOrDefault("ssl.truststore.location", "");
            checks.add(check("SSL Truststore Configured", "transport",
                    !truststoreLocation.isEmpty() ? "PASS" : "WARN",
                    !truststoreLocation.isEmpty() ? "Truststore configured" : "No SSL truststore — cannot verify client certs",
                    "MEDIUM", "CIS-4.12",
                    "Configure ssl.truststore.location for client certificate validation"));

            // 21. mTLS client authentication
            String clientAuth = brokerConfig.getOrDefault("ssl.client.auth", "none");
            checks.add(check("mTLS Enforcement", "transport",
                    "required".equals(clientAuth) ? "PASS"
                            : "requested".equals(clientAuth) ? "WARN" : "WARN",
                    "ssl.client.auth = " + clientAuth
                            + ("required".equals(clientAuth) ? " — mutual TLS enforced"
                                    : " — clients not required to present certificates"),
                    "HIGH", "CIS-4.13",
                    "Set ssl.client.auth: required for mTLS"));

            // 22. Log retention policy
            String retentionMs = brokerConfig.getOrDefault("log.retention.ms", "");
            String retentionHours = brokerConfig.getOrDefault("log.retention.hours", "168");
            long retentionH = retentionMs.isEmpty() ? Long.parseLong(retentionHours)
                    : Long.parseLong(retentionMs) / 3_600_000;
            checks.add(check("Log Retention Configured", "config",
                    retentionH > 0 && retentionH <= 720 ? "PASS" : "WARN",
                    "Retention: " + retentionH + "h"
                            + (retentionH > 720 ? " — over 30 days may waste disk and expose stale data"
                                    : retentionH <= 0 ? " — infinite retention is risky" : ""),
                    "MEDIUM", "CIS-3.7",
                    "Set log.retention.hours between 24 and 720 (1-30 days)"));

            // 23. Controlled shutdown enabled
            String controlledShutdown = brokerConfig.getOrDefault("controlled.shutdown.enable", "true");
            checks.add(check("Controlled Shutdown", "config",
                    "true".equals(controlledShutdown) ? "PASS" : "WARN",
                    "controlled.shutdown.enable = " + controlledShutdown
                            + ("true".equals(controlledShutdown) ? "" : " — unclean shutdowns risk data loss"),
                    "MEDIUM", "CIS-3.8",
                    "Set controlled.shutdown.enable: true"));

            // 24. Delete topic enabled audit
            String deleteTopic = brokerConfig.getOrDefault("delete.topic.enable", "true");
            checks.add(check("Delete Topic Enabled", "config",
                    "true".equals(deleteTopic) ? "WARN" : "PASS",
                    "delete.topic.enable = " + deleteTopic
                            + ("true".equals(deleteTopic) ? " — authorized users can permanently delete topics" : ""),
                    "MEDIUM", "CIS-3.9",
                    "Consider delete.topic.enable: false and use ACLs to control deletion"));

            // 25. Transaction ID authorization
            String transactionalIdAuth = brokerConfig.getOrDefault("transaction.state.log.replication.factor", "1");
            int txnRf = Integer.parseInt(transactionalIdAuth);
            checks.add(check("Transaction Log Replication", "durability",
                    txnRf >= 3 ? "PASS" : "WARN",
                    "transaction.state.log.replication.factor = " + txnRf
                            + (txnRf >= 3 ? "" : " — transactional guarantees weakened"),
                    "HIGH", "CIS-3.10",
                    "Set transaction.state.log.replication.factor: 3"));

            // 26. Request rate limits
            String maxRequestSize = brokerConfig.getOrDefault("max.request.size", "");
            String socketRequestMax = brokerConfig.getOrDefault("socket.request.max.bytes", "");
            boolean hasRequestLimits = !maxRequestSize.isEmpty() || !socketRequestMax.isEmpty();
            checks.add(check("Request Size Limits", "dos",
                    hasRequestLimits ? "PASS" : "WARN",
                    hasRequestLimits
                            ? "socket.request.max.bytes = " + (socketRequestMax.isEmpty() ? "default" : formatBytes(Long.parseLong(socketRequestMax)))
                            : "Using default request size limits — consider tuning for your workload",
                    "LOW", "CIS-6.3",
                    "Set socket.request.max.bytes to limit individual request size"));

            // 27. Background threads
            String bgThreads = brokerConfig.getOrDefault("background.threads", "10");
            int bgThreadCount = Integer.parseInt(bgThreads);
            checks.add(check("Background Threads", "config",
                    bgThreadCount >= 10 ? "PASS" : "WARN",
                    "background.threads = " + bgThreadCount
                            + (bgThreadCount >= 10 ? "" : " — low thread count may delay internal tasks"),
                    "LOW", "CIS-3.11",
                    "Set background.threads >= 10 for production workloads"));

            // 28. Consumer offset retention
            String offsetRetention = brokerConfig.getOrDefault("offsets.retention.minutes", "10080");
            long offsetRetMinutes = Long.parseLong(offsetRetention);
            checks.add(check("Offset Retention", "config",
                    offsetRetMinutes >= 10080 ? "PASS" : "WARN",
                    "offsets.retention.minutes = " + offsetRetMinutes + " (" + (offsetRetMinutes / 1440) + " days)"
                            + (offsetRetMinutes >= 10080 ? "" : " — short retention may lose consumer group state"),
                    "MEDIUM", "CIS-3.12",
                    "Set offsets.retention.minutes >= 10080 (7 days)"));

            // 29. Log segment size
            String segmentBytes = brokerConfig.getOrDefault("log.segment.bytes", "1073741824");
            long segBytes = Long.parseLong(segmentBytes);
            checks.add(check("Log Segment Size", "config",
                    segBytes >= 104857600 && segBytes <= 1073741824 ? "PASS" : "WARN",
                    "log.segment.bytes = " + formatBytes(segBytes)
                            + (segBytes < 104857600 ? " — too small, excessive segment files"
                                    : segBytes > 1073741824 ? " — very large segments delay compaction" : ""),
                    "LOW", "CIS-3.13",
                    "Set log.segment.bytes between 100MB and 1GB"));

            // 30. Compression type
            String compressionType = brokerConfig.getOrDefault("compression.type", "producer");
            checks.add(check("Compression Policy", "config",
                    "producer".equals(compressionType) || "lz4".equals(compressionType)
                            || "zstd".equals(compressionType) || "snappy".equals(compressionType) ? "PASS" : "WARN",
                    "compression.type = " + compressionType
                            + ("none".equals(compressionType) ? " — wasting bandwidth and disk" : ""),
                    "LOW", "CIS-3.14",
                    "Set compression.type to lz4 or zstd for bandwidth savings"));

            // 31. SSL cipher suites
            String cipherSuites = brokerConfig.getOrDefault("ssl.cipher.suites", "");
            boolean hasWeakCipher = cipherSuites.contains("RC4") || cipherSuites.contains("DES")
                    || cipherSuites.contains("MD5") || cipherSuites.contains("NULL");
            checks.add(check("SSL Cipher Suites", "transport",
                    cipherSuites.isEmpty() ? "PASS"
                            : hasWeakCipher ? "FAIL" : "PASS",
                    cipherSuites.isEmpty() ? "Using JVM default ciphers (secure)"
                            : hasWeakCipher ? "Weak ciphers detected: " + cipherSuites
                                    : "Custom ciphers: " + cipherSuites,
                    "HIGH", "CIS-4.5",
                    "Remove RC4, DES, MD5, NULL ciphers from ssl.cipher.suites"));

            // 32. SSL endpoint identification algorithm
            String endpointIdAlgo = brokerConfig.getOrDefault("ssl.endpoint.identification.algorithm", "https");
            checks.add(check("SSL Hostname Verification", "transport",
                    "https".equals(endpointIdAlgo) ? "PASS" : "WARN",
                    "ssl.endpoint.identification.algorithm = " + (endpointIdAlgo.isEmpty() ? "(empty)" : endpointIdAlgo)
                            + ("https".equals(endpointIdAlgo) ? " — hostname verification enabled" : " — hostname verification DISABLED, vulnerable to MITM"),
                    "HIGH", "CIS-4.6",
                    "Set ssl.endpoint.identification.algorithm: https"));

            // 33. SSL enabled protocols
            String sslEnabledProtocols = brokerConfig.getOrDefault("ssl.enabled.protocols", "");
            boolean hasLegacyProtocol = sslEnabledProtocols.contains("TLSv1,")
                    || sslEnabledProtocols.contains("TLSv1.0")
                    || sslEnabledProtocols.contains("SSLv3");
            checks.add(check("SSL Enabled Protocols", "transport",
                    sslEnabledProtocols.isEmpty() ? "PASS"
                            : hasLegacyProtocol ? "FAIL" : "PASS",
                    sslEnabledProtocols.isEmpty() ? "Using JVM defaults (TLSv1.2+)"
                            : hasLegacyProtocol ? "Legacy protocols enabled: " + sslEnabledProtocols
                                    : "Protocols: " + sslEnabledProtocols,
                    "HIGH", "CIS-4.7",
                    "Remove SSLv3 and TLSv1.0 from ssl.enabled.protocols"));

            // Topic-level checks
            var topicNames = client.listTopics().names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            var topicDescriptions = client.describeTopics(topicNames).allTopicNames()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            // 34. Topics with RF=1
            long rf1Topics = topicDescriptions.values().stream()
                    .filter(td -> !td.name().startsWith("__"))
                    .filter(td -> td.partitions().stream()
                            .anyMatch(p -> p.replicas().size() < 2))
                    .count();
            checks.add(check("Topics with RF=1", "topics",
                    rf1Topics == 0 ? "PASS" : "WARN",
                    rf1Topics == 0 ? "All user topics have replication factor ≥ 2"
                            : rf1Topics + " topics have RF=1 — single point of failure",
                    "HIGH", "CIS-3.15",
                    "Increase replication.factor to at least 2 for all production topics"));

            // 35. Under-replicated partitions
            long underReplicated = topicDescriptions.values().stream()
                    .flatMap(td -> td.partitions().stream())
                    .filter(p -> p.isr().size() < p.replicas().size())
                    .count();
            checks.add(check("Under-Replicated Partitions", "topics",
                    underReplicated == 0 ? "PASS" : "WARN",
                    underReplicated == 0 ? "All partitions fully replicated"
                            : underReplicated + " partitions under-replicated — data at risk",
                    "CRITICAL", "CIS-3.16",
                    "Investigate broker health — under-replicated partitions indicate failing nodes"));

            // 36. Internal topic protection (__consumer_offsets RF)
            var consumerOffsets = topicDescriptions.get("__consumer_offsets");
            int offsetsRf = consumerOffsets != null && !consumerOffsets.partitions().isEmpty()
                    ? consumerOffsets.partitions().get(0).replicas().size() : 0;
            checks.add(check("Internal Topic Protection", "topics",
                    offsetsRf >= 3 ? "PASS" : "WARN",
                    offsetsRf == 0 ? "__consumer_offsets not found"
                            : "__consumer_offsets RF=" + offsetsRf
                                    + (offsetsRf >= 3 ? " — properly replicated" : " — should be ≥ 3"),
                    "HIGH", "CIS-3.17",
                    "Set offsets.topic.replication.factor: 3"));

            // 37. Excessive topic count
            long userTopics = topicDescriptions.keySet().stream()
                    .filter(n -> !n.startsWith("__"))
                    .count();
            checks.add(check("Topic Count", "topics",
                    userTopics <= 1000 ? "PASS" : "WARN",
                    userTopics + " user topics" + (userTopics > 1000 ? " — excessive count strains metadata and controller" : ""),
                    "LOW", "CIS-3.18",
                    "Consider consolidating topics or partitioning strategy if count exceeds 1000"));

            // 38. Max connections per IP
            String maxConnsPerIp = brokerConfig.getOrDefault("max.connections.per.ip", "");
            checks.add(check("Max Connections Per IP", "network",
                    !maxConnsPerIp.isEmpty() && !"2147483647".equals(maxConnsPerIp) ? "PASS" : "WARN",
                    !maxConnsPerIp.isEmpty() && !"2147483647".equals(maxConnsPerIp)
                            ? "max.connections.per.ip = " + maxConnsPerIp
                            : "No per-IP connection limit — single host can exhaust all connections",
                    "MEDIUM", "CIS-6.4",
                    "Set max.connections.per.ip to limit connection exhaustion from single host"));

            // 39. Network threads
            String netThreads = brokerConfig.getOrDefault("num.network.threads", "3");
            int netThreadCount = Integer.parseInt(netThreads);
            checks.add(check("Network Threads", "network",
                    netThreadCount >= 3 ? "PASS" : "WARN",
                    "num.network.threads = " + netThreadCount
                            + (netThreadCount < 3 ? " — too few for production traffic" : ""),
                    "MEDIUM", "CIS-6.5",
                    "Set num.network.threads >= 3 (typically 8 for production)"));

            // 40. IO threads
            String ioThreads = brokerConfig.getOrDefault("num.io.threads", "8");
            int ioThreadCount = Integer.parseInt(ioThreads);
            checks.add(check("IO Threads", "network",
                    ioThreadCount >= 8 ? "PASS" : "WARN",
                    "num.io.threads = " + ioThreadCount
                            + (ioThreadCount < 8 ? " — may bottleneck disk I/O under load" : ""),
                    "MEDIUM", "CIS-6.6",
                    "Set num.io.threads >= 8 for production workloads"));

            // 41. Listener security protocol map
            String protocolMap = brokerConfig.getOrDefault("listener.security.protocol.map", "");
            boolean hasPlaintextListener = protocolMap.contains("PLAINTEXT") && !protocolMap.contains("SASL_PLAINTEXT");
            checks.add(check("Listener Protocol Map", "network",
                    protocolMap.isEmpty() || !hasPlaintextListener ? "PASS" : "WARN",
                    protocolMap.isEmpty() ? "Using default listener protocols"
                            : hasPlaintextListener ? "PLAINTEXT listeners in protocol map — unencrypted traffic allowed"
                                    : "Protocol map: " + protocolMap,
                    "HIGH", "CIS-4.8",
                    "Map all listeners to SASL_SSL or SSL in listener.security.protocol.map"));

            // 42. Log flush interval
            String flushIntervalMs = brokerConfig.getOrDefault("log.flush.interval.ms", "");
            String flushIntervalMsgs = brokerConfig.getOrDefault("log.flush.interval.messages", "9223372036854775807");
            long flushMsgs = Long.parseLong(flushIntervalMsgs);
            checks.add(check("Log Flush Interval", "durability",
                    !flushIntervalMs.isEmpty() || flushMsgs < Long.MAX_VALUE ? "PASS" : "WARN",
                    !flushIntervalMs.isEmpty() ? "log.flush.interval.ms = " + flushIntervalMs
                            : flushMsgs < Long.MAX_VALUE ? "log.flush.interval.messages = " + flushMsgs
                                    : "Using OS-level fsync only — may lose data on power failure",
                    "MEDIUM", "CIS-3.19",
                    "Set log.flush.interval.ms or log.flush.interval.messages for durability vs performance trade-off"));

            // 43. Group coordinator replication
            String groupMetadataRf = brokerConfig.getOrDefault("offsets.topic.replication.factor", "1");
            int gmRf = Integer.parseInt(groupMetadataRf);
            checks.add(check("Group Coordinator Replication", "durability",
                    gmRf >= 3 ? "PASS" : "WARN",
                    "offsets.topic.replication.factor = " + gmRf
                            + (gmRf >= 3 ? "" : " — consumer group metadata at risk if broker fails"),
                    "HIGH", "CIS-3.20",
                    "Set offsets.topic.replication.factor: 3"));

            // 44. Leader imbalance threshold
            String imbalanceRatio = brokerConfig.getOrDefault("leader.imbalance.per.broker.percentage", "10");
            int imbalancePct = Integer.parseInt(imbalanceRatio);
            checks.add(check("Leader Imbalance Threshold", "durability",
                    imbalancePct <= 10 ? "PASS" : "WARN",
                    "leader.imbalance.per.broker.percentage = " + imbalancePct + "%"
                            + (imbalancePct > 10 ? " — high threshold may mask uneven load" : ""),
                    "LOW", "CIS-3.21",
                    "Keep leader.imbalance.per.broker.percentage <= 10"));

            // 45. Delegation token support
            String delegationTokenSecret = brokerConfig.getOrDefault("delegation.token.master.key", "");
            String delegationTokenMaxLife = brokerConfig.getOrDefault("delegation.token.max.lifetime.ms", "604800000");
            long maxLifeMs = Long.parseLong(delegationTokenMaxLife);
            long maxLifeDays = maxLifeMs / 86_400_000;
            checks.add(check("Delegation Token Config", "auth",
                    !delegationTokenSecret.isEmpty() ? "PASS" : "WARN",
                    !delegationTokenSecret.isEmpty()
                            ? "Delegation tokens enabled, max lifetime: " + maxLifeDays + " days"
                            : "No delegation token master key — tokens unavailable for short-lived clients",
                    "LOW", "CIS-4.9",
                    "Configure delegation.token.master.key for short-lived client credentials"));

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

        Map<String, Object> snapshot = new LinkedHashMap<>();
        snapshot.put("timestamp", report.getOrDefault("timestamp", Instant.now().toString()));
        snapshot.put("grade", report.getOrDefault("grade", "F"));
        snapshot.put("summary", report.get("summary"));
        scoreHistory.add(snapshot);
        if (scoreHistory.size() > 100) {
            scoreHistory.remove(0);
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
                if (entry.value() != null) {
                    result.put(entry.name(), entry.value());
                }
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

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> certificateCheck() {
        Map<String, Object> report = new LinkedHashMap<>();
        List<Map<String, Object>> certs = new ArrayList<>();

        try {
            AdminClient client = adminService.getClient();
            var clusterInfo = clusterHealthService.describeCluster();
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> brokers = (List<Map<String, Object>>) clusterInfo.getOrDefault("brokers", List.of());
            int brokerId = brokers.isEmpty() ? 0 : (int) brokers.get(0).get("id");
            Map<String, String> brokerConfig = fetchBrokerConfig(client, brokerId);

            String keystoreLocation = brokerConfig.getOrDefault("ssl.keystore.location", "");
            String truststoreLocation = brokerConfig.getOrDefault("ssl.truststore.location", "");
            String sslProtocol = brokerConfig.getOrDefault("ssl.protocol", "TLSv1.3");
            String clientAuth = brokerConfig.getOrDefault("ssl.client.auth", "none");
            String endpointAlgo = brokerConfig.getOrDefault("ssl.endpoint.identification.algorithm", "https");
            String cipherSuites = brokerConfig.getOrDefault("ssl.cipher.suites", "");
            String enabledProtocols = brokerConfig.getOrDefault("ssl.enabled.protocols", "");

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("broker", brokerId);
            entry.put("keystoreConfigured", !keystoreLocation.isEmpty());
            entry.put("truststoreConfigured", !truststoreLocation.isEmpty());
            entry.put("sslProtocol", sslProtocol);
            entry.put("clientAuth", clientAuth);
            entry.put("endpointIdentification", endpointAlgo);
            entry.put("cipherSuites", cipherSuites.isEmpty() ? "JVM defaults" : cipherSuites);
            entry.put("enabledProtocols", enabledProtocols.isEmpty() ? "JVM defaults" : enabledProtocols);

            boolean modernTls = sslProtocol.contains("1.2") || sslProtocol.contains("1.3");
            boolean mTls = "required".equals(clientAuth);
            boolean hostnameVerify = "https".equalsIgnoreCase(endpointAlgo);

            List<Map<String, Object>> checks = new ArrayList<>();
            checks.add(check("Keystore Configured", "transport",
                    !keystoreLocation.isEmpty() ? "PASS" : "FAIL",
                    !keystoreLocation.isEmpty() ? "SSL keystore present" : "No SSL keystore — TLS unavailable",
                    "CRITICAL", "CIS-4.11",
                    "Configure ssl.keystore.location with broker certificate"));

            checks.add(check("Truststore Configured", "transport",
                    !truststoreLocation.isEmpty() ? "PASS" : "WARN",
                    !truststoreLocation.isEmpty() ? "SSL truststore present" : "No truststore — client certs not verifiable",
                    "HIGH", "CIS-4.12",
                    "Configure ssl.truststore.location for client certificate validation"));

            checks.add(check("TLS Protocol", "transport",
                    modernTls ? "PASS" : "FAIL",
                    "ssl.protocol = " + sslProtocol,
                    "HIGH", "CIS-4.4",
                    "Set ssl.protocol: TLSv1.3"));

            checks.add(check("Mutual TLS", "transport",
                    mTls ? "PASS" : "WARN",
                    "ssl.client.auth = " + clientAuth,
                    "HIGH", "CIS-4.13",
                    "Set ssl.client.auth: required"));

            checks.add(check("Hostname Verification", "transport",
                    hostnameVerify ? "PASS" : "WARN",
                    "ssl.endpoint.identification.algorithm = " + (endpointAlgo.isEmpty() ? "(empty)" : endpointAlgo),
                    "HIGH", "CIS-4.6",
                    "Set ssl.endpoint.identification.algorithm: https"));

            entry.put("checks", checks);
            certs.add(entry);

            report.put("certificates", certs);
            report.put("totalBrokers", brokers.size());
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Certificate check failed", e);
            report.put("error", "Certificate check failed: " + e.getMessage());
        }
        return report;
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(30_000)
    public Map<String, Object> cveCheck() {
        Map<String, Object> report = new LinkedHashMap<>();

        try {
            var clusterInfo = clusterHealthService.describeCluster();
            String kafkaVersion = String.valueOf(clusterInfo.getOrDefault("kafkaVersion", "unknown"));

            List<Map<String, Object>> knownCves = new ArrayList<>();
            knownCves.add(cve("CVE-2024-31141", "Apache Kafka Client JNDI Injection", "CRITICAL",
                    "0.0.0", "3.7.0", "Clients can be tricked into JNDI lookups via SASL/OAUTHBEARER"));
            knownCves.add(cve("CVE-2024-27309", "ACL Bypass during migration", "HIGH",
                    "3.5.0", "3.6.2", "ACL authorizer may not apply during ZK-to-KRaft migration"));
            knownCves.add(cve("CVE-2023-44981", "Zookeeper SASL quorum bypass", "CRITICAL",
                    "0.0.0", "3.5.2", "ZK-based clusters may have SASL quorum peer bypass"));
            knownCves.add(cve("CVE-2023-34455", "Snappy DoS decompression", "HIGH",
                    "0.0.0", "3.4.1", "Snappy decompression can cause OutOfMemoryError"));
            knownCves.add(cve("CVE-2023-25194", "JNDI Injection via SASL JAAS config", "HIGH",
                    "2.3.0", "3.3.2", "JNDI injection possible through SASL JAAS configuration"));
            knownCves.add(cve("CVE-2022-34917", "Unauthenticated memory exhaustion", "HIGH",
                    "2.8.0", "3.2.3", "Memory exhaustion via allocating large buffers on unauthenticated connections"));
            knownCves.add(cve("CVE-2021-38153", "Timing attack on SASL/SCRAM", "MEDIUM",
                    "2.0.0", "3.0.0", "Timing side-channel leak in SCRAM authentication"));

            List<Map<String, Object>> applicable = new ArrayList<>();
            List<Map<String, Object>> patched = new ArrayList<>();

            for (Map<String, Object> c : knownCves) {
                String affectedUpTo = (String) c.get("affectedUpTo");
                if (compareVersions(kafkaVersion, affectedUpTo) <= 0) {
                    c.put("status", "VULNERABLE");
                    applicable.add(c);
                } else {
                    c.put("status", "PATCHED");
                    patched.add(c);
                }
            }

            report.put("kafkaVersion", kafkaVersion);
            report.put("vulnerabilities", applicable);
            report.put("patched", patched);
            report.put("summary", Map.of(
                    "total", knownCves.size(),
                    "vulnerable", applicable.size(),
                    "patched", patched.size()));
            report.put("grade", applicable.isEmpty() ? "PASS" : "FAIL");
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("CVE check failed", e);
            report.put("error", "CVE check failed: " + e.getMessage());
        }
        return report;
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> configConsistency() {
        Map<String, Object> report = new LinkedHashMap<>();

        try {
            AdminClient client = adminService.getClient();
            var clusterInfo = clusterHealthService.describeCluster();
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> brokers = (List<Map<String, Object>>) clusterInfo.getOrDefault("brokers", List.of());

            String[] securityKeys = {
                    "ssl.protocol", "ssl.client.auth", "ssl.keystore.location", "ssl.truststore.location",
                    "ssl.endpoint.identification.algorithm", "ssl.enabled.protocols",
                    "sasl.enabled.mechanisms", "sasl.mechanism.inter.broker.protocol",
                    "authorizer.class.name", "super.users",
                    "security.inter.broker.protocol", "listener.security.protocol.map",
                    "auto.create.topics.enable", "unclean.leader.election.enable",
                    "min.insync.replicas", "default.replication.factor",
                    "log.cleanup.policy", "delete.topic.enable",
                    "max.connections.per.ip", "max.connections"
            };

            Map<Integer, Map<String, String>> brokerConfigs = new LinkedHashMap<>();
            for (Map<String, Object> broker : brokers) {
                int bId = (int) broker.get("id");
                Map<String, String> cfg = fetchBrokerConfig(client, bId);
                Map<String, String> filtered = new LinkedHashMap<>();
                for (String key : securityKeys) {
                    filtered.put(key, cfg.getOrDefault(key, "(not set)"));
                }
                brokerConfigs.put(bId, filtered);
            }

            List<Map<String, Object>> mismatches = new ArrayList<>();
            List<Map<String, Object>> consistent = new ArrayList<>();

            for (String key : securityKeys) {
                Map<Integer, String> valsByBroker = new LinkedHashMap<>();
                for (var entry : brokerConfigs.entrySet()) {
                    valsByBroker.put(entry.getKey(), entry.getValue().getOrDefault(key, "(not set)"));
                }
                long distinctValues = valsByBroker.values().stream().distinct().count();
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("key", key);
                item.put("values", valsByBroker);
                item.put("consistent", distinctValues <= 1);

                if (distinctValues > 1) {
                    mismatches.add(item);
                } else {
                    String commonValue = valsByBroker.values().iterator().next();
                    item.put("value", commonValue);
                    consistent.add(item);
                }
            }

            report.put("brokerCount", brokers.size());
            report.put("keysChecked", securityKeys.length);
            report.put("mismatches", mismatches);
            report.put("consistent", consistent);
            report.put("mismatchCount", mismatches.size());
            report.put("grade", mismatches.isEmpty() ? "PASS" : "WARN");
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Config consistency check failed", e);
            report.put("error", "Config consistency check failed: " + e.getMessage());
        }
        return report;
    }

    private Map<String, Object> cve(String id, String title, String severity,
            String affectedFrom, String affectedUpTo, String description) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("id", id);
        c.put("title", title);
        c.put("severity", severity);
        c.put("affectedFrom", affectedFrom);
        c.put("affectedUpTo", affectedUpTo);
        c.put("description", description);
        return c;
    }

    private int compareVersions(String v1, String v2) {
        try {
            String[] parts1 = v1.split("\\.");
            String[] parts2 = v2.split("\\.");
            int len = Math.max(parts1.length, parts2.length);
            for (int i = 0; i < len; i++) {
                int a = i < parts1.length ? Integer.parseInt(parts1[i].replaceAll("[^0-9]", "")) : 0;
                int b = i < parts2.length ? Integer.parseInt(parts2[i].replaceAll("[^0-9]", "")) : 0;
                if (a != b) return Integer.compare(a, b);
            }
            return 0;
        } catch (Exception e) {
            return 1;
        }
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> aclCoverage() {
        Map<String, Object> report = new LinkedHashMap<>();

        try {
            AdminClient client = adminService.getClient();
            List<AclBinding> acls = fetchAcls(client);
            var topicNames = client.listTopics().names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            Set<String> principals = new TreeSet<>();
            for (AclBinding acl : acls) {
                principals.add(acl.entry().principal());
            }

            List<Map<String, Object>> coverage = new ArrayList<>();
            int uncoveredCount = 0;

            for (String topic : new TreeSet<>(topicNames)) {
                if (topic.startsWith("__")) continue;
                Map<String, Object> entry = new LinkedHashMap<>();
                entry.put("topic", topic);

                List<AclBinding> topicAcls = acls.stream()
                        .filter(a -> a.pattern().resourceType() == ResourceType.TOPIC)
                        .filter(a -> a.pattern().name().equals(topic)
                                || (a.pattern().patternType() == PatternType.PREFIXED && topic.startsWith(a.pattern().name()))
                                || a.pattern().name().equals("*"))
                        .toList();

                Map<String, List<String>> userOps = new LinkedHashMap<>();
                for (AclBinding acl : topicAcls) {
                    String principal = acl.entry().principal();
                    String perm = acl.entry().permissionType() == AclPermissionType.ALLOW ? "+" : "-";
                    String op = perm + acl.entry().operation().toString();
                    userOps.computeIfAbsent(principal, k -> new ArrayList<>()).add(op);
                }

                entry.put("aclCount", topicAcls.size());
                entry.put("users", userOps);
                entry.put("covered", !topicAcls.isEmpty());
                if (topicAcls.isEmpty()) uncoveredCount++;
                coverage.add(entry);
            }

            report.put("topics", coverage);
            report.put("totalTopics", coverage.size());
            report.put("uncoveredTopics", uncoveredCount);
            report.put("principals", principals);
            report.put("totalAcls", acls.size());
            report.put("grade", uncoveredCount == 0 ? "PASS" : "WARN");
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("ACL coverage check failed", e);
            report.put("error", "ACL coverage check failed: " + e.getMessage());
        }
        return report;
    }

    public Map<String, Object> scoreTrend() {
        Map<String, Object> report = new LinkedHashMap<>();
        report.put("history", new ArrayList<>(scoreHistory));
        report.put("totalSnapshots", scoreHistory.size());

        if (!scoreHistory.isEmpty()) {
            Map<String, Object> latest = scoreHistory.get(scoreHistory.size() - 1);
            report.put("currentGrade", latest.get("grade"));

            if (scoreHistory.size() >= 2) {
                Map<String, Object> previous = scoreHistory.get(scoreHistory.size() - 2);
                String currGrade = String.valueOf(latest.get("grade"));
                String prevGrade = String.valueOf(previous.get("grade"));
                int trend = gradeRank(prevGrade) - gradeRank(currGrade);
                report.put("trend", trend > 0 ? "IMPROVING" : trend < 0 ? "DEGRADING" : "STABLE");
                report.put("previousGrade", prevGrade);
            } else {
                report.put("trend", "BASELINE");
            }
        } else {
            report.put("trend", "NO_DATA");
            report.put("message", "Run 'kates security audit' first to collect score data");
        }

        report.put("timestamp", Instant.now().toString());
        return report;
    }

    @Retry(maxRetries = 2, delay = 1000)
    @Timeout(60_000)
    public Map<String, Object> secretScan() {
        Map<String, Object> report = new LinkedHashMap<>();

        Pattern[] sensitivePatterns = {
                Pattern.compile("(?i)(password|passwd|secret|api[_-]?key|token|credential)"),
                Pattern.compile("(?i)(aws[_-]?access|aws[_-]?secret|AKIA[0-9A-Z])"),
                Pattern.compile("(?i)(private[_-]?key|ssh[_-]?key|BEGIN.*PRIVATE)"),
                Pattern.compile("(?i)(jdbc:|mongodb://|redis://|amqp://)"),
                Pattern.compile("[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}"),
                Pattern.compile("\\b(?:\\d[ -]*?){13,16}\\b"),
        };
        String[] patternNames = {
                "Credentials/Secrets", "AWS Keys", "Private Keys",
                "Connection Strings", "Email Addresses", "Credit Card Numbers"
        };

        try {
            AdminClient client = adminService.getClient();
            var topicNames = client.listTopics().names().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<Map<String, Object>> findings = new ArrayList<>();

            for (String topic : topicNames) {
                if (topic.startsWith("__")) continue;

                for (int i = 0; i < sensitivePatterns.length; i++) {
                    if (sensitivePatterns[i].matcher(topic).find()) {
                        Map<String, Object> finding = new LinkedHashMap<>();
                        finding.put("location", "topic-name");
                        finding.put("topic", topic);
                        finding.put("pattern", patternNames[i]);
                        finding.put("severity", "MEDIUM");
                        finding.put("detail", "Topic name matches sensitive pattern: " + patternNames[i]);
                        findings.add(finding);
                    }
                }
            }

            List<ConfigResource> topicConfigs = topicNames.stream()
                    .map(t -> new ConfigResource(ConfigResource.Type.TOPIC, t))
                    .toList();

            var configResults = client.describeConfigs(topicConfigs)
                    .all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            for (var configEntry : configResults.entrySet()) {
                String topicName = configEntry.getKey().name();
                if (topicName.startsWith("__")) continue;
                for (ConfigEntry ce : configEntry.getValue().entries()) {
                    String val = ce.value();
                    if (val == null || val.isEmpty()) continue;
                    for (int i = 0; i < sensitivePatterns.length; i++) {
                        if (sensitivePatterns[i].matcher(val).find()) {
                            Map<String, Object> finding = new LinkedHashMap<>();
                            finding.put("location", "topic-config");
                            finding.put("topic", topicName);
                            finding.put("configKey", ce.name());
                            finding.put("pattern", patternNames[i]);
                            finding.put("severity", "HIGH");
                            finding.put("detail", "Config value matches: " + patternNames[i]);
                            findings.add(finding);
                        }
                    }
                }
            }

            report.put("findings", findings);
            report.put("topicsScanned", topicNames.size());
            report.put("findingsCount", findings.size());
            report.put("patternsChecked", patternNames.length);
            report.put("grade", findings.isEmpty() ? "PASS" : "WARN");
            report.put("timestamp", Instant.now().toString());

        } catch (Exception e) {
            LOG.error("Secret scan failed", e);
            report.put("error", "Secret scan failed: " + e.getMessage());
        }
        return report;
    }
}
