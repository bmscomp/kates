package com.bmscomp.kates.grpc;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.Optional;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.RunNotCancellableException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.grpc.proto.CancelTestRequest;
import com.bmscomp.kates.grpc.proto.TestStatus;

/**
 * CancelTest answers with the run as the shared cancel stores it. It used to
 * call only {@code stopTest}, answer the resulting STOPPING as CANCELLED, and
 * mark a finished run STOPPING as well.
 */
class GrpcTestServiceCancelTest {

    private GrpcTestService service;
    private TestOrchestrator orchestrator;

    @BeforeEach
    void setUp() {
        orchestrator = mock(TestOrchestrator.class);
        service = new GrpcTestService();
        service.orchestrator = orchestrator;
    }

    @Test
    void answersTheRunAsStored() {
        TestRun stored = new TestRun(TestType.LOAD, null).withId("r1").withStatus(TestResult.TaskStatus.FAILED);
        when(orchestrator.cancelTest("r1")).thenReturn(Optional.of(stored));

        var answer = service.cancelTest(request("r1")).await().indefinitely();

        assertEquals("r1", answer.getId());
        assertEquals(TestStatus.FAILED, answer.getStatus());
    }

    @Test
    void anUnknownRunIsNotFound() {
        when(orchestrator.cancelTest("nope")).thenReturn(Optional.empty());

        var e = assertThrows(
                StatusRuntimeException.class,
                () -> service.cancelTest(request("nope")).await().indefinitely());
        assertEquals(Status.Code.NOT_FOUND, e.getStatus().getCode());
    }

    @Test
    void aRunThatIsNotRunningIsAFailedPrecondition() {
        when(orchestrator.cancelTest("done")).thenThrow(new RunNotCancellableException(TestResult.TaskStatus.DONE));

        var e = assertThrows(
                StatusRuntimeException.class,
                () -> service.cancelTest(request("done")).await().indefinitely());
        assertEquals(Status.Code.FAILED_PRECONDITION, e.getStatus().getCode());
    }

    @Test
    void stoppingIsNotReportedAsAnEndState() {
        // A cancel under way ends FAILED, never in a CANCELLED state.
        assertEquals(TestStatus.RUNNING, ProtoMapper.toProtoStatus(TestResult.TaskStatus.STOPPING));
    }

    private static CancelTestRequest request(String id) {
        return CancelTestRequest.newBuilder().setId(id).build();
    }
}
