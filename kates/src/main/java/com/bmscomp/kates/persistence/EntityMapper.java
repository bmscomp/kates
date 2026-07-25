package com.bmscomp.kates.persistence;

import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;

/**
 * Converts between domain objects ({@link TestRun}, {@link TestResult})
 * and their JPA entity representations.
 */
public final class EntityMapper {

    private static final Logger LOG = Logger.getLogger(EntityMapper.class);
    private static final ObjectMapper JSON = new ObjectMapper();

    private EntityMapper() {}

    public static TestRunEntity toEntity(TestRun run) {
        TestRunEntity entity = new TestRunEntity();
        entity.setId(run.getId());
        entity.setTestType(run.getTestType());
        entity.setStatus(run.getStatus());
        entity.setCreatedAt(parseInstant(run.getCreatedAt()));
        entity.setBackend(run.getBackend());
        entity.setScenarioName(run.getScenarioName());
        entity.setSpecJson(toJson(run.getSpec()));
        entity.setSlaJson(toJson(run.getSla()));
        entity.setLabelsJson(toJson(run.getLabels()));
        entity.setCdcPhasesJson(toJson(run.getCdcPhases()));

        if (run.getResults() != null) {
            for (TestResult result : run.getResults()) {
                entity.addResult(toResultEntity(result));
            }
        }

        return entity;
    }

    public static TestRun toDomain(TestRunEntity entity) {
        TestRun run = new TestRun()
                .withId(entity.getId())
                .withTestType(entity.getTestType())
                .withStatus(entity.getStatus())
                .withCreatedAt(
                        entity.getCreatedAt() != null ? entity.getCreatedAt().toString() : null)
                .withBackend(entity.getBackend())
                .withScenarioName(entity.getScenarioName())
                .withSpec(fromJson(entity.getSpecJson(), TestSpec.class))
                .withSla(fromJson(entity.getSlaJson(), SlaDefinition.class))
                .withLabels(fromJson(entity.getLabelsJson(), new TypeReference<LinkedHashMap<String, String>>() {}))
                .withCdcPhases(
                        fromJson(entity.getCdcPhasesJson(), new TypeReference<LinkedHashMap<String, Long>>() {}));

        if (entity.getResults() != null) {
            run = run.withResults(entity.getResults().stream()
                    .map(EntityMapper::toResultDomain)
                    .collect(Collectors.toList()));
        }

        return run;
    }

    /**
     * Lightweight mapper for list endpoints — skips the lazy-loaded results collection
     * to avoid N+1 queries. Use {@link #toDomain} when results are needed (detail view).
     */
    public static TestRun toDomainSummary(TestRunEntity entity) {
        return new TestRun()
                .withId(entity.getId())
                .withTestType(entity.getTestType())
                .withStatus(entity.getStatus())
                .withCreatedAt(
                        entity.getCreatedAt() != null ? entity.getCreatedAt().toString() : null)
                .withBackend(entity.getBackend())
                .withScenarioName(entity.getScenarioName())
                .withSpec(fromJson(entity.getSpecJson(), TestSpec.class))
                .withSla(fromJson(entity.getSlaJson(), SlaDefinition.class))
                .withLabels(fromJson(entity.getLabelsJson(), new TypeReference<LinkedHashMap<String, String>>() {}))
                .withCdcPhases(
                        fromJson(entity.getCdcPhasesJson(), new TypeReference<LinkedHashMap<String, Long>>() {}));
    }

    /**
     * Applies a domain run onto a MANAGED entity, mutating in place so Hibernate
     * can dirty-check it.
     *
     * <p>Child results are diffed by task id rather than cleared and rebuilt.
     * With {@code cascade=ALL, orphanRemoval=true}, clearing the collection
     * issues a DELETE for every result row followed by an INSERT for every one
     * again — on every status poll of a running multi-task run. Diffing turns
     * that into a handful of UPDATEs (usually none, since Hibernate skips
     * unchanged rows) and keeps the child primary keys stable.
     */
    public static void updateEntity(TestRunEntity entity, TestRun run) {
        entity.setTestType(run.getTestType());
        entity.setStatus(run.getStatus());
        entity.setBackend(run.getBackend());
        entity.setScenarioName(run.getScenarioName());
        entity.setSpecJson(toJson(run.getSpec()));
        entity.setSlaJson(toJson(run.getSla()));
        entity.setLabelsJson(toJson(run.getLabels()));
        entity.setCdcPhasesJson(toJson(run.getCdcPhases()));

        mergeResults(entity, run.getResults());
    }

