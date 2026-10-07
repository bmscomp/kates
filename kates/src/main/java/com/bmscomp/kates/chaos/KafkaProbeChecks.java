package com.bmscomp.kates.chaos;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collection;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Function;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.Admin;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterOptions;
import org.apache.kafka.clients.admin.DescribeTopicsOptions;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsOptions;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsSpec;
import org.apache.kafka.clients.admin.ListConsumerGroupsOptions;
import org.apache.kafka.clients.admin.ListOffsetsOptions;
import org.apache.kafka.clients.admin.ListOffsetsResult.ListOffsetsResultInfo;
import org.apache.kafka.clients.admin.ListPartitionReassignmentsOptions;
import org.apache.kafka.clients.admin.ListTopicsOptions;
import org.apache.kafka.clients.admin.OffsetSpec;
import org.apache.kafka.clients.admin.PartitionReassignment;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.clients.producer.KafkaProducer;
import org.apache.kafka.clients.producer.Producer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.ProducerRecord;
import org.apache.kafka.clients.producer.RecordMetadata;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.TopicPartitionInfo;
import org.apache.kafka.common.serialization.ByteArraySerializer;

import com.bmscomp.kates.config.KafkaSecurityConfig;
import com.bmscomp.kates.service.KafkaAdminService;

/**
 * The checks a {@code kafkaProbe} names in its {@code command}. Kates runs them
 * itself, over its own connection to the cluster at
 * {@code kates.kafka.bootstrap-servers} with its own credentials, whatever the
 * fault's target namespace. Nothing runs inside a broker, so no credential
 * leaves the Kates pod and no Kafka CLI JVM competes with the broker under
 * test.
 *
 * <p>The probes used to run the Kafka CLI with {@code sh -c} in a broker pod:
 * {@code kafka-topics.sh … 2>/dev/null | grep -c 'Topic:' || echo '0'}. A
 * command that could not reach the broker printed 0, so those probes passed
 * whenever they could not ask. Here a check that cannot ask throws
 * {@link ProbeFailure}, and the probe fails.
 *
 * <ul>
 *   <li>{@code under-replicated-partitions}: the partitions with fewer in-sync
 *       replicas than replicas, not counting the replicas a reassignment is
 *       still adding, as {@code kafka-topics.sh --describe
 *       --under-replicated-partitions} lists them.
 *   <li>{@code unavailable-partitions}: the partitions with no leader, or a
 *       leader that is not a live broker ({@code --unavailable-partitions}).
 *   <li>{@code produce <topic>}: sends {@value #PRODUCE_RECORDS} records of
 *       {@value #PRODUCE_RECORD_BYTES} bytes with {@code acks=all}, and prints
 *       how many were acknowledged per second.
 *   <li>{@code consumer-lag [<group>]}: the lag of every consumer group, or of
 *       one, summed over the partitions it has committed an offset for, as
 *       {@code kafka-consumer-groups.sh --describe} reports it.
 * </ul>
 */
@ApplicationScoped
public class KafkaProbeChecks {

    public static final String UNDER_REPLICATED_PARTITIONS = "under-replicated-partitions";
    public static final String UNAVAILABLE_PARTITIONS = "unavailable-partitions";
    public static final String PRODUCE = "produce";
    public static final String CONSUMER_LAG = "consumer-lag";

    private record Check(String name, int minArguments, int maxArguments, String usage) {}

    private static final List<Check> KNOWN = List.of(
            new Check(UNDER_REPLICATED_PARTITIONS, 0, 0, UNDER_REPLICATED_PARTITIONS),
            new Check(UNAVAILABLE_PARTITIONS, 0, 0, UNAVAILABLE_PARTITIONS),
            new Check(PRODUCE, 1, 1, PRODUCE + " <topic>"),
            new Check(CONSUMER_LAG, 0, 1, CONSUMER_LAG + " [<group>]"));

    /** How each check is written. */
    public static final List<String> CHECKS = KNOWN.stream().map(Check::usage).toList();

    static final int PRODUCE_RECORDS = 10;
    static final int PRODUCE_RECORD_BYTES = 100;

    @Inject
    KafkaAdminService adminService;

