package com.klster.kates.trogdor;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.klster.kates.trogdor.spec.ConsumeBenchSpec;
import com.klster.kates.trogdor.spec.ProduceBenchSpec;
import com.klster.kates.trogdor.spec.RoundTripWorkloadSpec;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class TrogdorSpecSerializationTest {

    private final ObjectMapper mapper = new ObjectMapper();

    @Test
    void produceBenchSpecSerializesClassField() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create(
                "localhost:9092", "test-topic", 3, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("org.apache.kafka.trogdor.workload.ProduceBenchSpec",
                json.get("class").asText());
        assertFalse(json.has("specClass"),
                "Should serialize as 'class' not 'specClass'");
    }

    @Test
    void consumeBenchSpecSerializesCorrectClass() throws Exception {
        ConsumeBenchSpec spec = ConsumeBenchSpec.create(
                "localhost:9092", "test-topic", 3, 100_000, 60_000, "test-group");

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("org.apache.kafka.trogdor.workload.ConsumeBenchSpec",
                json.get("class").asText());
    }

    @Test
    void roundTripSpecSerializesCorrectClass() throws Exception {
        RoundTripWorkloadSpec spec = RoundTripWorkloadSpec.create(
                "localhost:9092", "test-topic", 3, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("org.apache.kafka.trogdor.workload.RoundTripWorkloadSpec",
                json.get("class").asText());
    }

    @Test
    void nullFieldsAreOmitted() throws Exception {
        ProduceBenchSpec spec = new ProduceBenchSpec(60_000);
        JsonNode json = mapper.valueToTree(spec);

        assertFalse(json.has("bootstrapServers"),
                "Null bootstrapServers should be omitted");
        assertFalse(json.has("keyGenerator"),
                "Null keyGenerator should be omitted");
    }

    @Test
    void activeTopicsKeyFormatIsCorrect() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create(
                "localhost:9092", "perf-topic", 6, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        JsonNode topics = json.get("activeTopics");
        assertTrue(topics.has("perf-topic[0-5]"),
                "Topic key should be 'perf-topic[0-5]' for 6 partitions");
    }

    @Test
    void produceBenchSpecContainsAllExpectedFields() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create(
                "broker:9092", "topic", 3, 5000, 200_000, 120_000, 2048);
        spec.getProducerConf().put("acks", "all");

        JsonNode json = mapper.valueToTree(spec);

        assertEquals("broker:9092", json.get("bootstrapServers").asText());
        assertEquals(5000, json.get("targetMessagesPerSec").asInt());
        assertEquals(200_000, json.get("maxMessages").asLong());
        assertEquals(120_000, json.get("durationMs").asLong());
        assertTrue(json.get("startMs").asLong() > 0);
        assertEquals("all", json.get("producerConf").get("acks").asText());
    }

    @Test
    void consumeBenchSpecContainsConsumerGroup() throws Exception {
        ConsumeBenchSpec spec = ConsumeBenchSpec.create(
                "broker:9092", "topic", 3, 100_000, 60_000, "my-group");

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("my-group", json.get("consumerGroup").asText());
        assertEquals("broker:9092", json.get("bootstrapServers").asText());
    }

    @Test
    void roundTripSpecContainsValueSize() throws Exception {
        RoundTripWorkloadSpec spec = RoundTripWorkloadSpec.create(
                "broker:9092", "topic", 3, 500, 50_000, 30_000, 4096);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(4096, json.get("valueSize").asInt());
        assertEquals(500, json.get("targetMessagesPerSec").asInt());
    }

    @Test
    void valueGeneratorSpecSerializesSize() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create(
                "localhost:9092", "topic", 3, 1000, 100_000, 60_000, 8192);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(8192, json.get("valueGenerator").get("size").asInt());
    }
}
