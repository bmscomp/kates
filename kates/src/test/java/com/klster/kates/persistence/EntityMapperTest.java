package com.klster.kates.persistence;

import com.klster.kates.domain.SlaDefinition;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import org.junit.jupiter.api.Test;

import java.util.LinkedHashMap;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

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
        TestRun run = new TestRun();
        run.setTestType(TestType.LOAD);
        run.setStatus(TestResult.TaskStatus.PENDING);

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

        TestResult r2 = new TestResult();
        r2.setTaskId("task-2");
        r2.setRecordsSent(2000);
        TestResult r3 = new TestResult();
        r3.setTaskId("task-3");
        r3.setRecordsSent(3000);
        run.getResults().clear();
        run.addResult(r2);
        run.addResult(r3);

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

        TestResult result = new TestResult();
        result.setTaskId("task-1");
        result.setRecordsSent(1000);
        result.setThroughputRecordsPerSec(500.0);
        result.setAvgLatencyMs(5.0);
        result.setP50LatencyMs(3.0);
        result.setP95LatencyMs(10.0);
        result.setP99LatencyMs(20.0);
        result.setMaxLatencyMs(50.0);
        result.setStatus(TestResult.TaskStatus.DONE);

        TestRun run = new TestRun(TestType.LOAD, spec);
        run.setStatus(TestResult.TaskStatus.RUNNING);
        run.setBackend("native");
        run.setScenarioName("basic-load");
        run.setSla(sla);
        run.setLabels(new LinkedHashMap<>(Map.of("env", "test")));
        run.addResult(result);

        return run;
    }
}