    @Inject
    KafkaSecurityConfig securityConfig;

    /** Builds the produce check's producer; a test hands it a MockProducer instead. */
    Function<Properties, Producer<byte[], byte[]>> producerFactory =
            props -> new KafkaProducer<>(props, new ByteArraySerializer(), new ByteArraySerializer());

    private final AtomicInteger producerCount = new AtomicInteger();

    /** Runs the check {@code command} names, within {@code timeout}, and returns what it prints. */
    public String run(String command, Duration timeout) {
        problem(command).ifPresent(problem -> {
            throw new ProbeFailure(problem);
        });
        String[] words = words(command);
        Deadline deadline = new Deadline(timeout);
        return switch (words[0]) {
            case UNDER_REPLICATED_PARTITIONS -> String.valueOf(underReplicatedPartitions(deadline));
            case UNAVAILABLE_PARTITIONS -> String.valueOf(unavailablePartitions(deadline));
            case PRODUCE -> produce(words[1], deadline);
            default -> String.valueOf(consumerLag(words.length > 1 ? words[1] : null, deadline));
        };
    }

    /**
     * What is wrong with {@code command} as a kafkaProbe's, if anything: no
     * check by that name, or the wrong number of arguments for it.
     */
    public static Optional<String> problem(String command) {
        String[] words = words(command);
        if (words.length == 0) {
            return Optional.of("a kafkaProbe needs a command naming its check; use one of " + CHECKS);
        }
        Optional<Check> check =
                KNOWN.stream().filter(c -> c.name().equals(words[0])).findFirst();
        if (check.isEmpty()) {
            return Optional.of("a kafkaProbe has no check '" + words[0] + "'; use one of " + CHECKS);
        }
        int given = words.length - 1;
        if (given < check.get().minArguments() || given > check.get().maxArguments()) {
            return Optional.of("kafkaProbe command '" + String.join(" ", words) + "' does not match '"
                    + check.get().usage() + "'");
        }
        return Optional.empty();
    }

    private static String[] words(String command) {
        return command == null || command.isBlank()
                ? new String[0]
                : command.trim().split("\\s+");
    }

    int underReplicatedPartitions(Deadline deadline) {
        Admin admin = adminService.sharedAdminClient();
        Map<String, TopicDescription> topics = describeAllTopics(admin, deadline);
        Map<TopicPartition, PartitionReassignment> reassignments = await(
                admin.listPartitionReassignments(
                                new ListPartitionReassignmentsOptions().timeoutMs(deadline.remainingMs()))
                        .reassignments(),
                deadline,
                "listing partition reassignments");
        int count = 0;
        for (TopicDescription topic : topics.values()) {
            for (TopicPartitionInfo partition : topic.partitions()) {
                PartitionReassignment reassignment =
                        reassignments.get(new TopicPartition(topic.name(), partition.partition()));
                if (partition.isr().size() < replicationFactor(partition, reassignment)) {
                    count++;
                }
            }
        }
        return count;
    }

    /**
     * The replicas a partition should have in sync, as TopicCommand works it
     * out. While a reassignment is in progress, the partition's replicas
     * include the ones it is adding, which are not expected in the ISR until
     * they catch up. It is in progress as long as the partition still has one
     * of the replicas it adds or removes; one that finished after it was read
     * no longer counts.
     */
    private static int replicationFactor(TopicPartitionInfo partition, PartitionReassignment reassignment) {
        if (reassignment != null) {
            Set<Integer> changing = new HashSet<>(reassignment.addingReplicas());
            changing.addAll(reassignment.removingReplicas());
            boolean inProgress = partition.replicas().stream().map(Node::id).anyMatch(changing::contains);
            if (inProgress) {
                return reassignment.replicas().size()
                        - reassignment.addingReplicas().size();
            }
        }
        return partition.replicas().size();
    }

