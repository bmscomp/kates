package com.klster.kates.domain;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class TestRunTest {

    @Test
    void defaultConstructorGeneratesUniqueId() {
        TestRun run1 = new TestRun();
        TestRun run2 = new TestRun();
        assertNotNull(run1.getId());
        assertNotNull(run2.getId());
        assertNotEquals(run1.getId(), run2.getId());
    }

    @Test
    void defaultConstructorSetsPendingStatus() {
        TestRun run = new TestRun();
        assertEquals(TestResult.TaskStatus.PENDING, run.getStatus());
    }

    @Test
    void defaultConstructorInitializesEmptyResults() {
        TestRun run = new TestRun();
        assertNotNull(run.getResults());
        assertTrue(run.getResults().isEmpty());
    }

    @Test
    void defaultConstructorSetsCreatedAt() {
        TestRun run = new TestRun();
        assertNotNull(run.getCreatedAt());
        assertFalse(run.getCreatedAt().isEmpty());
    }

    @Test
    void parameterizedConstructorSetsTypeAndSpec() {
        TestSpec spec = new TestSpec();
        spec.setTopic("my-topic");
        TestRun run = new TestRun(TestType.LOAD, spec);

        assertEquals(TestType.LOAD, run.getTestType());
        assertEquals("my-topic", run.getSpec().getTopic());
        assertEquals(TestResult.TaskStatus.PENDING, run.getStatus());
    }

    @Test
    void addResultAppendsToList() {
        TestRun run = new TestRun();
        TestResult r1 = new TestResult().withTaskId("task-1");
        TestResult r2 = new TestResult().withTaskId("task-2");

        run = run.withAddedResult(r1);
        run = run.withAddedResult(r2);

        assertEquals(2, run.getResults().size());
        assertEquals("task-1", run.getResults().get(0).getTaskId());
        assertEquals("task-2", run.getResults().get(1).getTaskId());
    }

    @Test
    void idHasExpectedLength() {
        TestRun run = new TestRun();
        assertEquals(8, run.getId().length());
    }
}
