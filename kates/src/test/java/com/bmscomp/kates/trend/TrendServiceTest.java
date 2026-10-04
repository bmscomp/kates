package com.bmscomp.kates.trend;

import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.is;
import static org.junit.jupiter.api.Assertions.*;

import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.List;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.RestAssured;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.report.ClusterSnapshot;
import com.bmscomp.kates.service.ClusterHealthService;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * Trends over runs stored in the database and read back as the trend
 * endpoints read them.
 *
 * <p>{@link TrendResourceTest} mocks this service, so nothing covered what it
 * reads. The runs came back without their task results: every point was 0, no
 * regression was ever flagged, and each run's report was left empty in the
 * report cache, where the report, JUnit, compare and regression endpoints
 * found it.
 */
@QuarkusTest
class TrendServiceTest {

    private static final String TOPIC = "trend-topic";

    @Inject
    TrendService trendService;

    @Inject
    TestRunRepository repository;

    @Inject
    EntityManager em;

    /** Mocked so a report's cluster snapshot never waits on a broker; the broker test gives it one. */
    @InjectMock
    ClusterHealthService clusterHealthService;

    @BeforeEach
    @Transactional
    void setUp() {
        em.createQuery("DELETE FROM TestResultEntity").executeUpdate();
        em.createQuery("DELETE FROM TestRunEntity").executeUpdate();
    }

