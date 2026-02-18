package com.klster.kates.persistence;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.stream.Collectors;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.jboss.logging.Logger;

import com.klster.kates.domain.SlaDefinition;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestSpec;

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

        if (run.getResults() != null) {
            for (TestResult result : run.getResults()) {
                entity.addResult(toResultEntity(result));
            }
        }

        return entity;
    }

    public static TestRun toDomain(TestRunEntity entity) {
        TestRun run = new TestRun();
        run.setId(entity.getId());
        run.setTestType(entity.getTestType());
        run.setStatus(entity.getStatus());
        run.setCreatedAt(entity.getCreatedAt() != null ? entity.getCreatedAt().toString() : null);
        run.setBackend(entity.getBackend());
        run.setScenarioName(entity.getScenarioName());
        run.setSpec(fromJson(entity.getSpecJson(), TestSpec.class));
        run.setSla(fromJson(entity.getSlaJson(), SlaDefinition.class));
        run.setLabels(fromJson(entity.getLabelsJson(), new TypeReference<LinkedHashMap<String, String>>() {}));

        if (entity.getResults() != null) {
            run.setResults(entity.getResults().stream()
                    .map(EntityMapper::toResultDomain)
                    .collect(Collectors.toList()));
        }

        return run;
    }

    public static void updateEntity(TestRunEntity entity, TestRun run) {
        entity.setTestType(run.getTestType());
        entity.setStatus(run.getStatus());
        entity.setBackend(run.getBackend());
        entity.setScenarioName(run.getScenarioName());
        entity.setSpecJson(toJson(run.getSpec()));
        entity.setSlaJson(toJson(run.getSla()));
        entity.setLabelsJson(toJson(run.getLabels()));

        entity.getResults().clear();
        if (run.getResults() != null) {
            for (TestResult result : run.getResults()) {
                entity.addResult(toResultEntity(result));
            }
        }
    }

    private static TestResultEntity toResultEntity(TestResult result) {
        TestResultEntity entity = new TestResultEntity();
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
        return entity;
    }

    private static TestResult toResultDomain(TestResultEntity entity) {
        TestResult result = new TestResult();
        result.setTaskId(entity.getTaskId());
        result.setTestType(entity.getTestType());
        result.setStatus(entity.getStatus());
        result.setRecordsSent(entity.getRecordsSent());
        result.setThroughputRecordsPerSec(entity.getThroughputRecordsPerSec());
        result.setThroughputMBPerSec(entity.getThroughputMBPerSec());
        result.setAvgLatencyMs(entity.getAvgLatencyMs());
        result.setP50LatencyMs(entity.getP50LatencyMs());
        result.setP95LatencyMs(entity.getP95LatencyMs());
        result.setP99LatencyMs(entity.getP99LatencyMs());
        result.setMaxLatencyMs(entity.getMaxLatencyMs());
        result.setStartTime(entity.getStartTime());
        result.setEndTime(entity.getEndTime());
        result.setError(entity.getError());
        result.setPhaseName(entity.getPhaseName());
        return result;
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
