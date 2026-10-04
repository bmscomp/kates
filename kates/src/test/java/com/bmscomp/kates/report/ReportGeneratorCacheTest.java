package com.bmscomp.kates.report;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import java.util.List;

import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.KatesMetrics;
import com.bmscomp.kates.engine.SlaEvaluator;
import com.bmscomp.kates.export.JunitXmlExporter;
import com.bmscomp.kates.service.ClusterHealthService;

/**
 * Which reports the generator keeps: a finished run's, and only when the run
 * carries its results. A copy read without them, as every list query returns
 * a run, used to leave an all-zero report under the run's id, which then
 * answered for the run until it was evicted.
 */
class ReportGeneratorCacheTest {

    private final ReportGenerator generator = new ReportGenerator();

    private final TestRun run = new TestRun(TestType.LOAD, new TestSpec())
            .withStatus(TestResult.TaskStatus.DONE)
            .withResults(List.of(new TestResult()
                    .withTaskId("producer-0")
                    .withTestType(TestType.LOAD)
                    .withStatus(TestResult.TaskStatus.DONE)
                    .withRecordsSent(100_000)
                    .withThroughputRecordsPerSec(10_000)
                    .withP99LatencyMs(40)));

    @BeforeEach
    void wire() {
        generator.clusterHealthService = mock(ClusterHealthService.class);
        generator.katesMetrics = new KatesMetrics(new SimpleMeterRegistry());
        generator.slaEvaluator = new SlaEvaluator();
    }

    @Test
    void aFinishedRunReadWithoutItsResultsLeavesNoReportBehind() {
        TestRun listed = run.withResults(List.of());
        assertEquals(0, generator.generate(listed).getSummary().totalRecords());

        TestReport report = generator.generate(run);

        assertEquals(100_000, report.getSummary().totalRecords());
        assertEquals(10_000, report.getSummary().avgThroughputRecPerSec());
        // The JUnit export counts the report's run's results: the empty copy
        // read tests="0" failures="0", which CI takes for a pass.
        assertTrue(new JunitXmlExporter().export(report).contains("tests=\"1\""));
    }

    @Test
    void aFinishedRunsReportIsBuiltOnce() {
        assertSame(generator.generate(run), generator.generate(run));
    }

    @Test
    void aRunStillInFlightIsBuiltEachTime() {
        TestRun running = run.withStatus(TestResult.TaskStatus.RUNNING);

        assertNotSame(generator.generate(running), generator.generate(running));
    }
}
