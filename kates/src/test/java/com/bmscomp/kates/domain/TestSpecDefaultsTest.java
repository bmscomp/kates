package com.bmscomp.kates.domain;

import static org.junit.jupiter.api.Assertions.*;

import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import java.util.LinkedHashMap;
import java.util.Map;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

class TestSpecDefaultsTest {

    @Test
    void defaultNumRecords() {
        assertEquals(1_000_000, new TestSpec().getNumRecords());
    }

    @Test
    void defaultRecordSize() {
        assertEquals(1024, new TestSpec().getRecordSize());
    }

    @Test
    void defaultThroughputIsUnlimited() {
        assertEquals(-1, new TestSpec().getThroughput());
    }

    @Test
    void defaultAcksIsAll() {
        assertEquals("all", new TestSpec().getAcks());
    }

    @Test
    void defaultBatchSize() {
        assertEquals(65536, new TestSpec().getBatchSize());
    }

    @Test
    void defaultLingerMs() {
        assertEquals(5, new TestSpec().getLingerMs());
    }

    @Test
    void defaultCompressionType() {
        assertEquals("lz4", new TestSpec().getCompressionType());
    }

    @Test
    void defaultReplicationFactor() {
        assertEquals(3, new TestSpec().getReplicationFactor());
    }

    @Test
    void defaultPartitions() {
        assertEquals(3, new TestSpec().getPartitions());
    }

    @Test
    void defaultMinInsyncReplicas() {
        assertEquals(2, new TestSpec().getMinInsyncReplicas());
    }

    @Test
    void defaultProducersAndConsumers() {
        TestSpec spec = new TestSpec();
        assertEquals(1, spec.getNumProducers());
        assertEquals(1, spec.getNumConsumers());
    }

    @Test
    void defaultDurationMs() {
        assertEquals(600_000, new TestSpec().getDurationMs());
    }

    @Test
    void defaultTopicIsNull() {
        assertNull(new TestSpec().getTopic());
    }

    @Test
    void newSpecHasNoUserValues() {
        TestSpec spec = new TestSpec();
        assertFalse(spec.hasNumRecords());
        assertFalse(spec.hasRecordSize());
        assertFalse(spec.hasThroughput());
        assertFalse(spec.hasAcks());
        assertFalse(spec.hasBatchSize());
        assertFalse(spec.hasLingerMs());
        assertFalse(spec.hasCompressionType());
        assertFalse(spec.hasNumProducers());
        assertFalse(spec.hasNumConsumers());
        assertFalse(spec.hasDurationMs());
        assertFalse(spec.hasReplicationFactor());
        assertFalse(spec.hasPartitions());
        assertFalse(spec.hasMinInsyncReplicas());
    }

    @Test
    void setterMarksFieldAsProvided() {
        TestSpec spec = new TestSpec();
        spec.setPartitions(3);
        assertTrue(spec.hasPartitions());
        assertEquals(3, spec.getPartitions());
    }

    @Test
    void setterValueMatchingDefaultIsStillDetected() {
        TestSpec spec = new TestSpec();
        spec.setReplicationFactor(3);
        assertTrue(spec.hasReplicationFactor());

        spec.setAcks("all");
        assertTrue(spec.hasAcks());

        spec.setCompressionType("lz4");
        assertTrue(spec.hasCompressionType());
    }

    @Test
    void integrityOptionsDefaultWithoutCountingAsSet() {
        TestSpec spec = new TestSpec();

        assertFalse(spec.isEnableIdempotence());
        assertFalse(spec.isEnableTransactions());
        assertTrue(spec.isEnableCrc());
        assertFalse(spec.hasEnableIdempotence());
        assertFalse(spec.hasEnableTransactions());
        assertFalse(spec.hasEnableCrc());
        assertFalse(spec.hasConsumerGroup());
    }

    @Test
    void anExplicitFalseCountsAsSet() {
        TestSpec spec = new TestSpec();
        spec.setEnableIdempotence(false);

        assertTrue(spec.hasEnableIdempotence(), "false is an instruction, not the absence of one");
        assertFalse(spec.isEnableIdempotence());
    }

    @Test
    void explicitFieldsHoldOnlyWhatWasSet() {
        TestSpec spec = new TestSpec();
        spec.setTargetThroughput(2000);
        spec.setEnableIdempotence(false);

        assertEquals(Map.of("targetThroughput", 2000, "enableIdempotence", false), spec.explicitFields());
        assertEquals(Map.of(), new TestSpec().explicitFields());
    }

    @Test
    void explicitFieldsCoverEveryField() throws Exception {
        // Deserialized the way a request is, with every field in it: each must
        // come back out, or explicitFields has fallen behind the class.
        Map<String, Object> all = new LinkedHashMap<>();
        for (Field f : TestSpec.class.getDeclaredFields()) {
            if (Modifier.isStatic(f.getModifiers())) {
                continue;
            }
            Class<?> t = f.getType();
            all.put(
                    f.getName(),
                    t == String.class
                            ? (f.getName().equals("acks") ? "1" : f.getName().equals("compressionType") ? "zstd" : "x")
                            : t == Boolean.class ? Boolean.FALSE : t == Long.class ? 5_000L : 7);
        }
        TestSpec spec = new ObjectMapper().convertValue(all, TestSpec.class);

        assertEquals(all.keySet(), spec.explicitFields().keySet());
    }

    @Test
    void aJsonNullLeavesAFieldUnset() throws Exception {
        TestSpec spec = new ObjectMapper()
                .readValue(
                        "{\"enableCrc\":null,\"enableIdempotence\":null,\"enableTransactions\":null}", TestSpec.class);

        assertEquals(Map.of(), spec.explicitFields(), "null is no instruction; false would turn CRC checks off");
        assertTrue(spec.isEnableCrc());
    }

    @Test
    void theJsonHoldsOnlyTheFieldsThatWereSet() throws Exception {
        TestSpec spec = new TestSpec();
        spec.setNumRecords(1000);
        spec.setEnableIdempotence(false);

        assertEquals("{\"numRecords\":1000,\"enableIdempotence\":false}", new ObjectMapper().writeValueAsString(spec));
    }
}
