package com.bmscomp.kates.api;

import java.util.List;
import jakarta.inject.Inject;
import jakarta.validation.Valid;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.PUT;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import io.smallrye.common.annotation.Blocking;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.ChaosProvider;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.persistence.BaselineEntity;
import com.bmscomp.kates.service.AuditService;
import com.bmscomp.kates.service.BaselineService;
import com.bmscomp.kates.service.TestRunRepository;

@Path("/api/tests")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Blocking
@Tag(name = "Tests")
public class TestResource {

    /**
     * Retry-After for a 429. Runs last minutes, so a short retry would just
     * bounce; a minute is the smallest interval likely to find a free slot.
     */
    private static final String RETRY_AFTER_SECONDS = "60";

    /** What a client is told of a run that did not start for a fault in the Kates API. */
    private static final String NOT_STARTED =
            "The Kates API failed to start the test run — see server logs for details";

    private static final Logger LOG = Logger.getLogger(TestResource.class);

    private final TestOrchestrator orchestrator;
    private final TestRunRepository repository;
    private final ChaosProvider chaosProvider;

    @Inject
    BaselineService baselineService;

    @Inject
    AuditService auditService;

    @Inject
    public TestResource(
            TestOrchestrator orchestrator,
            TestRunRepository repository,
            @jakarta.inject.Named("kubernetes") ChaosProvider chaosProvider) {
        this.orchestrator = orchestrator;
        this.repository = repository;
        this.chaosProvider = chaosProvider;
    }

    @POST
    @Operation(
            summary = "Create and execute a test",
            description = "Submits a new performance test run for asynchronous execution")
    @APIResponse(responseCode = "202", description = "Test accepted for execution")
    @APIResponse(
            responseCode = "400",
            description = "Invalid request, including a value outside its limits in the spec or a scenario's specs,"
                    + " or a spec field the test type or backend cannot apply; fieldErrors names each field")
    @APIResponse(responseCode = "429", description = "Concurrency limit reached — retry later")
    @APIResponse(
            responseCode = "500",
            description = "A fault in the Kates API, such as its database being unreachable; the run did not start")
    public Response createTest(@Valid CreateTestRequest request) {
        var result = orchestrator.executeTest(request);
        if (result.isFailure()) {
            Exception failure = result.asFailure().orElseThrow();
            ApiError error = notStarted(request, failure);
            Response.ResponseBuilder answer = Response.status(error.getStatus()).entity(error);
            if (failure instanceof com.bmscomp.kates.engine.ConcurrencyLimitException) {
                answer.header("Retry-After", RETRY_AFTER_SECONDS);
            }
            return answer.build();
        }
        TestRun run = result.asSuccess().orElseThrow();
        recordCreated(run, request.getType() + " test");
        return Response.accepted(run).build();
    }

    /**
     * Writes a started run's audit row. The run is producing by now, so a
     * write that fails is logged, not answered. Answering it reported the run
     * as one that never started: a 500 from POST /api/tests, which invited
     * the same request again, and a second run; a failed item with no id from
     * POST /api/tests/bulk, which left the run nothing to find or stop it by.
     */
    private void recordCreated(TestRun run, String details) {
        try {
            auditService.record("CREATE", "test", run.getId(), details);
        } catch (RuntimeException e) {
            LOG.warnf("Run %s started, but its audit row was not written: %s", run.getId(), e);
        }
    }

