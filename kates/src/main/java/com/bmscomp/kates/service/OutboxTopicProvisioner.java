package com.bmscomp.kates.service;

import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.errors.TopicExistsException;
import org.apache.kafka.common.errors.UnknownTopicOrPartitionException;
import org.eclipse.microprofile.config.Config;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Creates the topic the transactional outbox publishes to, when it is missing.
 *
 * <p>{@link OutboxPoller} sends every lifecycle event to the {@code
 * test-events-out} channel, and the webhook path reads them back from {@code
 * test-events-in}. Both name {@code kates-test-events}, and nothing created it:
 * no chart or profile declares it, and the {@code kafka-cluster} chart sets
 * {@code auto.create.topics.enable=false}. So every send blocked for the
 * producer's {@code max.block.ms} and failed with "Topic kates-test-events not
 * present in metadata", every event reached {@code outbox_dead_letters} after
 * {@code kates.outbox.max-attempts} failed sends, and no webhook ever fired.
 *
 * <p>The Kates API is the only writer and the only reader of that topic, so it
 * provisions the topic itself rather than asking every install to declare it.
 * That covers the platform profile and a Kafka the user brings alike, as long as
 * the Kates API's principal may create topics, which it already needs for the
 * topics its tests create.
 *
 * <p>A schedule rather than a one-off startup step: the cluster under test is
 * routinely down when the pod starts, and a test or a chaos experiment can
 * delete the topic later. The first check runs as the scheduler starts. A topic
 * that exists is left exactly as it is, since whoever declared it owns its
 * configuration.
 */
@ApplicationScoped
public class OutboxTopicProvisioner {

    private static final Logger LOG = Logger.getLogger(OutboxTopicProvisioner.class);

    static final String OUTGOING_CHANNEL = "test-events-out";
    static final String INCOMING_CHANNEL = "test-events-in";
    private static final String KAFKA_CONNECTOR = "smallrye-kafka";
    private static final long TIMEOUT_SECONDS = 10;

    private final KafkaAdminService adminService;
    private final Config config;
    private final String bootstrapServers;
    private final int partitions;
    private final int replicationFactor;
    private final String retentionMs;

    /**
     * What went wrong on the last check, or null if it succeeded. Only decides
     * log levels: a problem is logged at WARN when it first appears and at DEBUG
     * while it persists, so an unreachable cluster does not add a warning to
     * every check.
     */
    private volatile String lastProblem;

    @Inject
    public OutboxTopicProvisioner(
            KafkaAdminService adminService,
            Config config,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            @ConfigProperty(name = "kates.outbox.topic.partitions", defaultValue = "1") int partitions,
            @ConfigProperty(name = "kates.outbox.topic.replication-factor", defaultValue = "3") int replicationFactor,
            @ConfigProperty(name = "kates.outbox.topic.retention-ms", defaultValue = "604800000") String retentionMs) {
        this.adminService = adminService;
        this.config = config;
        this.bootstrapServers = bootstrapServers;
        this.partitions = partitions;
        this.replicationFactor = replicationFactor;
        this.retentionMs = retentionMs;
    }

