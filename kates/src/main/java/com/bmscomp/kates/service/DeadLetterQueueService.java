package com.bmscomp.kates.service;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.common.config.SaslConfigs;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import io.quarkus.scheduler.Scheduled;

/**
 * Consumes messages from the Dead Letter Queue (kates-dlq) topic.
 * Tracks failed messages, exposes metrics, and logs them for investigation.
 * Messages in the DLQ are records that failed processing in upstream consumers.
 */
@ApplicationScoped
public class DeadLetterQueueService {

    private static final Logger LOG = Logger.getLogger(DeadLetterQueueService.class);
    private static final String DLQ_TOPIC = "kates-dlq";

    private final String bootstrapServers;
    private final Optional<String> saslUsername;
    private final Optional<String> saslPassword;
    private final Optional<String> clientRack;

    private volatile KafkaConsumer<String, String> consumer;
    private volatile boolean running = false;

    private final AtomicLong totalDlqMessages = new AtomicLong(0);
    private final AtomicLong dlqMessagesSinceLastCheck = new AtomicLong(0);
    private final Map<String, AtomicLong> dlqBySource = new ConcurrentHashMap<>();
    private volatile Instant lastDlqMessage = null;

    @Inject
    public DeadLetterQueueService(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            @ConfigProperty(name = "kates.kafka.sasl.username") Optional<String> saslUsername,
            @ConfigProperty(name = "kates.kafka.sasl.password") Optional<String> saslPassword,
            @ConfigProperty(name = "kates.kafka.client-rack") Optional<String> clientRack) {
        this.bootstrapServers = bootstrapServers;
        this.saslUsername = saslUsername;
        this.saslPassword = saslPassword;
        this.clientRack = clientRack;
    }

    @PostConstruct
    void init() {
        try {
            consumer = buildConsumer();
            consumer.subscribe(List.of(DLQ_TOPIC));
            running = true;
            LOG.info("DLQ consumer subscribed to: " + DLQ_TOPIC);
        } catch (Exception e) {
            LOG.warn("DLQ consumer init deferred — topic may not exist yet: " + e.getMessage());
        }
    }

    @PreDestroy
    void shutdown() {
        running = false;
        if (consumer != null) {
            try {
                consumer.close();
                LOG.info("DLQ consumer closed");
            } catch (Exception e) {
                LOG.warn("DLQ consumer close failed", e);
            }
        }
    }

    /**
     * Polls the DLQ topic every 30 seconds and processes any dead-lettered messages.
     */
    @Scheduled(every = "30s", identity = "dlq-poller")
    void pollDlq() {
        if (!running || consumer == null) return;

        try {
            ConsumerRecords<String, String> records = consumer.poll(Duration.ofMillis(1000));
            if (records.isEmpty()) return;

            records.forEach(record -> {
                totalDlqMessages.incrementAndGet();
                dlqMessagesSinceLastCheck.incrementAndGet();
                lastDlqMessage = Instant.now();

                String source = extractSource(record.key());
                dlqBySource.computeIfAbsent(source, k -> new AtomicLong(0)).incrementAndGet();

                LOG.warnf("DLQ message [partition=%d offset=%d key=%s]: %s",
                        record.partition(), record.offset(), record.key(),
                        truncate(record.value(), 200));
            });

            consumer.commitSync(Duration.ofSeconds(5));
        } catch (Exception e) {
            LOG.error("DLQ poll failed", e);
        }
    }

    /**
     * Resets the since-last-check counter (called by alert evaluation).
     */
    @Scheduled(every = "5m", identity = "dlq-alert-check")
    void checkDlqAlertThreshold() {
        long recent = dlqMessagesSinceLastCheck.getAndSet(0);
        if (recent > 0) {
            LOG.warnf("DLQ alert: %d messages received in last 5 minutes (total: %d)",
                    recent, totalDlqMessages.get());
        }
    }

    public long getTotalDlqMessages() {
        return totalDlqMessages.get();
    }

    public Instant getLastDlqMessage() {
        return lastDlqMessage;
    }

    public Map<String, AtomicLong> getDlqBySource() {
        return Map.copyOf(dlqBySource);
    }

    private KafkaConsumer<String, String> buildConsumer() {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(ConsumerConfig.GROUP_ID_CONFIG, "kates-dlq-consumer");
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        props.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "false");
        props.put(ConsumerConfig.MAX_POLL_RECORDS_CONFIG, "100");

        clientRack.ifPresent(rack -> props.put(ConsumerConfig.CLIENT_RACK_CONFIG, rack));

        if (saslUsername.isPresent() && saslPassword.isPresent()) {
            props.put("security.protocol", "SASL_PLAINTEXT");
            props.put(SaslConfigs.SASL_MECHANISM, "SCRAM-SHA-512");
            props.put(SaslConfigs.SASL_JAAS_CONFIG,
                    "org.apache.kafka.common.security.scram.ScramLoginModule required "
                    + "username=\"" + saslUsername.get() + "\" "
                    + "password=\"" + saslPassword.get() + "\";");
        }

        return new KafkaConsumer<>(props);
    }

    private String extractSource(String key) {
        if (key == null) return "unknown";
        int dot = key.indexOf('.');
        return dot > 0 ? key.substring(0, dot) : key;
    }

    private String truncate(String value, int maxLen) {
        if (value == null) return "null";
        return value.length() <= maxLen ? value : value.substring(0, maxLen) + "...";
    }
}
