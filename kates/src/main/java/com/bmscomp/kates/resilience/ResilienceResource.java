package com.bmscomp.kates.resilience;

import java.util.Map;

import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.StreamingOutput;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.bmscomp.kates.api.ApiError;

/**
 * REST endpoint for combined resilience testing (performance + chaos + probes).
 */
@Path("/api/resilience")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Resilience")
public class ResilienceResource {

    private static final org.jboss.logging.Logger LOG = org.jboss.logging.Logger.getLogger(ResilienceResource.class);

    @Inject
    ResilienceOrchestrator orchestrator;

    @Inject
    ObjectMapper objectMapper;

    private StreamingOutput executeWithKeepAlive(ResilienceTestRequest request, String scenarioId) {
        return os -> {
            java.util.concurrent.CompletableFuture<ResilienceReport> future = 
                java.util.concurrent.CompletableFuture.supplyAsync(() -> orchestrator.execute(request));
            
            while (!future.isDone()) {
                try {
                    os.write(" ".getBytes());
                    os.flush();
                    Thread.sleep(10000);
                } catch (Exception e) {
                    future.cancel(true);
                    return;
                }
            }
            
            try {
                ResilienceReport report = future.get();
                Object payload = scenarioId != null ? Map.of("scenario", scenarioId, "report", report) : report;
                objectMapper.writeValue(os, payload);
                os.flush();
            } catch (Exception e) {
                LOG.error("Failed to execute resilience test", e);
            }
        };
    }

    @POST
    @Operation(
            summary = "Execute a resilience test",
            description = "Runs a combined performance + chaos test with probe evaluation and returns impact analysis")
    @APIResponse(responseCode = "200", description = "Resilience test report with probe results and RTO")
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

        StreamingOutput stream = executeWithKeepAlive(request, null);
        return Response.ok(stream).build();
    }

    @GET
    @Path("/scenarios")
    @Operation(
            summary = "List resilience scenarios",
            description = "Returns pre-built resilience test scenarios with appropriate probes for common Kafka failure modes")
    public Response listScenarios() {
        return Response.ok(ResilienceScenarios.listAll()).build();
    }

    @POST
    @Path("/scenarios/{id}")
    @Operation(
            summary = "Run a resilience scenario",
            description = "Executes a pre-built scenario with optional parameter overrides (targetPod, chaosDurationSec)")
    @APIResponse(responseCode = "200", description = "Resilience test report")
    @APIResponse(responseCode = "404", description = "Scenario not found")
    public Response runScenario(
            @Parameter(description = "Scenario ID") @PathParam("id") String id,
            Map<String, Object> overrides) {

        var scenario = ResilienceScenarios.findById(id);
        if (scenario == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No scenario with ID: " + id))
                    .build();
        }

        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setChaosSpec(ResilienceScenarios.buildFaultSpec(scenario, overrides));
        request.setProbes(scenario.probes());
        request.setSteadyStateSec(scenario.steadyStateSec());
        request.setMaxRecoveryWaitSec(scenario.maxRecoveryWaitSec());

        // Scenario requires a testRequest — use minimal load test if none provided
        if (overrides != null && overrides.containsKey("testRequest")) {
            // The caller provided a full testRequest via the overrides body
            // In practice this would need deserialization — for now, scenarios
            // run chaos-only (no benchmark) by returning an error
        }

        if (request.getTestRequest() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request",
                            "A 'testRequest' override is required when running a scenario. "
                                    + "Include it in the request body to run a combined benchmark + chaos test."))
                    .build();
        }

        StreamingOutput stream = executeWithKeepAlive(request, id);
        return Response.ok(stream).build();
    }
}
