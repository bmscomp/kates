package com.bmscomp.kates.trogdor;

import static org.junit.jupiter.api.Assertions.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;
import com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec;

class TrogdorSpecSerializationTest {

    private final ObjectMapper mapper = new ObjectMapper();

    @Test
    void produceBenchSpecSerializesClassField() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("localhost:9092", "test-topic", 3, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(
                "org.apache.kafka.trogdor.workload.ProduceBenchSpec",
                json.get("class").asText());
        assertFalse(json.has("specClass"), "Should serialize as 'class' not 'specClass'");
    }

    @Test
    void consumeBenchSpecSerializesCorrectClass() throws Exception {
        ConsumeBenchSpec spec = ConsumeBenchSpec.create("localhost:9092", "test-topic", 100_000, 60_000, "test-group");

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(
                "org.apache.kafka.trogdor.workload.ConsumeBenchSpec",
                json.get("class").asText());
    }

    @Test
    void roundTripSpecSerializesCorrectClass() throws Exception {
        RoundTripWorkloadSpec spec =
                RoundTripWorkloadSpec.create("localhost:9092", "test-topic", 3, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(
                "org.apache.kafka.trogdor.workload.RoundTripWorkloadSpec",
                json.get("class").asText());
    }

    @Test
    void nullFieldsAreOmitted() throws Exception {
        ProduceBenchSpec spec = new ProduceBenchSpec(60_000);
        JsonNode json = mapper.valueToTree(spec);

        assertFalse(json.has("bootstrapServers"), "Null bootstrapServers should be omitted");
        assertFalse(json.has("keyGenerator"), "Null keyGenerator should be omitted");
    }

    @Test
    void activeTopicsKeyIsTheTopicWithItsPartitionCount() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("localhost:9092", "perf-topic", 6, 1000, 100_000, 60_000, 1024);

        JsonNode json = mapper.valueToTree(spec);
        JsonNode topics = json.get("activeTopics");
        // Trogdor expands a range in the key into topics: "perf-topic[0-5]"
        // was six topics, perf-topic0 to perf-topic5.
        assertEquals(1, topics.size());
        assertEquals(6, topics.get("perf-topic").get("numPartitions").asInt());
    }

    @Test
    void consumeActiveTopicsIsAListOfNames() throws Exception {
        ConsumeBenchSpec spec = ConsumeBenchSpec.create("broker:9092", "perf-topic", 100_000, 60_000, "my-group");

        JsonNode topics = mapper.valueToTree(spec).get("activeTopics");
        assertTrue(topics.isArray(), "Trogdor's ConsumeBenchSpec takes a List<String>");
        assertEquals("perf-topic", topics.get(0).asText());
        assertEquals(1, topics.size());
    }

    @Test
    void produceBenchSpecHasNoTotalProducers() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("localhost:9092", "topic", 3, 1000, 100_000, 60_000, 1024);

        assertFalse(mapper.valueToTree(spec).has("totalProducers"), "Trogdor refuses a spec with it");
    }

    @Test
    void produceBenchSpecContainsAllExpectedFields() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("broker:9092", "topic", 3, 5000, 200_000, 120_000, 2048);
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
        ConsumeBenchSpec spec = ConsumeBenchSpec.create("broker:9092", "topic", 100_000, 60_000, "my-group");

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("my-group", json.get("consumerGroup").asText());
        assertEquals("broker:9092", json.get("bootstrapServers").asText());
    }

    @Test
    void roundTripSpecCarriesItsValueSizeAsAGenerator() throws Exception {
        RoundTripWorkloadSpec spec = RoundTripWorkloadSpec.create("broker:9092", "topic", 3, 500, 50_000, 30_000, 4096);

        JsonNode json = mapper.valueToTree(spec);
        assertFalse(json.has("valueSize"), "Trogdor's RoundTripWorkloadSpec has no valueSize");
        assertEquals("constant", json.get("valueGenerator").get("type").asText());
        assertEquals(4096, json.get("valueGenerator").get("size").asInt());
        assertEquals(500, json.get("targetMessagesPerSec").asInt());
    }

    @Test
    void valueGeneratorSpecSerializesItsTypeAndSize() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("localhost:9092", "topic", 3, 1000, 100_000, 60_000, 8192);

        JsonNode json = mapper.valueToTree(spec);
        assertEquals(
                "constant", json.get("valueGenerator").get("type").asText(), "Trogdor refuses a generator without");
        assertEquals(8192, json.get("valueGenerator").get("size").asInt());
    }

    @Test
    void keyGeneratorSpecSerializesItsType() throws Exception {
        ProduceBenchSpec spec = ProduceBenchSpec.create("localhost:9092", "topic", 3, 1000, 100_000, 60_000, 8192);
        spec.setKeyGenerator(new ProduceBenchSpec.KeyGeneratorSpec());

        JsonNode json = mapper.valueToTree(spec);
        assertEquals("sequential", json.get("keyGenerator").get("type").asText());
        assertEquals(4, json.get("keyGenerator").get("size").asInt());
    }
}
