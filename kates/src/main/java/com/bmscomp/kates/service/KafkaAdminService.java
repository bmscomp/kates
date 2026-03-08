package com.bmscomp.kates.service;

import java.util.Properties;
import java.util.concurrent.locks.ReentrantLock;

import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AdminClientConfig;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.config.KafkaSecurityConfig;

/**
 * Manages the shared Kafka AdminClient lifecycle.
 * Business operations live in focused services:
 * {@link TopicService}, {@link ConsumerGroupService},
 * {@link ClusterHealthService}, {@link KafkaClientService}.
 */
@ApplicationScoped
public class KafkaAdminService {

    private static final Logger LOG = Logger.getLogger(KafkaAdminService.class);

    private final String bootstrapServers;
    private final KafkaSecurityConfig securityConfig;
    private volatile AdminClient sharedClient;
    private final ReentrantLock clientLock = new ReentrantLock();

    @Inject
    public KafkaAdminService(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            KafkaSecurityConfig securityConfig) {
        this.bootstrapServers = bootstrapServers;
        this.securityConfig = securityConfig;
    }

    @PostConstruct
    void init() {
        try {
            sharedClient = buildClient();
            LOG.info("AdminClient pool initialized for: " + bootstrapServers);
        } catch (Exception e) {
            LOG.warn("AdminClient init deferred — broker not reachable: " + e.getMessage());
        }
    }

    @PreDestroy
    void shutdown() {
        if (sharedClient != null) {
            try {
                sharedClient.close(java.time.Duration.ofSeconds(5));
                LOG.info("AdminClient pool closed");
            } catch (Exception e) {
                LOG.warn("AdminClient close failed", e);
            }
        }
    }

    private AdminClient buildClient() {
        Properties props = new Properties();
        props.put(AdminClientConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(AdminClientConfig.REQUEST_TIMEOUT_MS_CONFIG, "5000");
        props.put(AdminClientConfig.DEFAULT_API_TIMEOUT_MS_CONFIG, "15000");
        props.put(AdminClientConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);
        return AdminClient.create(props);
    }

    AdminClient getClient() {
        AdminClient c = sharedClient;
        if (c != null) return c;
        clientLock.lock();
        try {
            if (sharedClient == null) {
                sharedClient = buildClient();
            }
            return sharedClient;
        } finally {
            clientLock.unlock();
        }
    }

    public String getBootstrapServers() {
        return bootstrapServers;
    }
}