    private static void mergeResults(TestRunEntity entity, List<TestResult> incoming) {
        List<TestResultEntity> existing = entity.getResults();

        if (incoming == null || incoming.isEmpty()) {
            existing.clear();
            return;
        }

        Map<String, TestResultEntity> byTaskId = new HashMap<>();
        for (TestResultEntity child : existing) {
            if (child.getTaskId() != null) {
                byTaskId.put(child.getTaskId(), child);
            }
        }

        Set<String> seen = new HashSet<>();
        for (TestResult result : incoming) {
            String taskId = result.getTaskId();
            TestResultEntity target = taskId != null ? byTaskId.get(taskId) : null;
            if (target == null) {
                entity.addResult(toResultEntity(result));
            } else {
                applyResult(target, result);
            }
            if (taskId != null) {
                seen.add(taskId);
            }
        }

        // Drop children the run no longer carries (orphanRemoval deletes them).
        existing.removeIf(child -> child.getTaskId() == null || !seen.contains(child.getTaskId()));
    }

    private static TestResultEntity toResultEntity(TestResult result) {
        TestResultEntity entity = new TestResultEntity();
        applyResult(entity, result);
        return entity;
    }

    private static void applyResult(TestResultEntity entity, TestResult result) {
        entity.setTaskId(result.getTaskId());
        entity.setTestType(result.getTestType());
        entity.setStatus(result.getStatus());
        entity.setRecordsSent(result.getRecordsSent());
        entity.setThroughputRecordsPerSec(result.getThroughputRecordsPerSec());
        entity.setThroughputMBPerSec(result.getThroughputMBPerSec());
        entity.setAvgLatencyMs(result.getAvgLatencyMs());
        entity.setP50LatencyMs(result.getP50LatencyMs());
        entity.setP95LatencyMs(result.getP95LatencyMs());
        entity.setP99LatencyMs(result.getP99LatencyMs());
        entity.setMaxLatencyMs(result.getMaxLatencyMs());
        entity.setStartTime(result.getStartTime());
        entity.setEndTime(result.getEndTime());
        entity.setError(result.getError());
        entity.setPhaseName(result.getPhaseName());
    }

    private static TestResult toResultDomain(TestResultEntity entity) {
        return new TestResult()
                .withTaskId(entity.getTaskId())
                .withTestType(entity.getTestType())
                .withStatus(entity.getStatus())
                .withRecordsSent(entity.getRecordsSent())
                .withThroughputRecordsPerSec(entity.getThroughputRecordsPerSec())
                .withThroughputMBPerSec(entity.getThroughputMBPerSec())
                .withAvgLatencyMs(entity.getAvgLatencyMs())
                .withP50LatencyMs(entity.getP50LatencyMs())
                .withP95LatencyMs(entity.getP95LatencyMs())
                .withP99LatencyMs(entity.getP99LatencyMs())
                .withMaxLatencyMs(entity.getMaxLatencyMs())
                .withStartTime(entity.getStartTime())
                .withEndTime(entity.getEndTime())
                .withError(entity.getError())
                .withPhaseName(entity.getPhaseName());
    }

    private static String toJson(Object obj) {
        if (obj == null) return null;
        try {
            return JSON.writeValueAsString(obj);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize to JSON", e);
            return null;
        }
    }

    private static <T> T fromJson(String json, Class<T> type) {
        if (json == null || json.isBlank()) return null;
        try {
            return JSON.readValue(json, type);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to deserialize JSON", e);
            return null;
        }
    }

    private static <T> T fromJson(String json, TypeReference<T> typeRef) {
        if (json == null || json.isBlank()) return null;
        try {
            return JSON.readValue(json, typeRef);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to deserialize JSON", e);
            return null;
        }
    }

    private static Instant parseInstant(String s) {
        if (s == null || s.isBlank()) return Instant.now();
        try {
            return Instant.parse(s);
        } catch (Exception e) {
            return Instant.now();
        }
    }
}
