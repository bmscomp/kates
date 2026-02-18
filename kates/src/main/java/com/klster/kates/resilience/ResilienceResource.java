package com.klster.kates.resilience;

import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.klster.kates.api.ApiError;

/**
 * REST endpoint for combined resilience testing (performance + chaos).
 */
@Path("/api/resilience")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Resilience")
public class ResilienceResource {

    @Inject
    ResilienceOrchestrator orchestrator;

    @POST
    @Operation(
            summary = "Execute a resilience test",
            description = "Runs a combined performance + chaos test and returns impact analysis")
    @APIResponse(responseCode = "200", description = "Resilience test report")
    public Response executeResilienceTest(ResilienceTestRequest request) {
        if (request.getTestRequest() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'testRequest' is required"))
                    .build();
        }
        if (request.getChaosSpec() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'chaosSpec' is required"))
                    .build();
        }

        ResilienceReport report = orchestrator.execute(request);
        return Response.ok(report).build();
    }
}