    /**
     * The answer to a request whose run the orchestrator did not start, with
     * the status POST /api/tests gives it. POST /api/tests/bulk gives such an
     * item the same message.
     *
     * <p>A request the run could not honour as written, or one that names a
     * backend the Kates API does not have, is the caller's to change: 400. A
     * full engine is 429. Anything else is a fault in the Kates API, such as
     * its database being unreachable when the run is saved, or
     * kates.engine.default-backend naming no backend it has: 500, since the
     * same request can start a run once the fault is fixed. Such a fault was
     * answered 400, which told the caller to change a request that had
     * nothing wrong with it.
     */
    private static ApiError notStarted(CreateTestRequest request, Exception failure) {
        // A full engine is a temporary condition, not a malformed request.
        // Returning 400 for it made the two indistinguishable to clients and
        // to CI, which then retried nothing and failed the build instead.
        if (failure instanceof com.bmscomp.kates.engine.ConcurrencyLimitException) {
            return ApiError.of(429, "Too Many Requests", failure.getMessage());
        }
        if (failure instanceof com.bmscomp.kates.engine.InvalidTestSpecException invalid) {
            return ApiError.validationFailed(invalid.getMessage(), invalid.getFieldErrors());
        }
        if (failure instanceof com.bmscomp.kates.engine.UnknownBackendException unknown
                && namesBackend(request, unknown.getBackend())) {
            return ApiError.of(400, "Bad Request", failure.getMessage());
        }
        // The cause goes to the server log only, as GlobalExceptionMapper
        // keeps it: its message can name hosts and SQL statements. One line:
        // the orchestrator logs a run it could not save with the stack.
        LOG.errorf("A test run did not start for a fault in the Kates API: %s", failure);
        return ApiError.of(500, "Internal Server Error", NOT_STARTED);
    }

    /** Whether the request, or its scenario, names the backend {@code name}. */
    private static boolean namesBackend(CreateTestRequest request, String name) {
        var scenario = request.getScenario();
        return name.equals(request.getBackend()) || (scenario != null && name.equals(scenario.getBackend()));
    }

