package com.bmscomp.kates.service;

import java.time.Duration;
import java.util.List;
import java.util.Optional;
import java.util.Properties;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;

import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.config.KafkaSecurityConfig;

/**
 * Share Groups consumer for Kafka 4.2 (KIP-932).
 *
 * Unlike traditional consumer groups where each partition is assigned to exactly
 * one consumer, Share Groups allow multiple consumers to share partitions with
 * server-side message distribution. This provides work-queue semantics:
 *
 *   - No partition assignment — the broker distributes records
 *   - Per-record acknowledgment — failed records can be retried
 *   - Dynamic scaling — consumers can join/leave without rebalancing
 *
 * Use case: kates-results processing where multiple backend instances
 * need to process test results without partition affinity.
 *
 * Requires: group.share.enable=true on the Kafka broker (already configured).
 */
@ApplicationScoped
public class ShareGroupConsumerService {

    private static final Logger LOG = Logger.getLogger(ShareGroupConsumerService.class);
    private static final String SHARE_GROUP_ID = "kates-results-share";
    private static final String RESULTS_TOPIC = "kates-results";

    private final String bootstrapServers;
    private final KafkaSecurityConfig securityConfig;
    private final Optional<String> clientRack;

    private volatile KafkaConsumer<String, String> consumer;
    private final AtomicBoolean running = new AtomicBoolean(false);
    private final AtomicLong processedCount = new AtomicLong(0);
    private final AtomicLong failedCount = new AtomicLong(0);
    private final CopyOnWriteArrayList<String> recentResults = new CopyOnWriteArrayList<>();
    private Thread pollingThread;

    @Inject
    public ShareGroupConsumerService(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            KafkaSecurityConfig securityConfig,
            @ConfigProperty(name = "kates.kafka.client-rack") Optional<String> clientRack) {
        this.bootstrapServers = bootstrapServers;
        this.securityConfig = securityConfig;
        this.clientRack = clientRack;
    }

    /**
     * Starts the Share Group consumer. Call from an endpoint or startup event.
     * Returns false if already running.
     */
    public boolean start() {
        if (running.getAndSet(true)) {
            LOG.info("Share Group consumer already running");
            return false;
        }

        try {
            consumer = buildShareGroupConsumer();
            consumer.subscribe(List.of(RESULTS_TOPIC));
            LOG.infof("Share Group consumer started [group=%s, topic=%s]",
                    SHARE_GROUP_ID, RESULTS_TOPIC);

            pollingThread = new Thread(this::pollLoop, "share-group-poller");
            pollingThread.setDaemon(true);
            pollingThread.start();
            return true;
        } catch (Exception e) {
            running.set(false);
            LOG.error("Failed to start Share Group consumer", e);
            return false;
        }
    }

    /**
     * Stops the Share Group consumer gracefully.
     */
    public boolean stop() {
        if (!running.getAndSet(false)) {
            return false;
        }

        if (pollingThread != null) {
            pollingThread.interrupt();
        }
        if (consumer != null) {
            try {
                consumer.close();
                LOG.info("Share Group consumer stopped");
            } catch (Exception e) {
                LOG.warn("Share Group consumer close error", e);
            }
        }
        return true;
    }

    @PreDestroy
    void shutdown() {
        stop();
    }

    private void pollLoop() {
        while (running.get()) {
            try {
                ConsumerRecords<String, String> records = consumer.poll(Duration.ofMillis(500));
                if (records.isEmpty()) continue;

                records.forEach(record -> {
                    try {
                        processResult(record.key(), record.value());
                        processedCount.incrementAndGet();
                    } catch (Exception e) {
                        failedCount.incrementAndGet();
                        LOG.warnf("Share Group record processing failed [key=%s]: %s",
                                record.key(), e.getMessage());
                    }
                });
            } catch (org.apache.kafka.common.errors.InterruptException e) {
                LOG.debug("Share Group poll interrupted (shutdown)");
                break;
            } catch (Exception e) {
                if (running.get()) {
                    LOG.error("Share Group poll error, retrying in 5s", e);
                    try { Thread.sleep(5000); } catch (InterruptedException ie) { break; }
                }
            }
        }
    }

    private void processResult(String key, String value) {
        // Keep last 50 results for inspection via REST
        if (recentResults.size() >= 50) {
            recentResults.remove(0);
        }
        recentResults.add(String.format("[%s] %s", key, truncate(value, 150)));

        LOG.debugf("Share Group processed result [key=%s, len=%d]",
                key, value != null ? value.length() : 0);
    }

    private KafkaConsumer<String, String> buildShareGroupConsumer() {
        Properties props = new Properties();
        props.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        // Share Group protocol: use group.protocol=share
        props.put(ConsumerConfig.GROUP_ID_CONFIG, SHARE_GROUP_ID);
        props.put("group.protocol", "share");
        props.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        props.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        props.put(ConsumerConfig.MAX_POLL_RECORDS_CONFIG, "50");

        clientRack.ifPresent(rack -> {
            if (!rack.isEmpty()) {
                props.put(ConsumerConfig.CLIENT_RACK_CONFIG, rack);
            }
        });
        securityConfig.apply(props);

        return new KafkaConsumer<>(props);
    }

    public boolean isRunning() {
        return running.get();
    }

    public long getProcessedCount() {
        return processedCount.get();
    }

    public long getFailedCount() {
        return failedCount.get();
    }

    public List<String> getRecentResults() {
        return List.copyOf(recentResults);
    }

    private String truncate(String value, int maxLen) {
        if (value == null) return "null";
        return value.length() <= maxLen ? value : value.substring(0, maxLen) + "...";
    }
}
