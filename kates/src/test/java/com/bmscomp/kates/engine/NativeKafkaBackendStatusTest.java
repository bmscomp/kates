package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * Pins the outcome of a task that finished without throwing.
 *
 * <p>"The workload method returned" used to be the entire success criterion, so
 * anything that failed quietly was reported as a pass. A native end-to-end run
 * showed both halves of that at once: a produce task at 0 records/s marked
 * FAILED, and next to it the consume task for the same run marked DONE, also at
 * 0 records/s. It had polled an empty topic for its full duration and returned
 * normally, which was all that was ever asked of it.
 */
class NativeKafkaBackendStatusTest {

    /** Security and CDC are untouched by the status decision. */
    private final NativeKafkaBackend backend = new NativeKafkaBackend("localhost:9092", null, null);

    /** Unique per task: the latency gauges are tagged with the task id. */
    private static final java.util.concurrent.atomic.AtomicInteger SEQ =
            new java.util.concurrent.atomic.AtomicInteger();

    private static BenchmarkTask task(BenchmarkTask.WorkloadType type) {
        return BenchmarkTask.builder("run-1-" + type + "-" + SEQ.incrementAndGet(), type)
                .runId("run-1")
                .topic("kates-run-1")
                .maxMessages(1000)
                .durationMs(60_000)
                .build();
    }

    private NativeKafkaBackend.WorkerState finish(
            BenchmarkTask.WorkloadType type, long processed, long failedSends, boolean stopped) {
        BenchmarkTask task = task(type);
        NativeKafkaBackend.WorkerState state = new NativeKafkaBackend.WorkerState(task);
        state.status = TaskStatus.RUNNING;
        state.recordsProcessed.set(processed);
        state.errors.set(failedSends);
        if (failedSends > 0) {
            state.recordSendError(new org.apache.kafka.common.errors.TimeoutException("expired"));
        }
        state.stopRequested.set(stopped);
        backend.applyPostConditions(task, state);
        return state;
    }

    @Test
    @DisplayName("a consumer that received nothing has not succeeded")
    void consumerWithNoRecordsFails() {
        NativeKafkaBackend.WorkerState state =
                finish(BenchmarkTask.WorkloadType.CONSUME, 0, 0, false);

        assertEquals(TaskStatus.FAILED, state.status);
        assertNotNull(state.error, "a failure with no explanation is barely better than a false pass");
        assertTrue(state.error.contains("kates-run-1"), state.error);
    }

    @Test
    @DisplayName("a consumer that received records succeeds")
    void consumerWithRecordsSucceeds() {
        NativeKafkaBackend.WorkerState state =
                finish(BenchmarkTask.WorkloadType.CONSUME, 1000, 0, false);

        assertEquals(TaskStatus.DONE, state.status);
        assertNull(state.error);
    }

    @Test
    @DisplayName("a consumer told to stop early is obeying, not failing")
    void stoppedConsumerIsNotAFailure() {
        NativeKafkaBackend.WorkerState state = finish(BenchmarkTask.WorkloadType.CONSUME, 0, 0, true);

        assertEquals(TaskStatus.DONE, state.status);
    }

    @Test
    @DisplayName("a producer whose every send was rejected has not succeeded")
    void producerWithAllSendsRejectedFails() {
        // Send failures arrive on a callback. They were counted and never read
        // again, so this was indistinguishable from a clean run.
        NativeKafkaBackend.WorkerState state = finish(BenchmarkTask.WorkloadType.PRODUCE, 500, 500, false);

        assertEquals(TaskStatus.FAILED, state.status);
        assertTrue(state.error.contains("expired"), "the broker's reason should survive: " + state.error);
    }

    @Test
    @DisplayName("partial send loss is a result, not a broken run — but it is recorded")
    void producerWithSomeSendsRejectedStillPasses() {
        NativeKafkaBackend.WorkerState state = finish(BenchmarkTask.WorkloadType.PRODUCE, 1000, 3, false);

        assertEquals(TaskStatus.DONE, state.status);
        assertTrue(state.error.contains("3 of 1000"), state.error);
    }

    @Test
    @DisplayName("a clean producer run stays clean")
    void producerWithNoErrorsSucceeds() {
        NativeKafkaBackend.WorkerState state = finish(BenchmarkTask.WorkloadType.PRODUCE, 1000, 0, false);

        assertEquals(TaskStatus.DONE, state.status);
        assertNull(state.error);
    }

    @Test
    @DisplayName("CDC keeps the status its own service decided")
    void integrityCdcStatusIsLeftAlone() {
        BenchmarkTask task = task(BenchmarkTask.WorkloadType.INTEGRITY_CDC);
        NativeKafkaBackend.WorkerState state = new NativeKafkaBackend.WorkerState(task);
        state.status = TaskStatus.RUNNING;

        backend.applyPostConditions(task, state);

        // runIntegrityCdc copies the CDC service's own verdict; nothing here
        // knows better than it does.
        assertEquals(TaskStatus.DONE, state.status);
    }

    @Test
    @DisplayName("an unknown task is not a finished task")
    void pollingAnUnknownTaskDoesNotReportSuccess() {
        // This returned DONE, so a handle from a process that had since
        // restarted read as a passed run with nothing in it.
        BenchmarkStatus status = backend.poll(new BenchmarkHandle("native", "gone-1"));

        assertEquals(TaskStatus.FAILED, status.getState());
        assertTrue(status.getError().contains("gone-1"), status.getError());
    }

    @Test
    @DisplayName("a handle still answers after its worker is evicted from the map")
    void pollFallsBackToTheHandlesOwnState() {
        // Completed workers are evicted once MAX_RETAINED_COMPLETED is passed.
        // The handle has held the state all along, so the answer is not lost.
        BenchmarkTask task = task(BenchmarkTask.WorkloadType.PRODUCE);
        NativeKafkaBackend.WorkerState state = new NativeKafkaBackend.WorkerState(task);
        state.status = TaskStatus.DONE;
        state.recordsProcessed.set(4200);

        BenchmarkStatus status = backend.poll(new BenchmarkHandle("native", task.getTaskId(), state));

        assertEquals(TaskStatus.DONE, status.getState());
        assertEquals(4200, status.getRecordsProcessed());
    }
}
