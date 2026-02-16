package com.klster.kates.api;

import com.klster.kates.domain.CreateTestRequest;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestType;
import com.klster.kates.engine.TestOrchestrator;
import com.klster.kates.service.TestRunRepository;
import jakarta.inject.Inject;
import jakarta.validation.Valid;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import java.util.List;

@Path("/api/tests")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Tests")
public class TestResource {

    private final TestOrchestrator orchestrator;
    private final TestRunRepository repository;

    @Inject
    public TestResource(TestOrchestrator orchestrator, TestRunRepository repository) {
        this.orchestrator = orchestrator;
        this.repository = repository;
    }

    @POST
    @Operation(summary = "Create and execute a test", description = "Submits a new performance test run for asynchronous execution")
    @APIResponse(responseCode = "202", description = "Test accepted for execution")
    public Response createTest(@Valid CreateTestRequest request) {
        TestRun run = orchestrator.executeTest(request);
        return Response.accepted(run).build();
    }

    @GET
    @Operation(summary = "List test runs", description = "Returns paginated test runs, optionally filtered by type or status")
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
                return Response.ok(new PagedResponse<>(content, safePage, safeSize, total)).build();
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
                return Response.ok(new PagedResponse<>(content, 0, content.size(), content.size())).build();
            } catch (IllegalArgumentException e) {
                return Response.status(Response.Status.BAD_REQUEST)
                        .entity(new ApiError(400, "Bad Request", "Invalid status: " + status))
                        .build();
            }
        }

        List<TestRun> content = repository.findAllPaged(safePage, safeSize);
        long total = repository.countAll();
        return Response.ok(new PagedResponse<>(content, safePage, safeSize, total)).build();
    }

    @GET
    @Path("/{id}")
    @Operation(summary = "Get a test run", description = "Returns a single test run by ID, refreshing its status")
    @APIResponse(responseCode = "200", description = "Test run details")
    @APIResponse(responseCode = "404", description = "Test run not found")
    public Response getTest(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        return repository.findById(id)
                .map(run -> {
                    orchestrator.refreshStatus(id);
                    return Response.ok(run).build();
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
        return repository.findById(id)
                .map(run -> {
                    orchestrator.stopTest(id);
                    repository.delete(id);
                    return Response.noContent().build();
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
}
