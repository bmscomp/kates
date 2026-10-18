package com.bmscomp.kates.engine;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;

import com.bmscomp.kates.domain.TestType;

@ApplicationScoped
public class TuningTestRunner {

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

    public static List<Map<String, Object>> availableTuningTests() {
        List<Map<String, Object>> result = new ArrayList<>();
        for (TestType type : TestType.values()) {
            if (isTuningType(type)) {
                Map<String, Object> entry = new LinkedHashMap<>();
                entry.put("type", type.name());
                entry.put("parameter", parameterNameFor(type));
                // A run measures the spec's one setting: it executes one produce task.
                entry.put("steps", 1);
                entry.put("description", descriptionFor(type));
                result.add(entry);
            }
        }
        return result;
    }

    private static String descriptionFor(TestType type) {
        return switch (type) {
            case TUNE_REPLICATION -> "Measures throughput and latency at the spec's replication factor";
            case TUNE_ACKS -> "Measures throughput and latency at the spec's acks setting";
            case TUNE_BATCHING -> "Measures throughput and latency at the spec's batch.size and linger.ms";
            case TUNE_COMPRESSION -> "Measures throughput and latency with the spec's compression codec";
            case TUNE_PARTITIONS -> "Measures throughput and latency at the spec's partition count";
            default -> "";
        };
    }
}
