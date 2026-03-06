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

    private final TestOrchestrator orchestrator;
    private final TestRunRepository repository;

    @Inject
    BaselineService baselineService;

    @Inject
    AuditService auditService;

    @Inject
    public TestResource(TestOrchestrator orchestrator, TestRunRepository repository) {
        this.orchestrator = orchestrator;
        this.repository = repository;
    }

    @POST
    @Operation(
            summary = "Create and execute a test",
            description = "Submits a new performance test run for asynchronous execution")
    @APIResponse(responseCode = "202", description = "Test accepted for execution")
    public Response createTest(@Valid CreateTestRequest request) {
        var result = orchestrator.executeTest(request);
        if (result.isFailure()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", result.asFailure().orElseThrow().getMessage()))
                    .build();
        }
        TestRun run = result.asSuccess().orElseThrow();
        auditService.record("CREATE", "test", run.getId(), request.getType() + " test");
        return Response.accepted(run).build();
    }

    @POST
    @Path("/bulk")
    @Operation(summary = "Create multiple tests", description = "Submits up to 10 test runs in a single request")
    @APIResponse(responseCode = "202", description = "Tests accepted for execution")
    public Response bulkCreate(List<CreateTestRequest> requests) {
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
                    results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.failure(testResult.asFailure().orElseThrow().getMessage()));
                } else {
                    TestRun run = testResult.asSuccess().orElseThrow();
                    auditService.record("CREATE", "test", run.getId(), req.getType() + " bulk test");
                    results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.success(run.getId(), run.getStatus().name()));
                }
            } catch (Exception e) {
                results.add(com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.failure(e.getMessage()));
            }
        }
        return Response.accepted(new com.bmscomp.kates.domain.BulkCreateResponse(results.size(), results))
                .build();
    }

    @DELETE
    @Path("/bulk")
    @Operation(summary = "Delete multiple tests", description = "Deletes test runs by a list of IDs")
    public Response bulkDelete(com.bmscomp.kates.domain.BulkDeleteRequest request) {
        List<String> ids = request != null ? request.ids() : null;
        if (ids == null || ids.isEmpty()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'ids' is required"))
                    .build();
        }
        int deleted = 0, notFound = 0;
        for (String id : ids) {
            var run = repository.findById(id);
            if (run.isPresent()) {
                repository.delete(id);
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
                List<TestRun> content = repository.findByStatus(taskStatus);
                return Response.ok(new PagedResponse<>(content, 0, content.size(), content.size()))
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
    @Operation(summary = "Get a test run", description = "Returns a single test run by ID, refreshing its status")
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
    @Operation(summary = "Delete a test run", description = "Stops the test if running and removes it")
    @APIResponse(responseCode = "204", description = "Test run deleted")
    @APIResponse(responseCode = "404", description = "Test run not found")
    public Response deleteTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        return repository
                .findById(id)
                .map(run -> {
                    orchestrator.stopTest(id);
                    repository.delete(id);
                    auditService.record("DELETE", "test", id, "Test deleted");
                    return Response.noContent().build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(new ApiError(404, "Not Found", "Test run not found: " + id))
                        .build());
    }

    @POST
    @Path("/{id}/cancel")
    @Operation(
            summary = "Cancel a running test",
            description = "Safely stops all tasks and marks the test as CANCELLED")
    @APIResponse(responseCode = "200", description = "Test cancelled")
    @APIResponse(responseCode = "404", description = "Test run not found")
    @APIResponse(responseCode = "409", description = "Test is not running")
    public Response cancelTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        return repository
                .findById(id)
                .map(run -> {
                    var status = run.getStatus();
                    if (status != com.bmscomp.kates.domain.TestResult.TaskStatus.RUNNING
                            && status != com.bmscomp.kates.domain.TestResult.TaskStatus.PENDING) {
                        return Response.status(Response.Status.CONFLICT)
                                .entity(new ApiError(409, "Conflict", "Test is not running (status: " + status + ")"))
                                .build();
                    }
                    orchestrator.stopTest(id);
                    run = run.withStatus(com.bmscomp.kates.domain.TestResult.TaskStatus.FAILED);
                    
                    if (run.getResults() != null) {
                        java.util.List<com.bmscomp.kates.domain.TestResult> updatedResults = new java.util.ArrayList<>();
                        for (var result : run.getResults()) {
                            if (result.getStatus() == com.bmscomp.kates.domain.TestResult.TaskStatus.RUNNING
                                    || result.getStatus() == com.bmscomp.kates.domain.TestResult.TaskStatus.PENDING) {
                                result = result.withStatus(com.bmscomp.kates.domain.TestResult.TaskStatus.FAILED)
                                               .withError("Cancelled by user")
                                               .withEndTime(java.time.Instant.now().toString());
                            }
                            updatedResults.add(result);
                        }
                        run = run.withResults(updatedResults);
                    }
                    
                    repository.save(run);
                    auditService.record("CANCEL", "test", id, "Test cancelled by user");
                    return Response.ok(java.util.Map.of(
                                    "id", run.getId(),
                                    "status", "CANCELLED",
                                    "message", "Test cancelled successfully"))
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
        return new com.bmscomp.kates.domain.BaselineResponse(
            b.getTestType().name(),
            b.getRunId(),
            b.getSetAt()
        );
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
