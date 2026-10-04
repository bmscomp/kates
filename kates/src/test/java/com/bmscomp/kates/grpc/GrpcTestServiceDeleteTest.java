package com.bmscomp.kates.grpc;

import static com.bmscomp.kates.engine.InMemoryEngine.taskIds;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;

import java.util.List;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.grpc.proto.DeleteTestRequest;

/**
 * DeleteTest settles a run that is still going before deleting it, and
 * announces its end, as the REST delete does. It used to remove the row and
 * nothing else: the run's workers kept producing, its concurrency permit
 * stayed taken, so a few deletes made every new run answer 429 until a
 * restart, and nothing said the run had ended.
 */
class GrpcTestServiceDeleteTest {

    private final InMemoryEngine engine = new InMemoryEngine();
    private final GrpcTestService service = new GrpcTestService();

    @BeforeEach
    void setUp() {
        service.orchestrator = engine.orchestrator;
        service.repository = engine.repository;
    }

    @Test
    void aDeleteStopsTheRunAndGivesBackItsSlot() {
        List<String> ids = engine.fill();

        for (String id : ids) {
            service.deleteTest(request(id)).await().indefinitely();
            assertTrue(engine.backend.stopped.containsAll(taskIds(id)), "the tasks of " + id + " are stopped");
            assertFalse(engine.rows.containsKey(id));
            engine.assertAnnouncedDeleted(id);
        }
        verify(engine.katesMetrics, times(ids.size())).recordTestCompleted("LOAD", "failed");

        engine.assertEverySlotFree();
    }

    @Test
    void anUnknownRunIsNotFound() {
        var e = assertThrows(
                StatusRuntimeException.class,
                () -> service.deleteTest(request("no-such-run")).await().indefinitely());
        assertEquals(Status.Code.NOT_FOUND, e.getStatus().getCode());
    }

    private static DeleteTestRequest request(String id) {
        return DeleteTestRequest.newBuilder().setId(id).build();
    }
}