    @Scheduled(
            every = "{kates.outbox.topic.check-interval:60s}",
            identity = "outbox-topic-provisioner",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void check() {
        ensureTopics();
    }

    /**
     * Creates whichever event-bus topics are missing. Never throws: a failure is
     * logged and the next scheduled check tries again.
     *
     * @return the topics this call created
     */
    public Set<String> ensureTopics() {
        Set<String> topics = topicsToEnsure(config, bootstrapServers);
        if (topics.isEmpty()) {
            return Set.of();
        }

        AdminClient admin;
        Set<String> missing;
        try {
            admin = adminService.sharedAdminClient();
            missing = missing(admin, topics);
        } catch (Exception e) {
            problem(
                    "check",
                    e,
                    "Could not check the outbox topic %s: %s. Retrying every kates.outbox.topic.check-interval.",
                    topics);
            return Set.of();
        }
        if (missing.isEmpty()) {
            resolved(topics);
            return Set.of();
        }

        Set<String> created = new LinkedHashSet<>();
        try {
            int brokers = admin.describeCluster()
                    .nodes()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .size();
            short rf = (short) Math.max(1, Math.min(replicationFactor, brokers));
            Map<String, String> topicConfig = Map.of("cleanup.policy", "delete", "retention.ms", retentionMs);
            List<NewTopic> newTopics = missing.stream()
                    .map(name -> new NewTopic(name, partitions, rf).configs(topicConfig))
                    .toList();
            for (Map.Entry<String, KafkaFuture<Void>> result :
                    admin.createTopics(newTopics).values().entrySet()) {
                try {
                    result.getValue().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
                    created.add(result.getKey());
                    LOG.infof(
                            "Created the outbox topic %s (%d partition(s), replication factor %d, retention.ms %s)",
                            result.getKey(), partitions, rf, retentionMs);
                } catch (ExecutionException e) {
                    // Another replica created it between our check and our create.
                    if (!(e.getCause() instanceof TopicExistsException)) {
                        throw e;
                    }
                }
            }
        } catch (Exception e) {
            problem(
                    "create",
                    e,
                    "The outbox topic %s is missing and could not be created: %s. Until it exists, each lifecycle"
                            + " event moves to outbox_dead_letters after kates.outbox.max-attempts failed sends and no"
                            + " webhook fires. Create the topic, or let the Kates API's Kafka principal create it.",
                    missing);
            return created;
        }
        resolved(topics);
        return created;
    }

    /**
     * The topics behind the event-bus channels that this bean should look after:
     * those wired to the Kafka connector, on the cluster at {@code
     * kates.kafka.bootstrap-servers}.
     *
     * <p>A channel on another connector needs no topic (the tests route both to
     * the in-memory connector). A channel that names its own {@code
     * bootstrap.servers} keeps it — {@link
     * com.bmscomp.kates.config.MessagingKafkaConfigCustomizer} leaves such a
     * channel alone — so its topic lives on a cluster the shared AdminClient does
     * not reach, and creating it here would put it on the wrong one.
     */
    static Set<String> topicsToEnsure(Config config, String bootstrapServers) {
        Set<String> topics = new LinkedHashSet<>();
        addTopic(config, bootstrapServers, "mp.messaging.outgoing." + OUTGOING_CHANNEL + ".", OUTGOING_CHANNEL, topics);
        addTopic(config, bootstrapServers, "mp.messaging.incoming." + INCOMING_CHANNEL + ".", INCOMING_CHANNEL, topics);
        return topics;
    }

    private static void addTopic(
            Config config, String bootstrapServers, String prefix, String channel, Set<String> topics) {
        String connector = value(config, prefix + "connector").orElse("");
        if (!KAFKA_CONNECTOR.equals(connector)) {
            return;
        }
        Optional<String> ownBootstrap =
                value(config, prefix + "bootstrap.servers").or(() -> value(config, "kafka.bootstrap.servers"));
        if (ownBootstrap.isPresent() && !ownBootstrap.get().equals(bootstrapServers)) {
            LOG.debugf("Channel %s names its own bootstrap.servers; its topic is left to that cluster", channel);
            return;
        }
        // The connector's own default when a channel names no topic.
        topics.add(value(config, prefix + "topic").orElse(channel));
    }

    private static Optional<String> value(Config config, String key) {
        return config.getOptionalValue(key, String.class).filter(v -> !v.isBlank());
    }

    private static Set<String> missing(AdminClient admin, Set<String> topics) throws Exception {
        Set<String> missing = new LinkedHashSet<>();
        for (Map.Entry<String, KafkaFuture<TopicDescription>> description :
                admin.describeTopics(topics).topicNameValues().entrySet()) {
            try {
                description.getValue().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);
            } catch (ExecutionException e) {
                if (!(e.getCause() instanceof UnknownTopicOrPartitionException)) {
                    throw e;
                }
                missing.add(description.getKey());
            }
        }
        return missing;
    }

    private void problem(String stage, Exception e, String message, Set<String> topics) {
        if (e instanceof InterruptedException) {
            Thread.currentThread().interrupt();
        }
        Throwable cause = e instanceof ExecutionException && e.getCause() != null ? e.getCause() : e;
        String key = stage + ":" + cause.getClass().getName();
        String text = String.format(message, String.join(", ", topics), cause);
        if (key.equals(lastProblem)) {
            LOG.debug(text);
        } else {
            LOG.warn(text);
        }
        lastProblem = key;
    }

    private void resolved(Set<String> topics) {
        if (lastProblem != null) {
            LOG.infof("The outbox topic %s is in place", String.join(", ", topics));
        }
        lastProblem = null;
    }
}
