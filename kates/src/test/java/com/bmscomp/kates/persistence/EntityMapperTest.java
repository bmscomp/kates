package com.bmscomp.kates.persistence;

import static org.junit.jupiter.api.Assertions.*;

import java.util.LinkedHashMap;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

class EntityMapperTest {

    @Test
    void roundTripPreservesAllFields() {
        TestRun original = buildFullRun();
        TestRunEntity entity = EntityMapper.toEntity(original);
        TestRun restored = EntityMapper.toDomain(entity);

        assertEquals(original.getId(), restored.getId());
        assertEquals(original.getTestType(), restored.getTestType());
        assertEquals(original.getStatus(), restored.getStatus());
        assertEquals(original.getBackend(), restored.getBackend());
        assertEquals(original.getScenarioName(), restored.getScenarioName());
        assertEquals(original.getResults().size(), restored.getResults().size());
    }

    @Test
    void toEntitySetsAllFields() {
        TestRun run = buildFullRun();
        TestRunEntity entity = EntityMapper.toEntity(run);

        assertEquals(run.getId(), entity.getId());
        assertEquals(TestType.LOAD, entity.getTestType());
        assertEquals(TestResult.TaskStatus.RUNNING, entity.getStatus());
        assertEquals("native", entity.getBackend());
        assertNotNull(entity.getSpecJson());
        assertNotNull(entity.getCreatedAt());
        assertEquals(1, entity.getResults().size());
    }

    @Test
    void toDomainSetsAllFields() {
        TestRun run = buildFullRun();
        TestRunEntity entity = EntityMapper.toEntity(run);
        TestRun restored = EntityMapper.toDomain(entity);

        assertEquals("my-topic", restored.getSpec().getTopic());
        assertNotNull(restored.getSla());
        assertEquals(50.0, restored.getSla().getMaxP99LatencyMs());
        assertEquals(1, restored.getResults().size());

        TestResult result = restored.getResults().get(0);
        assertEquals("task-1", result.getTaskId());
        assertEquals(1000, result.getRecordsSent());
        assertEquals(5.0, result.getAvgLatencyMs(), 0.01);
    }

    @Test
    void nullSpecHandled() {
        TestRun run = new TestRun(TestType.LOAD, null)
            .withStatus(TestResult.TaskStatus.PENDING);

        TestRunEntity entity = EntityMapper.toEntity(run);
        assertNull(entity.getSpecJson());

        TestRun restored = EntityMapper.toDomain(entity);
        assertNull(restored.getSpec());
    }

    @Test
    void updateEntityReplacesResults() {
        TestRun run = buildFullRun();
        TestRunEntity entity = EntityMapper.toEntity(run);
        assertEquals(1, entity.getResults().size());

        TestResult r2 = new TestResult()
            .withTaskId("task-2")
            .withRecordsSent(2000);
        TestResult r3 = new TestResult()
            .withTaskId("task-3")
            .withRecordsSent(3000);
        run = run.withResults(java.util.List.of(r2, r3));

        EntityMapper.updateEntity(entity, run);

        assertEquals(2, entity.getResults().size());
        assertEquals("task-2", entity.getResults().get(0).getTaskId());
        assertEquals("task-3", entity.getResults().get(1).getTaskId());
    }

    private TestRun buildFullRun() {
        TestSpec spec = new TestSpec();
        spec.setTopic("my-topic");
        spec.setNumProducers(2);
        spec.setNumConsumers(1);

        SlaDefinition sla = new SlaDefinition();
        sla.setMaxP99LatencyMs(50.0);

        TestResult result = new TestResult()
            .withTaskId("task-1")
            .withRecordsSent(1000)
            .withThroughputRecordsPerSec(500.0)
            .withAvgLatencyMs(5.0)
            .withP50LatencyMs(3.0)
            .withP95LatencyMs(10.0)
            .withP99LatencyMs(20.0)
            .withMaxLatencyMs(50.0)
            .withStatus(TestResult.TaskStatus.DONE);

        TestRun run = new TestRun(TestType.LOAD, spec)
            .withStatus(TestResult.TaskStatus.RUNNING)
            .withBackend("native")
            .withScenarioName("basic-load")
            .withSla(sla)
            .withLabels(new LinkedHashMap<>(Map.of("env", "test")))
            .withAddedResult(result);

        return run;
    }
}
