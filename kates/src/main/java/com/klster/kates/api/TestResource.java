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

import java.util.List;

@Path("/api/tests")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class TestResource {

    private final TestOrchestrator orchestrator;
    private final TestRunRepository repository;

    @Inject
    public TestResource(TestOrchestrator orchestrator, TestRunRepository repository) {
        this.orchestrator = orchestrator;
        this.repository = repository;
    }

    @POST
    public Response createTest(@Valid CreateTestRequest request) {
        TestRun run = orchestrator.executeTest(request);
        return Response.accepted(run).build();
    }

    @GET
    public Response listTests(
            @QueryParam("type") String type,
            @QueryParam("status") String status,
            @QueryParam("page") @DefaultValue("0") int page,
            @QueryParam("size") @DefaultValue("50") int size) {

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
    public Response getTest(@PathParam("id") String id) {
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
    public Response deleteTest(@PathParam("id") String id) {
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
    public TestType[] getTestTypes() {
        return TestType.values();
    }

    @GET
    @Path("/backends")
    public List<String> getBackends() {
        return orchestrator.availableBackends();
    }
}
