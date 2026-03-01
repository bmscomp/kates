package com.klster.kates.api;

import java.util.List;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.klster.kates.service.AdvisorService;
import com.klster.kates.service.TestRunRepository;

@Path("/api/tests/{id}/advisor")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Advisor")
public class AdvisorResource {

    @Inject
    TestRunRepository repository;

    @Inject
    AdvisorService advisorService;

    @GET
    @Operation(
            summary = "Analyze test run",
            description = "Runs rule engine against a completed test run to generate tuning recommendations")
    public Response analyze(@Parameter(description = "Test run ID") @PathParam("id") String id) {

        return repository
                .findById(id)
                .map(run -> {
                    List<AdvisorService.Recommendation> recommendations = advisorService.analyze(run);
                    return Response.ok(recommendations).build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(ApiError.of(404, "Not Found", "Test run not found: " + id))
                        .build());
    }
}
