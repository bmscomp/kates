package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import java.util.function.Predicate;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.common.acl.AclBinding;
import org.apache.kafka.common.acl.AclBindingFilter;
import org.apache.kafka.common.config.ConfigResource;
import org.jboss.logging.Logger;

/**
 * Low-level Kafka inspection helpers shared by {@link SecurityService} and
 * {@link SecurityPentestService}. Extracted so the pentest logic could move to
 * its own service without these three helpers (previously private to the
 * 1500-line SecurityService and used by ~6 of its methods) being duplicated.
 */
@ApplicationScoped
public class SecurityKafkaHelpers {

    private static final Logger LOG = Logger.getLogger(SecurityKafkaHelpers.class);
    private static final int TIMEOUT_SECONDS = 30;

    public Map<String, String> fetchBrokerConfig(AdminClient client, int brokerId) {
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

    public List<AclBinding> fetchAcls(AdminClient client) {
        try {
            return new ArrayList<>(
                    client.describeAcls(AclBindingFilter.ANY).values().get(TIMEOUT_SECONDS, TimeUnit.SECONDS));
        } catch (Exception e) {
            LOG.warn("Failed to fetch ACLs (authorizer may not be configured)", e);
            return List.of();
        }
    }

    public String formatBytes(long bytes) {
        if (bytes >= 1_073_741_824) return String.format("%.1fGB", bytes / 1_073_741_824.0);
        if (bytes >= 1_048_576) return String.format("%.1fMB", bytes / 1_048_576.0);
        if (bytes >= 1_024) return String.format("%.1fKB", bytes / 1_024.0);
        return bytes + "B";
    }

    /**
     * Each listener's security protocol, by listener name, as the broker
     * resolves it: the name before :// in listeners, looked up in
     * listener.security.protocol.map, or the name itself when the map has no
     * entry. The checks read these, not the text of listeners, where Strimzi's
     * PLAIN-9092 holds no PLAINTEXT://, and not sasl.enabled.mechanisms, which
     * Kafka reports as GSSAPI on a broker with no SASL listener. A config
     * without listeners, one that could not be read included, gets Kafka's
     * default, PLAINTEXT://:9092.
     */
    static Map<String, String> listenerProtocols(Map<String, String> config) {
        Map<String, String> protocolMap = new LinkedHashMap<>();
        for (String entry :
                config.getOrDefault("listener.security.protocol.map", "").split(",")) {
            int colon = entry.indexOf(':');
            if (colon > 0) {
                protocolMap.put(normalised(entry.substring(0, colon)), normalised(entry.substring(colon + 1)));
            }
        }
        String listeners = config.getOrDefault("listeners", "");
        Map<String, String> protocols = new LinkedHashMap<>();
        for (String listener : (listeners.isBlank() ? "PLAINTEXT://:9092" : listeners).split(",")) {
            int end = listener.indexOf("://");
            if (end > 0) {
                String name = normalised(listener.substring(0, end));
                protocols.put(name, protocolMap.getOrDefault(name, name));
            }
        }
        return protocols;
    }

    /** Kafka upper-cases listener names and protocols before it compares them. */
    private static String normalised(String name) {
        return name.trim().toUpperCase(Locale.ROOT);
    }

    /** SASL_PLAINTEXT or SASL_SSL: the listener's clients authenticate. */
    static boolean sasl(String protocol) {
        return protocol.startsWith("SASL_");
    }

    /** PLAINTEXT or SASL_PLAINTEXT: the listener's traffic is not encrypted. */
    static boolean unencrypted(String protocol) {
        return protocol.equals("PLAINTEXT") || protocol.equals("SASL_PLAINTEXT");
    }

    /**
     * Every client has to authenticate: some listener is SASL_* and none is
     * PLAINTEXT. An SSL listener counts as neither, with or without client
     * certificates.
     */
    static boolean saslRequired(Map<String, String> protocols) {
        return protocols.values().stream().anyMatch(SecurityKafkaHelpers::sasl)
                && !protocols.containsValue("PLAINTEXT");
    }

    /** The listeners whose protocol matches, as "PLAIN-9092 (SASL_PLAINTEXT), ...", or "" when none does. */
    static String listeners(Map<String, String> protocols, Predicate<String> protocol) {
        return protocols.entrySet().stream()
                .filter(e -> protocol.test(e.getValue()))
                .map(e -> e.getKey() + " (" + e.getValue() + ")")
                .collect(Collectors.joining(", "));
    }

    /**
     * The mechanisms the SASL listeners enable, joined by commas, or "" when
     * no listener is SASL_*: each one's
     * listener.name.<listener>.sasl.enabled.mechanisms, as Strimzi sets them,
     * or the broker-wide sasl.enabled.mechanisms when that key is not reported.
     */
    static String saslMechanisms(Map<String, String> config, Map<String, String> protocols) {
        String brokerWide = config.getOrDefault("sasl.enabled.mechanisms", "");
        Set<String> mechanisms = new LinkedHashSet<>();
        protocols.forEach((name, protocol) -> {
            if (sasl(protocol)) {
                String key = "listener.name." + name.toLowerCase(Locale.ROOT) + ".sasl.enabled.mechanisms";
                for (String mechanism : config.getOrDefault(key, brokerWide).split(",")) {
                    if (!mechanism.isBlank()) mechanisms.add(mechanism.trim());
                }
            }
        });
        return String.join(",", mechanisms);
    }
}
