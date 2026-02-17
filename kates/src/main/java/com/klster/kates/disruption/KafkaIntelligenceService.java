package com.klster.kates.disruption;

import jakarta.enterprise.context.ApplicationScoped;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.AdminClientConfig;
import org.apache.kafka.clients.admin.ListOffsetsResult;
import org.apache.kafka.clients.admin.OffsetSpec;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.TopicPartitionInfo;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.jboss.logging.Logger;

/**
 * Kafka intelligence layer for disruption tests.
 * <ul>
 *   <li><b>Leader Resolution</b> — resolve partition leader broker ID before targeting</li>
 *   <li><b>ISR Tracking</b> — observe ISR health during disruption, compute Time-to-Full-ISR</li>
 *   <li><b>Consumer Lag Monitoring</b> — track consumer group lag, compute Time-to-Lag-Recovery</li>
 * </ul>
 */
@ApplicationScoped
public class KafkaIntelligenceService {

    private static final Logger LOG = Logger.getLogger(KafkaIntelligenceService.class);
    private static final int TIMEOUT_SECONDS = 15;

    @ConfigProperty(name = "kates.kafka.bootstrap-servers")
    String bootstrapServers;

    private final ScheduledExecutorService scheduler = Executors.newScheduledThreadPool(2,
            r -> {
                Thread t = new Thread(r, "kafka-intelligence");
                t.setDaemon(true);
                return t;
            });

    private AdminClient createClient() {
        Properties props = new Properties();
        props.put(AdminClientConfig.BOOTSTRAP_SERVERS_CONFIG, bootstrapServers);
        props.put(AdminClientConfig.REQUEST_TIMEOUT_MS_CONFIG, "10000");
        props.put(AdminClientConfig.DEFAULT_API_TIMEOUT_MS_CONFIG, "15000");
        return AdminClient.create(props);
    }

    /**
     * Resolves the broker ID that currently leads the given topic-partition.
     *
     * @return broker ID, or -1 if resolution fails
     */
    public int resolveLeaderBrokerId(String topic, int partition) {
        try (AdminClient client = createClient()) {
            var desc = client.describeTopics(Collections.singleton(topic))
                    .allTopicNames()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(topic);

            if (desc == null) {
                LOG.warn("Topic not found: " + topic);
                return -1;
            }

            for (TopicPartitionInfo pi : desc.partitions()) {
                if (pi.partition() == partition) {
                    Node leader = pi.leader();
                    if (leader != null) {
                        LOG.info("Resolved leader for " + topic + "-" + partition
                                + " → broker " + leader.id());
                        return leader.id();
                    }
                }
            }

            LOG.warn("Partition " + partition + " not found in topic " + topic);
            return -1;
        } catch (Exception e) {
            LOG.warn("Failed to resolve partition leader", e);
            return -1;
        }
    }

    /**
     * Starts background ISR tracking for the given topic.
     * Call {@link IsrTracker#stop()} to stop and compute metrics.
     */
    public IsrTracker startIsrTracking(String topic, int pollIntervalMs) {
        return new IsrTracker(topic, pollIntervalMs);
    }

    /**
     * Starts background consumer lag tracking for the given group.
     * Call {@link LagTracker#stop()} to stop and compute metrics.
     */
    public LagTracker startLagTracking(String groupId, int pollIntervalMs) {
        return new LagTracker(groupId, pollIntervalMs);
    }

    /**
     * Tracks ISR state over time for all partitions of a topic.
     */
    public class IsrTracker {
        private final String topic;
        private final List<IsrSnapshot.Entry> timeline = new CopyOnWriteArrayList<>();
        private final AtomicBoolean running = new AtomicBoolean(true);
        private final ScheduledFuture<?> future;
        private volatile Instant disruptionStartTime;

        IsrTracker(String topic, int pollIntervalMs) {
            this.topic = topic;
            this.future = scheduler.scheduleAtFixedRate(
                    this::poll, 0, pollIntervalMs, TimeUnit.MILLISECONDS);
        }

        public void markDisruptionStart() {
            this.disruptionStartTime = Instant.now();
        }

        private void poll() {
            if (!running.get()) return;

            try (AdminClient client = createClient()) {
                var desc = client.describeTopics(Collections.singleton(topic))
                        .allTopicNames()
                        .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                        .get(topic);

                if (desc == null) return;

                Instant now = Instant.now();
                for (TopicPartitionInfo pi : desc.partitions()) {
                    timeline.add(new IsrSnapshot.Entry(
                            now, topic, pi.partition(),
                            pi.leader() != null ? pi.leader().id() : -1,
                            pi.isr().stream().map(Node::id).toList(),
                            pi.replicas().size()));
                }
            } catch (Exception e) {
                LOG.debug("ISR poll failed", e);
            }
        }

        /**
         * Stops tracking and computes aggregated ISR metrics.
         */
        public IsrSnapshot.Metrics stop() {
            running.set(false);
            future.cancel(false);
            return computeMetrics();
        }

