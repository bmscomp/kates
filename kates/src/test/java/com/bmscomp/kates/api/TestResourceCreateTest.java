package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.Set;
import jakarta.persistence.PersistenceException;
import jakarta.ws.rs.core.Response;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.BulkCreateResponse;
import com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.engine.UnknownBackendException;
import com.bmscomp.kates.service.AuditService;
import com.bmscomp.kates.util.Result;

/**
 * What POST /api/tests and POST /api/tests/bulk answer for a run that does
 * not start, and so whose it is to fix. A run the Kates API could not save,
 * with its database unreachable, was answered 400 Bad Request with the
 * exception's message, as though the request had been wrong.
 */
class TestResourceCreateTest {

    /** What saving a run throws while the database is unreachable. */
    private static final PersistenceException UNREACHABLE =
            new PersistenceException("Unable to acquire JDBC Connection");

    private static final String NOT_STARTED =
            "The Kates API failed to start the test run — see server logs for details";

    private final InMemoryEngine engine = new InMemoryEngine();
    private final AuditService audit = mock(AuditService.class);
    private TestResource resource;

    @BeforeEach
    void setUp() {
        resource = new TestResource(engine.orchestrator, engine.repository, null);
        resource.auditService = audit;
    }

    @Test
    void aRunTheKatesApiCannotSaveIsAFaultOfTheKatesApi() {
        doThrow(UNREACHABLE).when(engine.repository).save(any());

        assertFaultOfTheKatesApi(resource.createTest(InMemoryEngine.request()));
        assertEquals(0, engine.orchestrator.activeTestCount(), "the run gave back its slot");
        verifyNoInteractions(audit);
    }

    @Test
    void aScenarioTheKatesApiCannotSaveIsAFaultOfTheKatesApi() {
        doThrow(UNREACHABLE).when(engine.repository).save(any());

        assertFaultOfTheKatesApi(resource.createTest(scenario()));
        assertEquals(0, engine.orchestrator.activeTestCount(), "the run gave back its slot");
        verifyNoInteractions(audit);
    }

    @Test
    void aBackendTheRequestNamesThatTheKatesApiDoesNotHaveIsTheCallersToChange() {
        CreateTestRequest request = InMemoryEngine.request();
        request.setBackend("no-such-backend");

        assertBadRequest("Backend not found: 'no-such-backend'. Available: [fake]", resource.createTest(request));
        assertEquals(0, engine.orchestrator.activeTestCount());
    }

    @Test
    void aBackendTheScenarioNamesThatTheKatesApiDoesNotHaveIsTheCallersToChange() {
        CreateTestRequest request = scenario();
        request.getScenario().setBackend("no-such-backend");

        assertBadRequest("Backend not found: 'no-such-backend'. Available: [fake]", resource.createTest(request));
        assertEquals(0, engine.orchestrator.activeTestCount());
    }

    @Test
    void aDefaultBackendTheKatesApiDoesNotHaveIsAFaultOfTheKatesApi() {
        // kates.engine.default-backend names a backend the Kates API does not
        // have, and the request names none, so it cannot be at fault.
        TestOrchestrator misconfigured = mock(TestOrchestrator.class);
        when(misconfigured.executeTest(any()))
                .thenReturn(Result.failure(new UnknownBackendException("nativ", List.of("native", "trogdor"))));
        resource = new TestResource(misconfigured, engine.repository, null);
        resource.auditService = audit;

        assertFaultOfTheKatesApi(resource.createTest(InMemoryEngine.request()));
        assertFaultOfTheKatesApi(resource.createTest(scenario()));
        verifyNoInteractions(audit);

        // A request that names it is the caller's to change, as with any
        // other backend the Kates API does not have.
        CreateTestRequest named = InMemoryEngine.request();
        named.setBackend("nativ");
        assertBadRequest("Backend not found: 'nativ'. Available: [native, trogdor]", resource.createTest(named));
    }

    @Test
    void aFullEngineIsStillToBeRetried() {
        engine.fill();

        Response response = resource.createTest(InMemoryEngine.request());

        assertEquals(429, response.getStatus());
        assertEquals("60", response.getHeaderString("Retry-After"));
        ApiError error = (ApiError) response.getEntity();
        assertEquals(429, error.getStatus());
        assertEquals("Too Many Requests", error.getError());
        assertTrue(error.getMessage().startsWith("Concurrency limit reached: 3 tests"), error.getMessage());
    }

    @Test
    void aFieldTheRunCannotHonourIsStillRefusedByName() {
        CreateTestRequest request = InMemoryEngine.request();
        request.getSpec().setEnableCrc(true);

        Response response = resource.createTest(request);

        assertEquals(400, response.getStatus());
        ApiError error = (ApiError) response.getEntity();
        assertEquals("Validation Failed", error.getError());
        assertEquals(Set.of("enableCrc"), error.getFieldErrors().keySet());
    }

    @Test
    void aBulkItemTheKatesApiCannotSaveSaysItIsAFaultOfTheKatesApi() {
        doThrow(UNREACHABLE).when(engine.repository).save(any());
        CreateTestRequest unknownBackend = InMemoryEngine.request();
        unknownBackend.setBackend("no-such-backend");

        Response response = resource.bulkCreate(List.of(InMemoryEngine.request(), unknownBackend));

        assertEquals(202, response.getStatus());
        assertEquals(
                List.of(
                        TestRunSummary.failure(NOT_STARTED),
                        TestRunSummary.failure("Backend not found: 'no-such-backend'. Available: [fake]")),
                ((BulkCreateResponse) response.getEntity()).runs());
        assertEquals(0, engine.orchestrator.activeTestCount());
        verifyNoInteractions(audit);
    }

    /** A 500 whose message keeps the cause, which can name hosts, to the server log. */
    private static void assertFaultOfTheKatesApi(Response response) {
        assertEquals(500, response.getStatus());
        ApiError error = (ApiError) response.getEntity();
        assertEquals(500, error.getStatus());
        assertEquals("Internal Server Error", error.getError());
        assertEquals(NOT_STARTED, error.getMessage());
        assertNull(error.getFieldErrors());
    }

    private static void assertBadRequest(String message, Response response) {
        assertEquals(400, response.getStatus());
        ApiError error = (ApiError) response.getEntity();
        assertEquals(400, error.getStatus());
        assertEquals("Bad Request", error.getError());
        assertEquals(message, error.getMessage());
    }

    /** A LOAD scenario of one STEADY phase, on the default backend. */
    private static CreateTestRequest scenario() {
        TestScenario scenario = new TestScenario();
        scenario.setName("steady");
        scenario.setType(TestType.LOAD);
        scenario.setBaseSpec(new TestSpec());
        scenario.setPhases(List.of(new ScenarioPhase("steady", ScenarioPhase.PhaseType.STEADY, 60_000, -1)));
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setScenario(scenario);
        return request;
    }
}
