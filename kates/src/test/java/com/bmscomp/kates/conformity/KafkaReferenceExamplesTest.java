package com.bmscomp.kates.conformity;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.stream.Stream;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

/**
 * Each reference cli/examples/conformity ships is one the Kates API reads as
 * written, with its strict reader. The CLI's TestConformityExamplesMatchTheirSchema
 * validates the same files against the JSON Schema beside them, so the schema,
 * this model and the examples stay in step for every field the examples use.
 */
class KafkaReferenceExamplesTest {

    /** Relative to the kates module, where Maven runs the tests. */
    static final Path EXAMPLES = Path.of("..", "cli", "examples", "conformity");

    private final KafkaReferenceReader reader = new KafkaReferenceReader();

    static Stream<String> examples() throws IOException {
        try (Stream<Path> files = Files.list(EXAMPLES)) {
            List<String> names = files.map(file -> file.getFileName().toString())
                    .filter(name -> name.endsWith(".yaml"))
                    .sorted()
                    .toList();
            assertTrue(names.size() >= 2, "found only " + names);
            return names.stream();
        }
    }

    /**
     * The JSON the CLI sends for a YAML reference: a date stays the text it was
     * written as, as conformityInstance in cli/cmd/conformity_examples_test.go
     * converts it.
     */
    static byte[] json(String yaml) throws IOException {
        return new ObjectMapper().writeValueAsBytes(new YAMLMapper().readTree(yaml));
    }

    @ParameterizedTest(name = "{0}")
    @MethodSource("examples")
    void readsStrictly(String file) throws IOException {
        KafkaReference reference = reader.read(json(Files.readString(EXAMPLES.resolve(file))));

        assertEquals(file.replace(".yaml", ""), reference.metadata().name());
    }

    /** Every rule the full example declares, by id: the catalogue a waiver or a report names. */
    @Test
    void fullExampleDeclaresEveryRuleByItsPath() throws IOException {
        KafkaReference reference = reader.read(json(Files.readString(EXAMPLES.resolve("prod-3az.yaml"))));

        assertEquals(
                List.of(
                        "versions.kubernetes",
                        "versions.strimzi",
                        "versions.kafka",
                        "controllers.count",
                        "controllers.dedicated",
                        "controllers.requests.cpu",
                        "controllers.requests.memory",
                        "controllers.storage.type",
                        "controllers.storage.size",
                        "controllers.zones",
                        "controllers.skew",
                        "controllers.perNode",
                        "controllers.antiAffinity",
                        "brokers.count",
                        "brokers.requests.cpu",
                        "brokers.requests.memory",
                        "brokers.limits.memory",
                        "brokers.storage.type",
                        "brokers.storage.size",
                        "brokers.storage.class",
                        "brokers.zones",
                        "brokers.skew",
                        "brokers.perNode",
                        "brokers.antiAffinity",
                        "kafka.rackAwareness",
                        "kafka.authorization",
                        "kafka.listeners.require[tls]",
                        "kafka.listeners.every.tls",
                        "kafka.listeners.every.auth",
                        "kafka.config[default.replication.factor]",
                        "kafka.config[min.insync.replicas]",
                        "kafka.config[offsets.topic.replication.factor]",
                        "kafka.config[transaction.state.log.min.isr]",
                        "kafka.config[unclean.leader.election.enable]",
                        "kafka.config[auto.create.topics.enable]",
                        "topics.replicationFactor",
                        "topics.minInsyncReplicas",
                        "monitoring.metrics",
                        "monitoring.kafkaExporter",
                        "monitoring.podMonitor"),
                List.copyOf(KafkaReferenceReader.ruleIds(reference)));
    }
}
