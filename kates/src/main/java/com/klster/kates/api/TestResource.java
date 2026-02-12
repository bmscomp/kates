package com.klster.kates.api;

import com.klster.kates.domain.CreateTestRequest;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestType;
import com.klster.kates.service.TestExecutionService;
import com.klster.kates.service.TestRunRepository;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
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

    @Inject
    TestExecutionService executionService;

    @Inject
    TestRunRepository repository;

    @POST
    public Response createTest(CreateTestRequest request) {
        if (request.getType() == null) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity("{\"error\": \"type is required\"}")
                    .build();
        }

        TestRun run = executionService.executeTest(request);
        return Response.accepted(run).build();
    }

    @GET
    public List<TestRun> listTests(@QueryParam("type") String type) {
        if (type != null && !type.isEmpty()) {
            try {
                TestType testType = TestType.valueOf(type.toUpperCase());
                return repository.findByType(testType);
            } catch (IllegalArgumentException e) {
                return repository.findAll();
            }
        }
        return repository.findAll();
    }

    @GET
    @Path("/{id}")
    public Response getTest(@PathParam("id") String id) {
        return repository.findById(id)
                .map(run -> {
                    executionService.refreshStatus(id);
                    return Response.ok(run).build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity("{\"error\": \"Test run not found: " + id + "\"}")
                        .build());
    }

    @DELETE
    @Path("/{id}")
    public Response deleteTest(@PathParam("id") String id) {
        return repository.findById(id)
                .map(run -> {
                    executionService.stopTest(id);
                    repository.delete(id);
                    return Response.noContent().build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity("{\"error\": \"Test run not found: " + id + "\"}")
                        .build());
    }

    @GET
    @Path("/types")
    public TestType[] getTestTypes() {
        return TestType.values();
    }
}
