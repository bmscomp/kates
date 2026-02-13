package com.klster.kates.resilience;

import com.klster.kates.api.ApiError;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

/**
 * REST endpoint for combined resilience testing (performance + chaos).
 */
@Path("/api/resilience")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class ResilienceResource {

    @Inject
    ResilienceOrchestrator orchestrator;

    @POST
    public Response executeResilienceTest(ResilienceTestRequest request) {
        if (request.getTestRequest() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'testRequest' is required")).build();
        }
        if (request.getChaosSpec() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'chaosSpec' is required")).build();
        }

        ResilienceReport report = orchestrator.execute(request);
        return Response.ok(report).build();
    }
}
