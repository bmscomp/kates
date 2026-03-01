package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.trogdor.TrogdorClient;

@QuarkusTest
class TestExecutionServiceTest {

    @Inject
    TestExecutionService executionService;

    @Inject
    TestRunRepository repository;

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @InjectMock
    KafkaAdminService kafkaAdmin;

    @Inject
    EntityManager em;

    private final ObjectMapper mapper = new ObjectMapper();

    @BeforeEach
    void setUp() {
        doNothing().when(kafkaAdmin).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    @Test
    void executeTestCreatesTopicBeforeSubmittingTasks() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());
        CreateTestRequest request = createRequest(TestType.LOAD);

        executionService.executeTest(request);

        verify(kafkaAdmin, atLeastOnce()).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    @Test
    void executeTestSubmitsCorrectNumberOfTrogdorTasks() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        TestSpec spec = new TestSpec();
        spec.setNumProducers(2);
        spec.setNumConsumers(1);
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec);

        TestRun run = executionService.executeTest(request);

        verify(trogdorClient, times(3)).createTask(any());
        assertEquals(3, run.getResults().size());
    }

    @Test
    void executeTestSetsRunningOnSuccess() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        TestRun run = executionService.executeTest(createRequest(TestType.ROUND_TRIP));

        assertEquals(TestResult.TaskStatus.RUNNING, run.getStatus());
    }

    @Test
    void executeTestSetsFailedWhenAllTasksFail() {
        when(trogdorClient.createTask(any())).thenThrow(new RuntimeException("Connection refused"));

        TestRun run = executionService.executeTest(createRequest(TestType.ROUND_TRIP));

        assertEquals(TestResult.TaskStatus.FAILED, run.getStatus());
        assertTrue(run.getResults().stream().allMatch(r -> r.getStatus() == TestResult.TaskStatus.FAILED));
    }

    @Test
    void executeTestStoresRunInRepository() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        TestRun run = executionService.executeTest(createRequest(TestType.LOAD));

        assertTrue(repository.findById(run.getId()).isPresent());
    }

    @Test
    void executeTestWithNullSpecUsesDefaults() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);

        TestRun run = executionService.executeTest(request);
        assertNotNull(run.getSpec());
    }

    @Test
    void executeTestResultContainsTaskId() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        TestRun run = executionService.executeTest(createRequest(TestType.ROUND_TRIP));

        assertFalse(run.getResults().isEmpty());
        assertNotNull(run.getResults().get(0).getTaskId());
        assertTrue(run.getResults().get(0).getTaskId().contains("round_trip"));
    }

    @Test
    void refreshStatusUpdatesDoneFromTrogdor() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());
        TestRun run = executionService.executeTest(createRequest(TestType.ROUND_TRIP));

        ObjectNode doneResponse = mapper.createObjectNode();
        doneResponse.put("state", "DONE");
        ObjectNode status = doneResponse.putObject("status");
        status.put("totalSent", 100_000);
        status.put("elapsedMs", 60_000);
        status.put("averageLatencyMs", 5.2);
        status.put("p50LatencyMs", 3.1);
        status.put("p95LatencyMs", 12.5);
        status.put("p99LatencyMs", 45.0);
        status.put("maxLatencyMs", 120.0);

        when(trogdorClient.getTask(anyString())).thenReturn(doneResponse);

        TestRun refreshed = executionService.refreshStatus(run.getId());

        assertEquals(TestResult.TaskStatus.DONE, refreshed.getStatus());
        TestResult result = refreshed.getResults().get(0);
        assertEquals(100_000, result.getRecordsSent());
        assertEquals(5.2, result.getAvgLatencyMs(), 0.01);
        assertEquals(3.1, result.getP50LatencyMs(), 0.01);
        assertEquals(12.5, result.getP95LatencyMs(), 0.01);
        assertEquals(45.0, result.getP99LatencyMs(), 0.01);
        assertEquals(120.0, result.getMaxLatencyMs(), 0.01);
    }

    @Test
    void refreshStatusForUnknownRunThrows() {
        assertThrows(IllegalArgumentException.class, () -> executionService.refreshStatus("nonexistent"));
    }

    @Test
    void stopTestCallsStopOnRunningTasks() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());
        when(trogdorClient.stopTask(anyString())).thenReturn(emptyResponse());

        TestRun run = executionService.executeTest(createRequest(TestType.ROUND_TRIP));
        executionService.stopTest(run.getId());

        verify(trogdorClient, atLeastOnce()).stopTask(anyString());
        em.clear();
        TestRun stopped = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.STOPPING, stopped.getStatus());
    }

    @Test
    void stopTestForUnknownRunThrows() {
        assertThrows(IllegalArgumentException.class, () -> executionService.stopTest("nonexistent"));
    }

    @Test
    void volumeTestCreatesMultipleTopics() {
        when(trogdorClient.createTask(any())).thenReturn(emptyResponse());

        executionService.executeTest(createRequest(TestType.VOLUME));

        verify(kafkaAdmin, atLeast(3)).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    private CreateTestRequest createRequest(TestType type) {
        CreateTestRequest request = new CreateTestRequest();
        request.setType(type);
        request.setSpec(new TestSpec());
        return request;
    }

    private JsonNode emptyResponse() {
        return mapper.createObjectNode();
    }
}
