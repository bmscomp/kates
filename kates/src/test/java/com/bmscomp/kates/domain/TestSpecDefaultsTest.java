package com.bmscomp.kates.domain;

import static org.junit.jupiter.api.Assertions.*;

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
}
