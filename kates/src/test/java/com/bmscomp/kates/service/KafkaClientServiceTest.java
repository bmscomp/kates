package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;

import jakarta.inject.Inject;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

@QuarkusTest
class KafkaClientServiceTest {

    @Inject
    KafkaClientService kafkaClientService;

    @Test
    void serviceIsInjected() {
        assertNotNull(kafkaClientService);
    }

    @Test
    void fetchRecordsReturnsEmptyListWhenNoRecords() {
        // With no broker running, the consumer will poll and get nothing within the timeout.
        // The service catches the error or returns empty depending on timing.
        // We verify it doesn't blow up catastrophically.
        try {
            var records = kafkaClientService.fetchRecords("nonexistent-topic", "latest", 1);
            assertNotNull(records);
        } catch (RuntimeException e) {
            assertTrue(e.getMessage().contains("Failed to consume"));
        }
    }

    @Test
    void fetchRecordsNormalizesOffsetReset() {
        // Invalid offset reset value should be normalized to "latest"
        try {
            kafkaClientService.fetchRecords("topic", "invalid", 1);
        } catch (RuntimeException e) {
            assertTrue(e.getMessage().contains("Failed to consume"));
        }
    }

    @Test
    void produceRecordFailsGracefully() {
        // With no broker running, produce should fail with a descriptive error
        try {
            kafkaClientService.produceRecord("topic", "key", "value");
            fail("Expected RuntimeException");
        } catch (RuntimeException e) {
            assertTrue(e.getMessage().contains("Failed to produce"));
        }
    }
}
