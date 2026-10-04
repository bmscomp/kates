package com.bmscomp.kates.it;

import java.util.Map;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import org.testcontainers.kafka.KafkaContainer;
import org.testcontainers.utility.DockerImageName;

/**
 * Real Kafka (KRaft, PLAINTEXT) for the integration tests.
 *
 * <p>The broker auto-creates topics, as Apache Kafka does by default, unless the
 * test passes {@code @ResourceArg(name = AUTO_CREATE_TOPICS, value = "false")}.
 * {@code krafter} runs with auto-creation off, and a test that depends on a
 * topic existing has to run against a broker that behaves the same way, or it
 * passes whether or not anything creates the topic.
 */
public class KafkaTestResource implements QuarkusTestResourceLifecycleManager {

    static final String AUTO_CREATE_TOPICS = "auto-create-topics";

    private KafkaContainer kafka;
    private boolean autoCreateTopics = true;

    @Override
    public void init(Map<String, String> initArgs) {
        autoCreateTopics = Boolean.parseBoolean(initArgs.getOrDefault(AUTO_CREATE_TOPICS, "true"));
    }

    @Override
    public Map<String, String> start() {
        // apache/kafka 3.9.0 rejects the default advertised.listeners=0.0.0.0
        // during KRaft storage format (Apache Kafka KAFKA-18281). The
        // Testcontainers maintainer's fix is to define KAFKA_LISTENERS
        // explicitly so the container derives routable advertised listeners.
        kafka = new KafkaContainer(DockerImageName.parse("apache/kafka:3.9.0"))
                .withEnv("KAFKA_LISTENERS", "PLAINTEXT://:9092,BROKER://:9093,CONTROLLER://:9094")
                .withEnv("KAFKA_AUTO_CREATE_TOPICS_ENABLE", String.valueOf(autoCreateTopics));
        kafka.start();
        return Map.of(
                "kates.kafka.bootstrap-servers",
                kafka.getBootstrapServers(),
                "kates.kafka.security.protocol",
                "PLAINTEXT");
    }

    @Override
    public void stop() {
        if (kafka != null) {
            kafka.stop();
        }
    }
}