    @Test
    void pointsCarryTheValuesOfEachRunsResults() {
        TestRun older =
                save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(3), task("p", null, 100_000, 10_000, 40));
        TestRun newer =
                save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(2), task("p", null, 100_000, 10_000, 44));
        TestRun slow = save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(1), task("p", null, 50_000, 4_000, 90));

        TrendResponse throughput = trendService.computeTrend(TestType.LOAD, "avgThroughputRecPerSec", 30, 5);

        assertEquals(
                List.of(older.getId(), newer.getId(), slow.getId()),
                throughput.dataPoints().stream()
                        .map(TrendResponse.DataPoint::runId)
                        .toList());
        assertEquals(List.of(10_000.0, 10_000.0, 4_000.0), values(throughput.dataPoints()));
        assertEquals(8_000.0, throughput.baseline(), 1e-9);
        // 4,000 is 50% under the baseline: a regression, which a trend of
        // zeros could never flag.
        assertEquals(
                List.of(slow.getId()),
                throughput.regressions().stream()
                        .map(TrendResponse.Regression::runId)
                        .toList());

        TrendResponse p99 = trendService.computeTrend(TestType.LOAD, "p99LatencyMs", 30, 5);
        assertEquals(List.of(40.0, 44.0, 90.0), values(p99.dataPoints()));
    }

    @Test
    void phaseTrendsReadEachPhasesOwnResults() {
        TestRun run = save(
                TestType.LOAD,
                TestResult.TaskStatus.DONE,
                daysAgo(1),
                task("warm", "warmup", 10_000, 1_000, 20),
                task("peak", "peak", 90_000, 9_000, 60));

        TrendResponse peak = trendService.computeTrend(TestType.LOAD, "avgThroughputRecPerSec", 30, 5, "PEAK");
        assertEquals(List.of(9_000.0), values(peak.dataPoints()));
        assertEquals(run.getId(), peak.dataPoints().get(0).runId());

        PhaseTrendResponse breakdown = trendService.computeBreakdown(TestType.LOAD, "p99LatencyMs", 30, 5);
        assertEquals(
                List.of("warmup", "peak"),
                breakdown.phases().stream()
                        .map(PhaseTrendResponse.PhaseTrend::phase)
                        .toList());
        assertEquals(List.of(20.0), values(breakdown.phases().get(0).dataPoints()));
        assertEquals(List.of(60.0), values(breakdown.phases().get(1).dataPoints()));

        assertEquals(List.of("warmup", "peak"), trendService.discoverPhases(TestType.LOAD, 30));
    }

    @Test
    void onlyTheTypesDoneRunsInTheWindowAreTrended() {
        TestRun done =
                save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(2), task("p", "steady", 100_000, 10_000, 40));
        // A FAILED run's numbers stop where it failed; one still running has
        // no final numbers yet. Neither belongs on the line.
        save(TestType.LOAD, TestResult.TaskStatus.FAILED, daysAgo(1), task("p", "aborted", 1_000, 100, 900));
        save(
                TestType.LOAD,
                TestResult.TaskStatus.RUNNING,
                Instant.now().minus(1, ChronoUnit.MINUTES),
                task("p", "ramping", 5_000, 500, 300));
        save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(45), task("p", "old", 100_000, 7_000, 40));
        save(TestType.STRESS, TestResult.TaskStatus.DONE, daysAgo(1), task("p", "other", 100_000, 3_000, 40));

        TrendResponse trend = trendService.computeTrend(TestType.LOAD, "avgThroughputRecPerSec", 30, 5);

        assertEquals(
                List.of(done.getId()),
                trend.dataPoints().stream().map(TrendResponse.DataPoint::runId).toList());
        assertEquals(List.of(10_000.0), values(trend.dataPoints()));
        assertEquals(List.of("steady"), trendService.discoverPhases(TestType.LOAD, 30));
        assertEquals(
                List.of("steady"),
                trendService.computeBreakdown(TestType.LOAD, "avgThroughputRecPerSec", 30, 5).phases().stream()
                        .map(PhaseTrendResponse.PhaseTrend::phase)
                        .toList());
    }

    @Test
    void aTrendLeavesEveryRunsReportWhole() {
        TestRun run = save(TestType.LOAD, TestResult.TaskStatus.DONE, daysAgo(1), task("p", null, 100_000, 10_000, 40));
        Mockito.when(clusterHealthService.captureSnapshot(TOPIC))
                .thenReturn(new ClusterSnapshot(
                        "cluster",
                        1,
                        0,
                        List.of(new ClusterSnapshot.BrokerInfo(0, "broker-0", 9092, null)),
                        List.of(new ClusterSnapshot.PartitionAssignment(TOPIC, 0, 0, List.of(0), List.of(0)))));

        trendService.computeTrend(TestType.LOAD, "avgThroughputRecPerSec", 30, 5);
        trendService.computeBreakdown(TestType.LOAD, "avgThroughputRecPerSec", 30, 5);
        trendService.discoverPhases(TestType.LOAD, 30);
        BrokerTrendResponse broker = trendService.computeBrokerTrend(TestType.LOAD, "avgThroughputRecPerSec", 0, 30, 5);

        RestAssured.given()
                .when()
                .get("/api/tests/" + run.getId() + "/report")
                .then()
                .statusCode(200)
                .body("summary.totalRecords", is(100_000))
                .body("summary.avgThroughputRecPerSec", is(10_000.0f));
        RestAssured.given()
                .when()
                .get("/api/tests/" + run.getId() + "/report/junit")
                .then()
                .statusCode(200)
                .body(containsString("tests=\"1\""));

        // The broker trend is built from the runs' reports, so it had no point
        // for a run whose report had no results.
        assertEquals(List.of(10_000.0), values(broker.getDataPoints()));
    }

    private TestRun save(TestType type, TestResult.TaskStatus status, Instant createdAt, TestResult... results) {
        TestSpec spec = new TestSpec();
        spec.setTopic(TOPIC);
        TestRun run = new TestRun(type, spec)
                .withStatus(status)
                .withCreatedAt(createdAt.toString())
                .withResults(List.of(results));
        repository.save(run);
        return run;
    }

    private static TestResult task(String taskId, String phase, long records, double recPerSec, double p99Ms) {
        return new TestResult()
                .withTaskId(taskId + "-" + System.nanoTime())
                .withTestType(TestType.LOAD)
                .withStatus(TestResult.TaskStatus.DONE)
                .withRecordsSent(records)
                .withThroughputRecordsPerSec(recPerSec)
                .withAvgLatencyMs(p99Ms / 2)
                .withP50LatencyMs(p99Ms / 2)
                .withP95LatencyMs(p99Ms * 0.9)
                .withP99LatencyMs(p99Ms)
                .withMaxLatencyMs(p99Ms * 1.5)
                .withPhaseName(phase);
    }

    private static Instant daysAgo(int days) {
        return Instant.now().minus(days, ChronoUnit.DAYS);
    }

    private static List<Double> values(List<TrendResponse.DataPoint> points) {
        return points.stream().map(TrendResponse.DataPoint::value).toList();
    }
}
