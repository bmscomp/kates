package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.stream.Stream;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.IntegrityResult;
import com.bmscomp.kates.domain.IntegrityResultFixtures;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * An INTEGRITY run's integrity result reaches every poll after the run ends,
 * not only the one that saw it end. The orchestrator is a real one over the
 * real store, H2 behind TestRunRepository; only the backend is a stub.
 *
 * <p>The result was not stored, so the poll after the end, which reads a
 * finished run back from the database, had none. kates test apply --wait
 * then checked no integrity gate whenever the 5-second reconciler had made
 * the poll that saw the end.
 */
@QuarkusTest
class TestOrchestratorIntegrityReadBackTest {

    @Inject
    TestRunRepository repository;

    @Inject
    TestTypeDefaults typeDefaults;

    @Inject
    SpecLimits specLimits;

    @Test
    @SuppressWarnings("unchecked")
    void aPollAfterTheOneThatSawTheRunEndStillHasItsIntegrityResult() {
        IntegrityResult integrity = IntegrityResultFixtures.full();
        BenchmarkBackend backend = mock(BenchmarkBackend.class);
        when(backend.name()).thenReturn("native");
        when(backend.submit(any())).thenAnswer(invocation -> {
            BenchmarkTask task = invocation.getArgument(0);
            return new BenchmarkHandle("native", task.getTaskId());
        });
        when(backend.poll(any()))
                .thenReturn(BenchmarkStatus.builder(TestResult.TaskStatus.DONE)
                        .recordsProcessed(100_000)
                        .integrityResult(integrity)
                        .build());
        Instance<BenchmarkBackend> backends = mock(Instance.class);
        when(backends.stream()).thenAnswer(invocation -> Stream.of(backend));
        TestOrchestrator orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                repository,
                backends,
                typeDefaults,
                mock(BenchmarkMetrics.class),
                mock(KatesMetrics.class),
                new SlaEvaluator(),
                specLimits,
                mock(Event.class),
                "native",
                "localhost:9092",
                3,
                7_200_000L);
        TestSpec spec = orchestrator.applyTypeDefaults(TestType.INTEGRITY, null);
        TestRun run = new TestRun(TestType.INTEGRITY, spec).withBackend("native");
        repository.save(run);
        orchestrator.executeAsync(run, TestType.INTEGRITY, spec, "native", backend);

        TestRun sawTheEnd = orchestrator.refreshStatus(run.getId());
        assertEquals(TestResult.TaskStatus.DONE, sawTheEnd.getStatus());
        assertEquals(integrity, sawTheEnd.getResults().get(0).getIntegrity());

        // In a transaction of its own, so the read comes from the database and
        // not from the session's copy of the run.
        TestRun later = QuarkusTransaction.requiringNew().call(() -> orchestrator.refreshStatus(run.getId()));
        assertEquals(TestResult.TaskStatus.DONE, later.getStatus());
        assertEquals(
                integrity,
                later.getResults().get(0).getIntegrity(),
                "the poll after the end reads the finished run from the database");
    }
}