    int unavailablePartitions(Deadline deadline) {
        Admin admin = adminService.sharedAdminClient();
        Map<String, TopicDescription> topics = describeAllTopics(admin, deadline);
        Set<Integer> liveBrokers = await(
                        admin.describeCluster(new DescribeClusterOptions().timeoutMs(deadline.remainingMs()))
                                .nodes(),
                        deadline,
                        "describing the cluster")
                .stream()
                .map(Node::id)
                .collect(Collectors.toSet());
        int count = 0;
        for (TopicDescription topic : topics.values()) {
            for (TopicPartitionInfo partition : topic.partitions()) {
                Node leader = partition.leader();
                if (leader == null || !liveBrokers.contains(leader.id())) {
                    count++;
                }
            }
        }
        return count;
    }

    /** Every topic, internal ones included, as kafka-topics.sh --describe sees them. */
    private static Map<String, TopicDescription> describeAllTopics(Admin admin, Deadline deadline) {
        Set<String> names = await(
                admin.listTopics(new ListTopicsOptions().listInternal(true).timeoutMs(deadline.remainingMs()))
                        .names(),
                deadline,
                "listing topics");
        if (names.isEmpty()) {
            return Map.of();
        }
        return await(
                admin.describeTopics(names, new DescribeTopicsOptions().timeoutMs(deadline.remainingMs()))
                        .allTopicNames(),
                deadline,
                "describing topics");
    }

    /**
     * Sends the records and prints the acknowledged ones per second, from the
     * first send to the last acknowledgement. The topic's metadata is read
     * first: a producer would otherwise block {@code max.block.ms} on every
     * send to a topic that does not exist.
     */
    String produce(String topic, Deadline deadline) {
        Properties props = new Properties();
        props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, adminService.getBootstrapServers());
        props.put(ProducerConfig.CLIENT_ID_CONFIG, "kates-probe-" + producerCount.incrementAndGet());
        props.put(ProducerConfig.ACKS_CONFIG, "all");
        props.put(ProducerConfig.LINGER_MS_CONFIG, 0);
        int budgetMs = deadline.remainingMs();
        // delivery.timeout.ms must cover linger.ms + request.timeout.ms.
        props.put(ProducerConfig.MAX_BLOCK_MS_CONFIG, budgetMs);
        props.put(ProducerConfig.REQUEST_TIMEOUT_MS_CONFIG, Math.max(1, budgetMs / 2));
        props.put(ProducerConfig.DELIVERY_TIMEOUT_MS_CONFIG, budgetMs);
        props.put(ProducerConfig.METRIC_REPORTER_CLASSES_CONFIG, "");
        securityConfig.apply(props);

