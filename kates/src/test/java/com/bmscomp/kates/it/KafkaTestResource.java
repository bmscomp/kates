package com.bmscomp.kates.it;

import java.util.Map;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import org.testcontainers.kafka.KafkaContainer;
import org.testcontainers.utility.DockerImageName;

/** Real Kafka (KRaft, PLAINTEXT) for the AdminClient integration test. */
public class KafkaTestResource implements QuarkusTestResourceLifecycleManager {

    private KafkaContainer kafka;

    @Override
    public Map<String, String> start() {
        // apache/kafka 3.9.0 rejects the default advertised.listeners=0.0.0.0
        // during KRaft storage format (Apache Kafka KAFKA-18281). The
        // Testcontainers maintainer's fix is to define KAFKA_LISTENERS
        // explicitly so the container derives routable advertised listeners.
        kafka = new KafkaContainer(DockerImageName.parse("apache/kafka:3.9.0"))
                .withEnv("KAFKA_LISTENERS", "PLAINTEXT://:9092,BROKER://:9093,CONTROLLER://:9094");
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