    @POST
    @Path("/bulk")
    @Operation(
            summary = "Create multiple tests",
            description = "Submits up to 10 test runs in a single request. Requests beyond"
                    + " kates.engine.max-concurrent-tests (default 3) are reported as"
                    + " per-item failures rather than being queued. runs answers each request in"
                    + " order, with its run's id and status, or why it did not start; created"
                    + " counts the runs that started.")
    @APIResponse(responseCode = "202", description = "Tests accepted for execution")
    public Response bulkCreate(List<@Valid CreateTestRequest> requests) {
        if (requests == null || requests.isEmpty()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "At least one test request required"))
                    .build();
        }
        if (requests.size() > 10) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Maximum 10 tests per bulk request"))
                    .build();
        }
        List<com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary> results = new java.util.ArrayList<>();
        for (CreateTestRequest req : requests) {
            try {
                var testResult = orchestrator.executeTest(req);
                if (testResult.isFailure()) {
                    String error = notStarted(req, testResult.asFailure().orElseThrow())
                            .getMessage();
                    results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.failure(error));
                } else {
                    TestRun run = testResult.asSuccess().orElseThrow();
                    results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.success(
                            run.getId(), run.getStatus().name()));
                    recordCreated(run, req.getType() + " bulk test");
                }
            } catch (Exception e) {
                results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.failure(e.getMessage()));
            }
        }
        // The runs that started. Counting every item answered "created": 3
        // for three items that had all failed.
        long created = results.stream().filter(summary -> summary.id() != null).count();
        return Response.accepted(new com.bmscomp.kates.domain.BulkCreateResponse(created, results))
                .build();
    }

    @DELETE
    @Path("/bulk")
    @Operation(
            summary = "Delete multiple tests",
            description = "Deletes test runs by a list of IDs, stopping each one that is still running first,"
                    + " as DELETE /api/tests/{id} does")
    public Response bulkDelete(com.bmscomp.kates.domain.BulkDeleteRequest request) {
        List<String> ids = request != null ? request.ids() : null;
        if (ids == null || ids.isEmpty()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'ids' is required"))
                    .build();
        }
        int deleted = 0, notFound = 0;
        for (String id : ids) {
            // The same delete as the single one. Removing only the row left a
            // running run's workers producing and its concurrency slot taken.
            if (orchestrator.deleteTest(id)) {
                auditService.record("DELETE", "test", id, "bulk delete");
                deleted++;
            } else {
                notFound++;
            }
        }
        return Response.ok(new com.bmscomp.kates.domain.BulkDeleteResponse(deleted, notFound))
                .build();
    }

    @GET
    @Operation(
            summary = "List test runs",
            description = "Returns paginated test runs, optionally filtered by type or status")
    @APIResponse(responseCode = "200", description = "Paginated list of test runs")
    public Response listTests(
            @Parameter(description = "Filter by test type") @QueryParam("type") String type,
            @Parameter(description = "Filter by status") @QueryParam("status") String status,
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {

        int safePage = Math.max(0, page);
        int safeSize = Math.max(1, Math.min(size, 200));

        if (type != null && !type.isEmpty()) {
            try {
                TestType testType = TestType.valueOf(type.toUpperCase());
                List<TestRun> content = repository.findByTypePaged(testType, safePage, safeSize);
                long total = repository.countByType(testType);
                return Response.ok(new PagedResponse<>(content, safePage, safeSize, total))
                        .build();
            } catch (IllegalArgumentException e) {
                return Response.status(Response.Status.BAD_REQUEST)
                        .entity(new ApiError(400, "Bad Request", "Invalid test type: " + type))
                        .build();
            }
        }

        if (status != null && !status.isEmpty()) {
            try {
                TestResult.TaskStatus taskStatus = TestResult.TaskStatus.valueOf(status.toUpperCase());
                List<TestRun> content = repository.findByStatusPaged(taskStatus, safePage, safeSize);
                long total = repository.countByStatus(taskStatus);
                return Response.ok(new PagedResponse<>(content, safePage, safeSize, total))
                        .build();
            } catch (IllegalArgumentException e) {
                return Response.status(Response.Status.BAD_REQUEST)
                        .entity(new ApiError(400, "Bad Request", "Invalid status: " + status))
                        .build();
            }
        }

        List<TestRun> content = repository.findAllPaged(safePage, safeSize);
        long total = repository.countAll();
        return Response.ok(new PagedResponse<>(content, safePage, safeSize, total))
                .build();
    }

    @GET
    @Path("/{id}")
    @Operation(
            summary = "Get a test run",
            description = "Returns a single test run by ID, refreshing its status. spec is what the run used, the"
                    + " request merged with its test type's defaults; a field no type has a default for"
                    + " (consumerGroup, targetThroughput, the fetch settings, the enable options) appears only"
                    + " when the request set it. requestedSpec is the request's own spec fields, absent on runs"
                    + " stored before it was kept")
    @APIResponse(responseCode = "200", description = "Test run details")
    @APIResponse(responseCode = "404", description = "Test run not found")
    public Response getTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        return repository
                .findById(id)
                .map(run -> {
                    TestRun refreshed = orchestrator.refreshStatus(id);
                    return Response.ok(refreshed).build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(new ApiError(404, "Not Found", "Test run not found: " + id))
                        .build());
    }

    @DELETE
    @Path("/{id}")
    @Operation(
            summary = "Delete a test run",
            description = "Stops the test if running and removes it with its results. A running test's tasks"
                    + " stop, it gives back its place among the kates.engine.max-concurrent-tests running"
                    + " tests, and its end is announced as FAILED, with the detail \"deleted\" on the event"
                    + " stream.")
    @APIResponse(responseCode = "204", description = "Test run deleted")
    @APIResponse(responseCode = "404", description = "Test run not found")
    public Response deleteTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        // A running run used to be stopped through the backend but kept its
        // concurrency permit, so deleting max-concurrent-tests running runs
        // made every new run answer 429 until a restart.
        if (!orchestrator.deleteTest(id)) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(new ApiError(404, "Not Found", "Test run not found: " + id))
                    .build();
        }
        auditService.record("DELETE", "test", id, "Test deleted");
        return Response.noContent().build();
    }

    @POST
    @Path("/{id}/cancel")
    // Overrides the class-level @Consumes(APPLICATION_JSON). Cancel takes no
    // body, so a client sends none and therefore no Content-Type — which JAX-RS
    // treats as application/octet-stream, matches against application/json, and
    // rejects with 415. `curl -X POST .../cancel` could never cancel anything;
    // only a client that invented a Content-Type header for an empty body got
    // through.
    @Consumes(MediaType.WILDCARD)
    @Operation(
            summary = "Cancel a running test",
            description = "Stops the run's tasks and stores the run as FAILED, each unfinished task with the error"
                    + " \"Cancelled by user\". There is no CANCELLED status: the answer says FAILED, as every later"
                    + " read does, with reason \"cancelled\".")
    @APIResponse(responseCode = "200", description = "Test cancelled and stored as FAILED")
    @APIResponse(responseCode = "404", description = "Test run not found")
    @APIResponse(responseCode = "409", description = "Test is not running")
    public Response cancelTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        java.util.Optional<TestRun> cancelled;
        try {
            cancelled = orchestrator.cancelTest(id);
        } catch (com.bmscomp.kates.engine.RunNotCancellableException e) {
            return Response.status(Response.Status.CONFLICT)
                    .entity(new ApiError(409, "Conflict", e.getMessage()))
                    .build();
        }
        return cancelled
                .map(run -> {
                    auditService.record("CANCEL", "test", id, "Test cancelled by user");
                    // status is what the run is stored as, FAILED; reason
                    // says why, which the stored status cannot.
                    return Response.ok(java.util.Map.of(
                                    "id",
                                    run.getId(),
                                    "status",
                                    run.getStatus().name(),
                                    "reason",
                                    "cancelled",
                                    "message",
                                    "Test cancelled; it is stored as FAILED"))
                            .build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(new ApiError(404, "Not Found", "Test run not found: " + id))
                        .build());
    }

    @GET
    @Path("/types")
    @Operation(summary = "List available test types")
    public TestType[] getTestTypes() {
        return TestType.values();
    }

    @GET
    @Path("/backends")
    @Operation(summary = "List available test backends")
    public List<String> getBackends() {
        return orchestrator.availableBackends();
    }

    @GET
    @Path("/baselines")
    @Operation(summary = "List all baselines", description = "Returns the baseline run for each test type")
    @Tag(name = "Baselines")
    public Response listBaselines() {
        List<com.bmscomp.kates.domain.BaselineResponse> result = baselineService.listAll().stream()
                .map(this::baselineToResponse)
                .collect(java.util.stream.Collectors.toList());
        return Response.ok(result).build();
    }

    @GET
    @Path("/baselines/{type}")
    @Operation(summary = "Get baseline for a test type")
    @Tag(name = "Baselines")
    public Response getBaseline(@Parameter(description = "Test type") @PathParam("type") String typeStr) {
        TestType type = parseBaselineType(typeStr);
        if (type == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Invalid test type: " + typeStr))
                    .build();
        }
        return baselineService
                .get(type)
                .map(b -> Response.ok(baselineToResponse(b)).build())
                .orElse(Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "No baseline set for type: " + typeStr))
                        .build());
    }

    @PUT
    @Path("/baselines/{type}")
    @Operation(
            summary = "Set baseline for a test type",
            description = "Marks a test run as the baseline for the given type")
    @Tag(name = "Baselines")
    public Response setBaseline(
            @Parameter(description = "Test type") @PathParam("type") String typeStr,
            com.bmscomp.kates.domain.SetBaselineRequest request) {
        TestType type = parseBaselineType(typeStr);
        if (type == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Invalid test type: " + typeStr))
                    .build();
        }
        String runId = request != null ? request.runId() : null;
        if (runId == null || runId.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "runId is required in request body"))
                    .build();
        }
        if (repository.findById(runId).isEmpty()) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Test run not found: " + runId))
                    .build();
        }
        BaselineEntity baseline = baselineService.set(type, runId);
        return Response.ok(baselineToResponse(baseline)).build();
    }

    @DELETE
    @Path("/baselines/{type}")
    @Operation(summary = "Remove baseline for a test type")
    @Tag(name = "Baselines")
    public Response unsetBaseline(@Parameter(description = "Test type") @PathParam("type") String typeStr) {
        TestType type = parseBaselineType(typeStr);
        if (type == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Invalid test type: " + typeStr))
                    .build();
        }
        boolean removed = baselineService.unset(type);
        if (removed) {
            return Response.noContent().build();
        }
        return Response.status(404)
                .entity(ApiError.of(404, "Not Found", "No baseline set for type: " + typeStr))
                .build();
    }

    private com.bmscomp.kates.domain.BaselineResponse baselineToResponse(BaselineEntity b) {
        return new com.bmscomp.kates.domain.BaselineResponse(b.getTestType().name(), b.getRunId(), b.getSetAt());
    }

    private TestType parseBaselineType(String typeStr) {
        if (typeStr == null || typeStr.isBlank()) return null;
        try {
            return TestType.valueOf(typeStr.toUpperCase());
        } catch (IllegalArgumentException e) {
            return null;
        }
    }
}