        Producer<byte[], byte[]> producer = producerFactory.apply(props);
        try {
            try {
                producer.partitionsFor(topic);
            } catch (RuntimeException e) {
                throw new ProbeFailure("reading the metadata of topic " + topic + " failed: " + describe(e), e);
            }
            long start = System.nanoTime();
            List<Future<RecordMetadata>> sends = new ArrayList<>(PRODUCE_RECORDS);
            try {
                for (int i = 0; i < PRODUCE_RECORDS; i++) {
                    sends.add(producer.send(new ProducerRecord<>(topic, new byte[PRODUCE_RECORD_BYTES])));
                }
            } catch (RuntimeException e) {
                throw new ProbeFailure("sending to topic " + topic + " failed: " + describe(e), e);
            }
            int acknowledged = 0;
            Throwable firstError = null;
            for (Future<RecordMetadata> send : sends) {
                try {
                    send.get(deadline.remainingMsOrZero(), TimeUnit.MILLISECONDS);
                    acknowledged++;
                } catch (ExecutionException e) {
                    firstError = firstError != null ? firstError : e.getCause();
                } catch (TimeoutException e) {
                    firstError = firstError != null ? firstError : e;
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw new ProbeFailure("producing to " + topic + " was interrupted");
                }
            }
            if (acknowledged == 0) {
                throw new ProbeFailure("no record sent to " + topic + " was acknowledged within "
                        + deadline.timeoutText() + ": " + describe(firstError));
            }
            // A microsecond at least, so the rate stays a finite number.
            double seconds = Math.max(1e-6, (System.nanoTime() - start) / 1e9);
            return String.format(Locale.ROOT, "%.2f", acknowledged / seconds);
        } finally {
            // Every send is acknowledged, failed or given up on by now.
            producer.close(Duration.ZERO);
        }
    }

    long consumerLag(String group, Deadline deadline) {
        Admin admin = adminService.sharedAdminClient();
        Collection<ConsumerGroupListing> listed = await(
                admin.listConsumerGroups(new ListConsumerGroupsOptions().timeoutMs(deadline.remainingMs()))
                        .all(),
                deadline,
                "listing consumer groups");
        Set<String> groups =
                listed.stream().map(ConsumerGroupListing::groupId).collect(Collectors.toCollection(TreeSet::new));
        if (group != null) {
            // An unknown group commits nothing, so its lag would read 0.
            if (!groups.contains(group)) {
                throw new ProbeFailure("consumer group " + group + " does not exist");
            }
            groups = Set.of(group);
        }
        if (groups.isEmpty()) {
            return 0;
        }

        Map<String, ListConsumerGroupOffsetsSpec> specs = new HashMap<>();
        groups.forEach(g -> specs.put(g, new ListConsumerGroupOffsetsSpec()));
        Map<String, Map<TopicPartition, OffsetAndMetadata>> committed = await(
                admin.listConsumerGroupOffsets(
                                specs, new ListConsumerGroupOffsetsOptions().timeoutMs(deadline.remainingMs()))
                        .all(),
                deadline,
                "reading committed offsets");
        Map<TopicPartition, OffsetSpec> partitions = new HashMap<>();
        committed
                .values()
                .forEach(offsets -> offsets.forEach((tp, offset) -> {
                    if (offset != null) {
                        partitions.put(tp, OffsetSpec.latest());
                    }
                }));
        if (partitions.isEmpty()) {
            return 0;
        }
        Map<TopicPartition, ListOffsetsResultInfo> ends = await(
                admin.listOffsets(partitions, new ListOffsetsOptions().timeoutMs(deadline.remainingMs()))
                        .all(),
                deadline,
                "reading log-end offsets");

        long lag = 0;
        for (Map<TopicPartition, OffsetAndMetadata> offsets : committed.values()) {
            for (Map.Entry<TopicPartition, OffsetAndMetadata> e : offsets.entrySet()) {
                ListOffsetsResultInfo end = ends.get(e.getKey());
                if (e.getValue() != null && end != null) {
                    lag += Math.max(0, end.offset() - e.getValue().offset());
                }
            }
        }
        return lag;
    }

    private static <T> T await(KafkaFuture<T> future, Deadline deadline, String what) {
        try {
            return future.get(deadline.remainingMsOrZero(), TimeUnit.MILLISECONDS);
        } catch (ExecutionException e) {
            Throwable cause = e.getCause() != null ? e.getCause() : e;
            throw new ProbeFailure(what + " failed: " + describe(cause), cause);
        } catch (TimeoutException e) {
            throw new ProbeFailure(what + " did not finish within " + deadline.timeoutText());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new ProbeFailure(what + " was interrupted");
        }
    }

    private static String describe(Throwable t) {
        if (t == null) {
            return "no error reported";
        }
        return t.getMessage() != null ? t.getClass().getSimpleName() + ": " + t.getMessage() : t.toString();
    }

    /** The time a check has left; once it is spent, the next call to the cluster fails the check. */
    static final class Deadline {
        private final Duration timeout;
        private final long endNanos;

        Deadline(Duration timeout) {
            this.timeout = timeout;
            this.endNanos = System.nanoTime() + timeout.toNanos();
        }

        /** Milliseconds left, for a Kafka call's timeout; throws once none are. */
        int remainingMs() {
            long left = remainingMsOrZero();
            if (left <= 0) {
                throw new ProbeFailure("the probe's " + timeoutText() + " ran out");
            }
            return (int) Math.min(Integer.MAX_VALUE, left);
        }

        /** A probe's timeout is whole seconds; a test's may be shorter. */
        String timeoutText() {
            return timeout.toMillis() % 1000 == 0 ? timeout.toSeconds() + " s" : timeout.toMillis() + " ms";
        }

        long remainingMsOrZero() {
            return Math.max(0, TimeUnit.NANOSECONDS.toMillis(endNanos - System.nanoTime()));
        }
    }
}
