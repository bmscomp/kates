package com.bmscomp.kates.engine;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.report.ReportGenerator;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.report.TuningReport;
import com.bmscomp.kates.service.TestRunRepository;

@ApplicationScoped
public class TuningTestRunner {

    @Inject
    TestRunRepository repository;

    @Inject
    ReportGenerator reportGenerator;

    public static boolean isTuningType(TestType type) {
        return type != null && type.name().startsWith("TUNE_");
    }

    public static String parameterNameFor(TestType type) {
        return switch (type) {
            case TUNE_REPLICATION -> "replication.factor";
            case TUNE_ACKS -> "acks";
            case TUNE_BATCHING -> "batch.size × linger.ms";
            case TUNE_COMPRESSION -> "compression.type";
            case TUNE_PARTITIONS -> "partitions";
            default -> "unknown";
        };
    }

    public static List<Map<String, Object>> stepsFor(TestType type) {
        return switch (type) {
            case TUNE_REPLICATION ->
                List.of(
                        Map.of("replicationFactor", 1, "minInsyncReplicas", 1, "acks", "1", "label", "RF=1, acks=1"),
                        Map.of(
                                "replicationFactor",
                                2,
                                "minInsyncReplicas",
                                1,
                                "acks",
                                "all",
                                "label",
                                "RF=2, acks=all"),
                        Map.of(
                                "replicationFactor",
                                3,
                                "minInsyncReplicas",
                                2,
                                "acks",
                                "all",
                                "label",
                                "RF=3, min.isr=2, acks=all"));

            case TUNE_ACKS ->
                List.of(
                        Map.of("acks", "0", "enableIdempotence", false, "label", "acks=0"),
                        Map.of("acks", "1", "enableIdempotence", false, "label", "acks=1"),
                        Map.of("acks", "all", "enableIdempotence", false, "label", "acks=all"),
                        Map.of("acks", "all", "enableIdempotence", true, "label", "acks=all + idempotent"));

            case TUNE_BATCHING ->
                List.of(
                        Map.of("batchSize", 16384, "lingerMs", 0, "label", "16KB / 0ms"),
                        Map.of("batchSize", 32768, "lingerMs", 5, "label", "32KB / 5ms"),
                        Map.of("batchSize", 65536, "lingerMs", 5, "label", "64KB / 5ms"),
                        Map.of("batchSize", 131072, "lingerMs", 10, "label", "128KB / 10ms"),
                        Map.of("batchSize", 262144, "lingerMs", 20, "label", "256KB / 20ms"),
                        Map.of("batchSize", 524288, "lingerMs", 50, "label", "512KB / 50ms"));

            case TUNE_COMPRESSION ->
                List.of(
                        Map.of("compressionType", "none", "label", "none"),
                        Map.of("compressionType", "snappy", "label", "snappy"),
                        Map.of("compressionType", "lz4", "label", "lz4"),
                        Map.of("compressionType", "zstd", "label", "zstd"),
                        Map.of("compressionType", "gzip", "label", "gzip"));

            case TUNE_PARTITIONS ->
                List.of(
                        Map.of("partitions", 1, "numProducers", 1, "label", "1 partition / 1 producer"),
                        Map.of("partitions", 3, "numProducers", 3, "label", "3 partitions / 3 producers"),
                        Map.of("partitions", 6, "numProducers", 6, "label", "6 partitions / 6 producers"),
                        Map.of("partitions", 12, "numProducers", 12, "label", "12 partitions / 12 producers"),
                        Map.of("partitions", 24, "numProducers", 24, "label", "24 partitions / 24 producers"));

            default -> List.of();
        };
    }

    public TuningReport buildReport(String runId) {
        TestRun run = repository.findById(runId).orElse(null);
        if (run == null || !isTuningType(run.getTestType())) {
            return null;
        }

        TestType type = run.getTestType();
        List<Map<String, Object>> stepConfigs = stepsFor(type);
        var report = reportGenerator.generate(run);
        ReportSummary summary = report != null ? report.getSummary() : null;

        TuningReport tuning = new TuningReport();
        tuning.setTestType(type.name());
        tuning.setParameterName(parameterNameFor(type));

        List<TuningReport.TuningStep> steps = new ArrayList<>();
        double bestThroughput = -1;
        int bestIdx = 0;

        for (int i = 0; i < stepConfigs.size(); i++) {
            Map<String, Object> cfg = stepConfigs.get(i);
            TuningReport.TuningStep step = new TuningReport.TuningStep();
            step.setStepIndex(i);
            step.setLabel((String) cfg.get("label"));
            step.setConfig(cfg);
            step.setMetrics(summary);

            if (summary != null && summary.avgThroughputRecPerSec() > bestThroughput) {
                bestThroughput = summary.avgThroughputRecPerSec();
                bestIdx = i;
            }
            steps.add(step);
        }

        tuning.setSteps(steps);
        tuning.setBestStepIndex(bestIdx);
        tuning.setRecommendation(buildRecommendation(type, stepConfigs, bestIdx));
        return tuning;
    }

    public static List<Map<String, Object>> availableTuningTests() {
        List<Map<String, Object>> result = new ArrayList<>();
        for (TestType type : TestType.values()) {
            if (isTuningType(type)) {
                Map<String, Object> entry = new LinkedHashMap<>();
                entry.put("type", type.name());
                entry.put("parameter", parameterNameFor(type));
                entry.put("steps", stepsFor(type).size());
                entry.put("description", descriptionFor(type));
                result.add(entry);
            }
        }
        return result;
    }

    private static String descriptionFor(TestType type) {
        return switch (type) {
            case TUNE_REPLICATION -> "Measures throughput cost of increasing replication factor";
            case TUNE_ACKS -> "Compares latency impact of acks=0, 1, all, and idempotent modes";
            case TUNE_BATCHING -> "Sweeps batch.size and linger.ms to find optimal throughput";
            case TUNE_COMPRESSION -> "Benchmarks none, snappy, lz4, zstd, gzip codecs";
            case TUNE_PARTITIONS -> "Tests throughput linearity as partition count scales";
            default -> "";
        };
    }

    private String buildRecommendation(TestType type, List<Map<String, Object>> configs, int bestIdx) {
        String bestLabel = (String) configs.get(bestIdx).get("label");
        return switch (type) {
            case TUNE_COMPRESSION -> bestLabel + " provides the best throughput-to-CPU ratio";
            case TUNE_BATCHING -> bestLabel + " delivers optimal throughput without latency penalty";
            case TUNE_REPLICATION -> bestLabel + " balances durability with acceptable throughput";
            case TUNE_ACKS -> bestLabel + " gives the best latency for the required safety level";
            case TUNE_PARTITIONS -> bestLabel + " achieves peak throughput scaling for this cluster";
            default -> bestLabel + " is the recommended configuration";
        };
    }
}
