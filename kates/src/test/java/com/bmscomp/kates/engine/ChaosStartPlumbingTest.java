package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.argThat;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * The path a fault's start time takes from a resilience run to the integrity
 * verifier: orchestrator, backend, worker. The verifier was always handed -1
 * because nothing carried it, so RPO had nothing to measure from.
 */
class ChaosStartPlumbingTest {

    @Test
    void nativeWorkerKeepsTheFirstFaultItIsTold() {
        NativeKafkaBackend backend = new NativeKafkaBackend("localhost:9092", null, null);
        BenchmarkTask task = BenchmarkTask.builder("run-1-integrity-0", BenchmarkTask.WorkloadType.INTEGRITY)
                .runId("run-1")
                .topic("integrity-test")
                .maxMessages(1000)
                .build();
        NativeKafkaBackend.WorkerState state = new NativeKafkaBackend.WorkerState(task);
        BenchmarkHandle handle = new BenchmarkHandle("native", task.getTaskId(), state);

        assertEquals(-1, state.chaosStartNanos.get(), "unmarked until a fault is injected");

        backend.markChaosStart(handle, 0);
        assertEquals(-1, state.chaosStartNanos.get(), "not a nanoTime reading");

        backend.markChaosStart(handle, 5_000L);
        backend.markChaosStart(handle, 9_000L);
        assertEquals(5_000L, state.chaosStartNanos.get(), "RPO is measured from the first fault");

        // A handle the backend cannot resolve is ignored rather than thrown on.
        backend.markChaosStart(new BenchmarkHandle("native", "unknown"), 7_000L);
    }

    @Test
    @SuppressWarnings("unchecked")
    void orchestratorForwardsTheFaultToEveryLiveTaskOfTheRun() {
        BenchmarkBackend backend = mock(BenchmarkBackend.class);
        when(backend.name()).thenReturn("native");
        when(backend.submit(any()))
                .thenAnswer(invocation -> new BenchmarkHandle(
                        "native", invocation.<BenchmarkTask>getArgument(0).getTaskId()));
        Instance<BenchmarkBackend> backends = mock(Instance.class);
        when(backends.stream()).thenAnswer(invocation -> Stream.of(backend));

        TestOrchestrator orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                mock(TestRunRepository.class),
                backends,
                new TestTypeDefaults(),
                mock(BenchmarkMetrics.class),
                mock(KatesMetrics.class),
                new SlaEvaluator(),
                mock(Event.class),
                "native",
                "localhost:9092",
                3);

        TestSpec spec = new TestSpec();
        spec.setNumRecords(1000);
        spec.setPartitions(3);
        spec.setAcks("all");
        spec.setBatchSize(16384);
        spec.setLingerMs(0);
        spec.setCompressionType("none");
        spec.setRecordSize(128);
        spec.setDurationMs(60_000);
        TestRun run = new TestRun(TestType.INTEGRITY, spec).withBackend("native");
        orchestrator.executeAsync(run, TestType.INTEGRITY, spec, "native", backend);

        orchestrator.markChaosStart(run.getId(), 42L);
        orchestrator.markChaosStart("some-other-run", 43L);

        verify(backend).markChaosStart(argThat(h -> h.taskId().equals(run.getId() + "-integrity-0")), eq(42L));
        verify(backend, never()).markChaosStart(any(), eq(43L));
    }
}
