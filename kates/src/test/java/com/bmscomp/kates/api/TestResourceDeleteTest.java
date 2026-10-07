package com.bmscomp.kates.api;

import static com.bmscomp.kates.engine.InMemoryEngine.taskIds;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.verifyNoMoreInteractions;
import static org.mockito.Mockito.when;

import java.time.Instant;
import java.util.ArrayList;
import java.util.EnumSet;
import java.util.List;
import java.util.Set;
import jakarta.ws.rs.core.Response;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.BulkDeleteRequest;
import com.bmscomp.kates.domain.BulkDeleteResponse;
import com.bmscomp.kates.domain.PruneResponse;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.service.AuditService;

/**
 * Deleting runs that are still going, through DELETE /api/tests/{id} and
 * DELETE /api/tests/bulk. The single delete stopped a run's tasks but kept
 * its concurrency permit, and the bulk delete removed the row and nothing
 * else, so a few deletes of running runs made every new run answer 429 until
 * a restart. Neither said the run had ended. DELETE /api/tests prunes
 * through the same delete.
 */
class TestResourceDeleteTest {

    private final InMemoryEngine engine = new InMemoryEngine();
    private final AuditService audit = mock(AuditService.class);
    private TestResource resource;

    @BeforeEach
    void setUp() {
        resource = new TestResource(engine.orchestrator, engine.repository, null);
        resource.auditService = audit;
    }

    @Test
    void aDeleteStopsTheRunAndGivesBackItsSlot() {
        List<String> ids = engine.fill();

        for (String id : ids) {
            assertEquals(204, resource.deleteTest(id).getStatus());
            assertTrue(engine.backend.stopped.containsAll(taskIds(id)), "the tasks of " + id + " are stopped");
            assertFalse(engine.rows.containsKey(id));
            engine.assertAnnouncedDeleted(id);
            verify(audit).record("DELETE", "test", id, "Test deleted");
        }
        verify(engine.katesMetrics, times(ids.size())).recordTestCompleted("LOAD", "failed");

        engine.assertEverySlotFree();
        assertEquals(404, resource.deleteTest(ids.get(0)).getStatus());
    }

    @Test
    void aBulkDeleteStopsEveryRunAndGivesBackTheirSlots() {
        List<String> ids = engine.fill();
        String done = engine.storeEnded(TaskStatus.DONE);
        List<String> requested = new ArrayList<>(ids);
        requested.add(done);
        requested.add("no-such-run");

        Response response = resource.bulkDelete(new BulkDeleteRequest(requested));

        assertEquals(200, response.getStatus());
        assertEquals(new BulkDeleteResponse(ids.size() + 1, 1), response.getEntity());
        for (String id : ids) {
            assertTrue(engine.backend.stopped.containsAll(taskIds(id)), "the tasks of " + id + " are stopped");
            assertFalse(engine.rows.containsKey(id));
            engine.assertAnnouncedDeleted(id);
            verify(audit).record("DELETE", "test", id, "bulk delete");
        }
        assertFalse(engine.rows.containsKey(done));
        assertTrue(engine.failuresOf(done).isEmpty(), "a run that had ended is not announced again");
        verify(engine.katesMetrics, times(ids.size())).recordTestCompleted("LOAD", "failed");
        engine.assertEverySlotFree();
    }

    /**
     * A run another delete took between the prune reading the ids and
     * deleting them is not counted, and gets no audit row of the prune's.
     */
    @Test
    void aPruneCountsOnlyTheRunsItDeleted() {
        String done = engine.storeEnded(TaskStatus.DONE);
        Instant cutoff = Instant.parse("2026-09-07T00:00:00Z");
        Set<TaskStatus> finished = EnumSet.of(TaskStatus.DONE, TaskStatus.FAILED);
        when(engine.repository.countByStatusCreatedBefore(finished, cutoff)).thenReturn(2L, 0L);
        when(engine.repository.findIdsByStatusCreatedBefore(finished, cutoff, 1000))
                .thenReturn(List.of("taken-by-another-delete", done));

        Response response = resource.pruneTests(cutoff.toString(), List.of(), null, null);

        assertEquals(200, response.getStatus());
        assertEquals(
                new PruneResponse("2026-09-07T00:00:00Z", List.of("DONE", "FAILED"), false, 2, 1, 0),
                response.getEntity());
        assertFalse(engine.rows.containsKey(done));
        verify(audit).record("DELETE", "test", done, "retention: created before 2026-09-07T00:00:00Z");
        verifyNoMoreInteractions(audit);
    }
}
