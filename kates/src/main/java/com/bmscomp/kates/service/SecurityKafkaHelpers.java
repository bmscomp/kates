package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
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
}
