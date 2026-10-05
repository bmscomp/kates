package com.bmscomp.kates.resilience;

import java.util.Map;
import java.util.Optional;
import java.util.stream.Collectors;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.StreamingOutput;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.domain.TestSpec;

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
    com.bmscomp.kates.engine.TestOrchestrator testOrchestrator;

    @Inject
    ObjectMapper objectMapper;

    @Inject
    com.bmscomp.kates.engine.KatesExecutor executor;

    @Inject
    FaultLimits faultLimits;

    @Inject
    com.bmscomp.kates.engine.SpecLimits specLimits;

    /**
     * A 400 naming each parameter of the chaos spec outside the chaos limits,
     * or empty when all are within them. Asked before the stream starts: the
     * coordinator refuses such a fault too, but only after the benchmark has
     * run for steadyStateSec, and as a report with status ERROR.
     */
    private Optional<Response> outsideLimits(FaultSpec chaosSpec) {
        Map<String, String> found = faultLimits.violations(chaosSpec);
        if (found.isEmpty()) {
            return Optional.empty();
        }
        String message = found.entrySet().stream()
                .map(e -> "chaosSpec." + e.getKey() + ": " + e.getValue())
                .collect(Collectors.joining("; "));
        return Optional.of(Response.status(400)
                .entity(ApiError.validationFailed(message, found))
                .build());
    }

    /**
     * A 400 naming each value of the testRequest's spec outside the limits
     * TestSpec sets, or empty when all are within them. POST /api/tests checks
     * its spec by bean validation, which can't run on the whole testRequest
     * here: it would also require the request's own type, which a scenario
     * with a type of its own goes without. So the spec is checked on its own,
     * and keyed by field name, as bean validation keys it. A scenario's specs
     * are refusal()'s to check, as on POST /api/tests.
     */
    private Optional<Response> outsideSpecLimits(TestSpec spec) {
        Map<String, String> found = specLimits.violations(spec);
        if (found.isEmpty()) {
            return Optional.empty();
        }
        String message = found.entrySet().stream()
                .map(e -> "spec." + e.getKey() + ": " + e.getValue())
                .collect(Collectors.joining("; "));
        return Optional.of(Response.status(400)
                .entity(ApiError.validationFailed(message, found))
                .build());
    }

    private StreamingOutput executeWithKeepAlive(ResilienceTestRequest request, String scenarioId) {
        return os -> {
            java.util.concurrent.CompletableFuture<ResilienceReport> future =
                    java.util.concurrent.CompletableFuture.supplyAsync(
                            () -> orchestrator.execute(request), executor.get());

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
    @APIResponse(
            responseCode = "400",
            description = "Invalid request, including a testRequest with no type, a testRequest spec value outside"
                    + " its limits, a testRequest spec field the test type or backend cannot apply, or a chaosSpec"
                    + " parameter outside the chaos limits; fieldErrors names each field")
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

        // The spec limits first, as on POST /api/tests, where bean validation
        // answers before the orchestrator is asked.
        Optional<Response> outsideSpecLimits =
                outsideSpecLimits(request.getTestRequest().getSpec());
        if (outsideSpecLimits.isPresent()) {
            return outsideSpecLimits.get();
        }
        // Checked here because the answer is a stream: once the keep-alive
        // bytes have gone out the status is 200, and a test request the
        // orchestrator refuses could only end the report as ERROR.
        var refused = testOrchestrator.refusal(request.getTestRequest());
        if (refused.isPresent()) {
            return Response.status(400)
                    .entity(ApiError.validationFailed(
                            refused.get().getMessage(), refused.get().getFieldErrors()))
                    .build();
        }
        Optional<Response> outsideLimits = outsideLimits(request.getChaosSpec());
        if (outsideLimits.isPresent()) {
            return outsideLimits.get();
        }

        StreamingOutput stream = executeWithKeepAlive(request, null);
        return Response.ok(stream).build();
    }

    @GET
    @Path("/scenarios")
    @Operation(
            summary = "List resilience scenarios",
            description =
                    "Returns pre-built resilience test scenarios with appropriate probes for common Kafka failure modes")
    public Response listScenarios() {
        return Response.ok(ResilienceScenarios.listAll()).build();
    }

    @POST
    @Path("/scenarios/{id}")
    @Operation(
            summary = "Run a resilience scenario",
            description =
                    "Executes a pre-built scenario with optional parameter overrides (targetPod, chaosDurationSec)")
    @APIResponse(responseCode = "200", description = "Resilience test report")
    @APIResponse(responseCode = "404", description = "Scenario not found")
    public Response runScenario(
            @Parameter(description = "Scenario ID") @PathParam("id") String id, Map<String, Object> overrides) {

        var scenario = ResilienceScenarios.findById(id);
        if (scenario == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No scenario with ID: " + id))
                    .build();
        }

        ResilienceTestRequest request = new ResilienceTestRequest();
        request.setChaosSpec(ResilienceScenarios.buildFaultSpec(scenario, overrides));
        Optional<Response> outsideLimits = outsideLimits(request.getChaosSpec());
        if (outsideLimits.isPresent()) {
            return outsideLimits.get();
        }
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
                    .entity(ApiError.of(
                            400,
                            "Bad Request",
                            "A 'testRequest' override is required when running a scenario. "
                                    + "Include it in the request body to run a combined benchmark + chaos test."))
                    .build();
        }

        StreamingOutput stream = executeWithKeepAlive(request, id);
        return Response.ok(stream).build();
    }
}