        private IsrSnapshot.Metrics computeMetrics() {
            if (timeline.isEmpty()) {
                return new IsrSnapshot.Metrics(null, 0, 0, 0, List.of());
            }

            int minDepth = Integer.MAX_VALUE;
            int peakUnderReplicated = 0;
            Set<Integer> partitions = new HashSet<>();

            for (IsrSnapshot.Entry e : timeline) {
                partitions.add(e.partition());
                int depth = e.isrDepth();
                if (depth < minDepth) minDepth = depth;
            }

            int totalPartitions = partitions.size();

            Map<Instant, Integer> underReplicatedByTime = new TreeMap<>();
            for (IsrSnapshot.Entry e : timeline) {
                if (!e.isFullyReplicated()) {
                    underReplicatedByTime.merge(e.timestamp(), 1, Integer::sum);
                }
            }
            for (int count : underReplicatedByTime.values()) {
                if (count > peakUnderReplicated) peakUnderReplicated = count;
            }

            Duration timeToFullIsr = computeTimeToFullIsr();

            return new IsrSnapshot.Metrics(
                    timeToFullIsr, minDepth, peakUnderReplicated,
                    totalPartitions, List.copyOf(timeline));
        }

        private Duration computeTimeToFullIsr() {
            if (disruptionStartTime == null) return null;

            Instant lastUnderReplicated = null;
            for (IsrSnapshot.Entry e : timeline) {
                if (e.timestamp().isAfter(disruptionStartTime) && !e.isFullyReplicated()) {
                    lastUnderReplicated = e.timestamp();
                }
            }

            if (lastUnderReplicated == null) return Duration.ZERO;

            for (IsrSnapshot.Entry e : timeline) {
                if (e.timestamp().isAfter(lastUnderReplicated) && e.isFullyReplicated()) {
                    return Duration.between(disruptionStartTime, e.timestamp());
                }
            }

            return null;
        }
    }

    /**
     * Tracks consumer group lag over time.
     */
    public class LagTracker {
        private final String groupId;
        private final List<LagSnapshot.Entry> timeline = new CopyOnWriteArrayList<>();
        private final AtomicBoolean running = new AtomicBoolean(true);
        private final ScheduledFuture<?> future;
        private volatile long baselineLag = -1;
        private volatile Instant disruptionStartTime;

        LagTracker(String groupId, int pollIntervalMs) {
            this.groupId = groupId;
            this.future = scheduler.scheduleAtFixedRate(
                    this::poll, 0, pollIntervalMs, TimeUnit.MILLISECONDS);
        }

        public void markDisruptionStart() {
            this.disruptionStartTime = Instant.now();
            if (!timeline.isEmpty()) {
                this.baselineLag = timeline.getLast().totalLag();
            }
        }

        private void poll() {
            if (!running.get()) return;

            try (AdminClient client = createClient()) {
                Map<TopicPartition, OffsetAndMetadata> offsets = client
                        .listConsumerGroupOffsets(groupId)
                        .partitionsToOffsetAndMetadata()
                        .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

                if (offsets.isEmpty()) return;

                Map<TopicPartition, OffsetSpec> latestRequest = new HashMap<>();
                offsets.keySet().forEach(tp -> latestRequest.put(tp, OffsetSpec.latest()));

                Map<TopicPartition, ListOffsetsResult.ListOffsetsResultInfo> endOffsets =
                        client.listOffsets(latestRequest)
                                .all()
                                .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

                Map<String, Long> perTopicLag = new LinkedHashMap<>();
                long totalLag = 0;

                for (var entry : offsets.entrySet()) {
                    TopicPartition tp = entry.getKey();
                    long current = entry.getValue().offset();
                    long end = endOffsets.containsKey(tp)
                            ? endOffsets.get(tp).offset() : current;
                    long lag = Math.max(0, end - current);
                    totalLag += lag;
                    perTopicLag.merge(tp.topic(), lag, Long::sum);
                }

                timeline.add(new LagSnapshot.Entry(
                        Instant.now(), groupId, totalLag, perTopicLag));

            } catch (Exception e) {
                LOG.debug("Lag poll failed", e);
            }
        }

        /**
         * Stops tracking and computes aggregated lag metrics.
         */
        public LagSnapshot.Metrics stop() {
            running.set(false);
            future.cancel(false);
            return computeMetrics();
        }

        private LagSnapshot.Metrics computeMetrics() {
            if (timeline.isEmpty()) {
                return new LagSnapshot.Metrics(0, 0, null, List.of());
            }

            long baseLag = baselineLag >= 0 ? baselineLag : timeline.getFirst().totalLag();
            long peak = 0;

            for (LagSnapshot.Entry e : timeline) {
                if (e.totalLag() > peak) peak = e.totalLag();
            }

            Duration ttlr = computeTimeToLagRecovery(baseLag);

            return new LagSnapshot.Metrics(baseLag, peak, ttlr, List.copyOf(timeline));
        }

        private Duration computeTimeToLagRecovery(long baseLag) {
            if (disruptionStartTime == null) return null;

            boolean lagSpiked = false;
            for (LagSnapshot.Entry e : timeline) {
                if (e.timestamp().isAfter(disruptionStartTime) && e.totalLag() > baseLag * 1.1) {
                    lagSpiked = true;
                }
                if (lagSpiked && e.totalLag() <= baseLag * 1.1) {
                    return Duration.between(disruptionStartTime, e.timestamp());
                }
            }

            return lagSpiked ? null : Duration.ZERO;
        }
    }
}
