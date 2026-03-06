package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;

/**
 * Server-side rule engine that analyzes test results and cluster state
 * to produce actionable configuration recommendations.
 */
@ApplicationScoped
public class AdvisorService {

    @Inject
    ClusterHealthService clusterHealthService;

    public record Recommendation(String severity, String title, String fix, String evidence) {}

    public List<Recommendation> analyze(TestRun run) {
        var rules = new ArrayList<Recommendation>();

        if (run.getSpec() == null || run.getResults().isEmpty()) {
            return rules;
        }

        TestSpec spec = run.getSpec();
        double avgThroughput = 0, avgP99 = 0;
        int count = 0;
        for (TestResult r : run.getResults()) {
            avgThroughput += r.getThroughputRecordsPerSec();
            avgP99 += r.getP99LatencyMs();
            count++;
        }
        if (count > 0) {
            avgThroughput /= count;
            avgP99 /= count;
        }

        int clusterBrokers = clusterHealthService.brokerCount();

        if (spec.getBatchSize() > 0 && spec.getBatchSize() <= 16384 && avgThroughput > 10000) {
            rules.add(new Recommendation(
                    "HIGH",
                    String.format("batch.size=%d is leaving throughput on the table", spec.getBatchSize()),
                    "Try batch.size=65536 for improved batching efficiency",
                    String.format("current throughput: %.0f rec/s, estimated gain: ~30-50%%", avgThroughput)));
        }

        if (spec.getLingerMs() == 0 && avgThroughput > 5000) {
            rules.add(new Recommendation(
                    "HIGH",
                    "linger.ms=0 causes excessive small-batch sends",
                    "Set linger.ms=10 to coalesce batches and reduce request count",
                    "zero linger forces immediate sends, increasing network overhead"));
        }

        String acks = spec.getAcks();
        if (("1".equals(acks) || "0".equals(acks)) && spec.getReplicationFactor() >= 3) {
            String sev = "0".equals(acks) ? "HIGH" : "MED";
            rules.add(new Recommendation(
                    sev,
                    String.format(
                            "acks=%s with replicationFactor=%d risks data loss", acks, spec.getReplicationFactor()),
                    "Use acks=all for data durability with high replication",
                    String.format(
                            "replication=%d provides redundancy, but acks=%s bypasses it",
                            spec.getReplicationFactor(), acks)));
        }

        String compression = spec.getCompressionType();
        if ("none".equals(compression) || compression == null || compression.isEmpty()) {
            if (avgThroughput > 20000) {
                rules.add(new Recommendation(
                        "HIGH",
                        "No compression detected — high bandwidth usage",
                        "Use compression=lz4 for best throughput or zstd for best ratio",
                        String.format("%.0f rec/s uncompressed wastes ~30-60%% network bandwidth", avgThroughput)));
            } else {
                rules.add(new Recommendation(
                        "MED",
                        "No compression — consider enabling for network efficiency",
                        "compression=lz4 adds negligible CPU overhead",
                        null));
            }
        } else if ("gzip".equals(compression)) {
            rules.add(new Recommendation(
                    "MED",
                    "gzip compression has highest CPU overhead",
                    "Switch to lz4 for 3-5x faster compression at similar ratios",
                    "gzip is best for cold storage, lz4/zstd for streaming"));
        } else {
            rules.add(new Recommendation(
                    "OK", String.format("compression=%s is optimal for this workload", compression), null, null));
        }

        if (spec.getPartitions() > 0 && spec.getNumProducers() > 0) {
            double ratio = (double) spec.getPartitions() / spec.getNumProducers();
            if (ratio < 2) {
                rules.add(new Recommendation(
                        "MED",
                        String.format(
                                "partitions=%d with %d producers limits parallelism",
                                spec.getPartitions(), spec.getNumProducers()),
                        String.format(
                                "Increase partitions to %d (4× producers) for better distribution",
                                spec.getNumProducers() * 4),
                        String.format("partition:producer ratio is %.1f (recommended: ≥ 4)", ratio)));
            } else {
                rules.add(new Recommendation(
                        "OK",
                        String.format("partitions=%d matches producer count well", spec.getPartitions()),
                        null,
                        null));
            }
        }

        if (spec.getRecordSize() > 0 && spec.getRecordSize() < 256 && avgThroughput > 50000) {
            rules.add(new Recommendation(
                    "MED",
                    String.format("recordSize=%dB is small — high per-record overhead", spec.getRecordSize()),
                    "Batch application records or increase record size to reduce overhead",
                    "small records amplify per-message metadata costs"));
        }

        if (avgP99 > 100 && spec.getBatchSize() > 65536) {
            rules.add(new Recommendation(
                    "MED",
                    String.format("p99=%.0fms with large batch.size=%d — try reducing", avgP99, spec.getBatchSize()),
                    "Reduce batch.size or linger.ms to trade throughput for latency",
                    "large batches increase fill time, raising tail latency"));
        }

        if (clusterBrokers > 0 && spec.getReplicationFactor() > clusterBrokers) {
            rules.add(new Recommendation(
                    "HIGH",
                    String.format(
                            "replicationFactor=%d exceeds cluster size (%d brokers)",
                            spec.getReplicationFactor(), clusterBrokers),
                    String.format("Set replicationFactor ≤ %d to match available brokers", clusterBrokers),
                    "replication cannot exceed available broker count"));
        }

        if (clusterBrokers > 0 && spec.getPartitions() < clusterBrokers && avgThroughput > 50000) {
            rules.add(new Recommendation(
                    "MED",
                    String.format(
                            "partitions=%d is less than broker count (%d) — underutilizing cluster",
                            spec.getPartitions(), clusterBrokers),
                    String.format("Increase partitions to at least %d for full cluster utilization", clusterBrokers),
                    "partitions < brokers leaves some brokers idle for this topic"));
        }

        return rules;
    }
}
