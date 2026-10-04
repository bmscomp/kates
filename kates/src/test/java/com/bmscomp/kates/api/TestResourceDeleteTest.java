package com.bmscomp.kates.api;

import static com.bmscomp.kates.engine.InMemoryEngine.taskIds;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;

import java.util.ArrayList;
import java.util.List;
import jakarta.ws.rs.core.Response;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.BulkDeleteRequest;
import com.bmscomp.kates.domain.BulkDeleteResponse;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.service.AuditService;

/**
 * Deleting runs that are still going, through DELETE /api/tests/{id} and
 * DELETE /api/tests/bulk. The single delete stopped a run's tasks but kept
 * its concurrency permit, and the bulk delete removed the row and nothing
 * else, so a few deletes of running runs made every new run answer 429 until
 * a restart. Neither said the run had ended.
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
}
