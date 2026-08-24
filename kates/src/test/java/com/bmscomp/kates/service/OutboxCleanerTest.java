package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import java.util.UUID;
import jakarta.persistence.EntityManager;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.persistence.OutboxDeadLetterEntity;
import com.bmscomp.kates.persistence.OutboxEventEntity;

/**
 * Covers the retirement path that stops an unpublishable event from being
 * retried on every poll forever while holding a slot in the poll window.
 */
class OutboxCleanerTest {

    private OutboxCleaner cleaner;
    private EntityManager em;
    private UUID id;
    private OutboxEventEntity event;

    @BeforeEach
    void setUp() {
        cleaner = new OutboxCleaner();
        em = mock(EntityManager.class);
        cleaner.em = em;
        cleaner.maxAttempts = 3;

        id = UUID.randomUUID();
        event = new OutboxEventEntity("run-1", "TestRun", "test.lifecycle", "{\"broken\":");
        when(em.find(OutboxEventEntity.class, id)).thenReturn(event);
    }

    @Test
    @DisplayName("a failure below the limit only counts the attempt")
    void countsAttemptsBelowLimit() {
        boolean retired = cleaner.recordFailure(id, "boom");

        assertFalse(retired);
        assertEquals(1, event.getAttempts());
        assertEquals("boom", event.getLastError());
        verify(em, never()).remove(any());
        verify(em, never()).persist(any());
    }

    @Test
    @DisplayName("the event is dead-lettered, not dropped, once attempts run out")
    void retiresToDeadLettersAtLimit() {
        cleaner.recordFailure(id, "boom");
        cleaner.recordFailure(id, "boom");
        boolean retired = cleaner.recordFailure(id, "final boom");

        assertTrue(retired);
        verify(em).persist(any(OutboxDeadLetterEntity.class));
        verify(em).remove(event);
    }

    @Test
    @DisplayName("a very long error is truncated before it is stored")
    void truncatesLongErrors() {
        cleaner.recordFailure(id, "x".repeat(5_000));

        assertTrue(event.getLastError().length() <= 2_001, "stored errors must not be unbounded");
    }

    @Test
    @DisplayName("an already-published event is a no-op")
    void missingEventIsHarmless() {
        UUID unknown = UUID.randomUUID();
        when(em.find(OutboxEventEntity.class, unknown)).thenReturn(null);

        assertFalse(cleaner.recordFailure(unknown, "boom"));
    }

    @Test
    @DisplayName("a database failure never propagates into the messaging layer")
    void swallowsPersistenceErrors() {
        UUID broken = UUID.randomUUID();
        when(em.find(OutboxEventEntity.class, broken)).thenThrow(new IllegalStateException("no connection"));

        assertDoesNotThrow(() -> cleaner.recordFailure(broken, "boom"));
    }
}
