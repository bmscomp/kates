package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;


import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import jakarta.inject.Inject;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

@QuarkusTest
class AdvisorServiceTest {

    @InjectMock
    ClusterHealthService clusterHealthService;

    @Inject
    AdvisorService advisorService;

    private TestRun createRunWithMetrics(TestSpec spec, double throughput, double p99) {
        TestRun run = new TestRun(TestType.LOAD, spec);
        TestResult result = new TestResult()
                .withThroughputRecordsPerSec(throughput)
                .withP99LatencyMs(p99);
        return run.withAddedResult(result);
    }

    @Test
    void noRecommendationsForNullSpec() {
        TestRun run = new TestRun(TestType.LOAD, null);
        var recs = advisorService.analyze(run);
        assertTrue(recs.isEmpty());
    }

    @Test
    void noRecommendationsForEmptyResults() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        var recs = advisorService.analyze(run);
        assertTrue(recs.isEmpty());
    }

    @Test
    void detectsSmallBatchSizeWithHighThroughput() {
        when(clusterHealthService.brokerCount()).thenReturn(3);
        TestSpec spec = new TestSpec();
        spec.setBatchSize(16384);
        spec.setCompressionType("lz4");
        spec.setPartitions(12);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 50_000, 10);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("batch.size") && "HIGH".equals(r.severity())));
    }

    @Test
    void detectsZeroLingerWithHighThroughput() {
        when(clusterHealthService.brokerCount()).thenReturn(3);
        TestSpec spec = new TestSpec();
        spec.setLingerMs(0);
        spec.setCompressionType("lz4");
        spec.setPartitions(12);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 10_000, 5);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("linger.ms=0")));
    }

    @Test
    void detectsReplicationFactorExceedsBrokers() {
        when(clusterHealthService.brokerCount()).thenReturn(2);
        TestSpec spec = new TestSpec();
        spec.setReplicationFactor(3);
        spec.setCompressionType("lz4");
        spec.setPartitions(6);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 1000, 5);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("exceeds cluster size") && "HIGH".equals(r.severity())));
    }

    @Test
    void detectsNoCompression() {
        when(clusterHealthService.brokerCount()).thenReturn(3);
        TestSpec spec = new TestSpec();
        spec.setCompressionType("none");
        spec.setPartitions(12);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 30_000, 5);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("compression")));
    }

    @Test
    void detectsGzipCompression() {
        when(clusterHealthService.brokerCount()).thenReturn(3);
        TestSpec spec = new TestSpec();
        spec.setCompressionType("gzip");
        spec.setPartitions(12);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 1000, 5);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("gzip") && "MED".equals(r.severity())));
    }

    @Test
    void detectsHighP99WithLargeBatch() {
        when(clusterHealthService.brokerCount()).thenReturn(3);
        TestSpec spec = new TestSpec();
        spec.setBatchSize(131072);
        spec.setCompressionType("lz4");
        spec.setPartitions(12);
        spec.setNumProducers(1);

        TestRun run = createRunWithMetrics(spec, 5000, 200);
        var recs = advisorService.analyze(run);

        assertTrue(recs.stream().anyMatch(r ->
                r.title().contains("p99") && r.title().contains("batch.size")));
    }
}
