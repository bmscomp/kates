package com.bmscomp.kates.schedule;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.ConstraintViolationException;
import jakarta.validation.Validator;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.audit.Audited;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.security.Scopes;

/**
 * REST API for managing scheduled/recurring test configurations.
 */
@RolesAllowed(Scopes.READ)
@Path("/api/schedules")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Schedules")
public class ScheduleResource {

    private static final ObjectMapper JSON = new ObjectMapper();

    @Inject
    ScheduledTestRunRepository repository;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    Validator validator;

    /**
     * A 400 naming each field of the schedule's test request that the Kates
     * API would refuse, by its path under {@code testRequest}, or empty when
     * it would run the request. Asked before the schedule is saved: the
     * scheduler fires the request as saved, so a refused one would fail at
     * every firing, start no run, and say why only in the server log.
     */
    private Optional<Response> refusal(CreateTestRequest testRequest) {
        return unknownBackend(testRequest)
                .or(() -> orchestrator
                        .refusal(testRequest)
                        .map(refused -> refused.under("testRequest"))
                        .map(refused -> Response.status(400)
                                .entity(ApiError.validationFailed(refused.getMessage(), refused.getFieldErrors()))
                                .build()));
    }

    /**
     * A 400 naming the backend the request would run on, when the Kates API
     * has none by that name, or empty. TestOrchestrator.refusal() leaves the
     * backend to executeTest, which refuses an unknown one as it starts the
     * run, so each firing would fail on it. The run takes a scenario's own
     * backend when the scenario has phases and names one, and the request's
     * otherwise. Asked before refusal(), whose checks depend on the backend.
     */
    private Optional<Response> unknownBackend(CreateTestRequest testRequest) {
        String field = "testRequest.backend";
        String backend = testRequest.getBackend();
        if (testRequest.isScenario() && testRequest.getScenario().getBackend() != null) {
            field = "testRequest.scenario.backend";
            backend = testRequest.getScenario().getBackend();
        }
        List<String> available = orchestrator.availableBackends();
        if (backend == null || available.contains(backend)) {
            return Optional.empty();
        }
        String why = "the Kates API has no backend '" + backend + "'; set it to " + oneOf(available);
        return Optional.of(Response.status(400)
                .entity(ApiError.validationFailed(field + ": " + why, Map.of(field, why)))
                .build());
    }

    /** The names as "a", "a or b", or "a, b or c". */
    private static String oneOf(List<String> names) {
        if (names.size() < 2) {
            return String.join("", names);
        }
        return String.join(", ", names.subList(0, names.size() - 1)) + " or " + names.getLast();
    }

    @GET
    @Operation(summary = "List all schedules")
    public Response listSchedules() {
        return Response.ok(repository.findAll()).build();
    }

    @GET
    @Path("/{id}")
    @Operation(summary = "Get a schedule")
    @APIResponse(responseCode = "200", description = "Schedule details")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response getSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id) {
        return repository
                .findById(id)
                .map(s -> Response.ok(s).build())
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                        .build());
    }

    @Audited(action = "CREATE", type = "schedule")
    @RolesAllowed(Scopes.ADMIN)
    @POST
    @Operation(summary = "Create a schedule", description = "Creates a new recurring test schedule")
    @APIResponse(responseCode = "201", description = "Schedule created")
    @APIResponse(
            responseCode = "400",
            description = "Invalid request, including a testRequest that POST /api/tests would refuse, or one"
                    + " whose backend the Kates API doesn't have; fieldErrors names each field")
    public Response createSchedule(@jakarta.validation.Valid CreateScheduleRequest request) {
        if (request.name == null || request.name.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'name' is required"))
                    .build();
        }
        if (request.cronExpression == null || request.cronExpression.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'cronExpression' is required"))
                    .build();
        }
        if (request.testRequest == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'testRequest' is required"))
                    .build();
        }
        Optional<Response> refused = refusal(request.testRequest);
        if (refused.isPresent()) {
            return refused.get();
        }

        try {
            ScheduledTestRun schedule = new ScheduledTestRun();
            schedule.setId(UUID.randomUUID().toString().substring(0, 8));
            schedule.setName(request.name);
            schedule.setCronExpression(request.cronExpression);
            schedule.setEnabled(request.enabled);
            // A TestSpec writes only the fields the request set (TestSpec's
            // class comment), so the schedule fires the request it was given
            // and the type defaults of the day fill in the rest.
            schedule.setRequestJson(JSON.writeValueAsString(request.testRequest));
            repository.save(schedule);

            return Response.status(201).entity(schedule).build();
        } catch (JsonProcessingException e) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Invalid test request: " + e.getMessage()))
                    .build();
        }
    }

    @Audited(action = "UPDATE", type = "schedule")
    @RolesAllowed(Scopes.ADMIN)
    @PUT
    @Path("/{id}")
    @Operation(summary = "Update a schedule")
    @APIResponse(responseCode = "200", description = "Schedule updated")
    @APIResponse(
            responseCode = "400",
            description = "A testRequest that POST /api/schedules would refuse, with the body POST answers;"
                    + " fieldErrors names each field")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response updateSchedule(
            @Parameter(description = "Schedule ID") @PathParam("id") String id, CreateScheduleRequest request) {
        return repository
                .findById(id)
                .map(schedule -> {
                    // Only a testRequest the body sends is checked, so a
                    // schedule saved before the check, whose firings the
                    // Kates API refuses, can still be renamed or disabled.
                    if (request.testRequest != null) {
                        // Held to the constraints POST's @Valid holds its
                        // testRequest to, such as a type and at most 100
                        // producers, and answered as POST is, by
                        // ConstraintViolationExceptionMapper. A PUT can't take
                        // the @Valid itself: it would also require a name and
                        // a cronExpression, which a PUT may leave out.
                        Set<ConstraintViolation<CreateTestRequest>> violations =
                                validator.validate(request.testRequest);
                        if (!violations.isEmpty()) {
                            throw new ConstraintViolationException(violations);
                        }
                        Optional<Response> refused = refusal(request.testRequest);
                        if (refused.isPresent()) {
                            return refused.get();
                        }
                    }
                    if (request.name != null) schedule.setName(request.name);
                    if (request.cronExpression != null) schedule.setCronExpression(request.cronExpression);
                    schedule.setEnabled(request.enabled);
                    if (request.testRequest != null) {
                        try {
                            schedule.setRequestJson(JSON.writeValueAsString(request.testRequest));
                        } catch (JsonProcessingException e) {
                            throw new RuntimeException(e);
                        }
                    }
                    repository.save(schedule);
                    return Response.ok(schedule).build();
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                        .build());
    }

    @Audited(action = "DELETE", type = "schedule")
    @RolesAllowed(Scopes.ADMIN)
    @DELETE
    @Path("/{id}")
    @Operation(summary = "Delete a schedule")
    @APIResponse(responseCode = "204", description = "Schedule deleted")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response deleteSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id) {
        return repository
                .findById(id)
                .map(s -> {
                    repository.delete(id);
                    return Response.noContent().build();
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                        .build());
    }

    public static class CreateScheduleRequest {
        @jakarta.validation.constraints.NotBlank(message = "name is required")
        @jakarta.validation.constraints.Size(max = 255)
        public String name;

        @jakarta.validation.constraints.NotBlank(message = "cronExpression is required")
        @jakarta.validation.constraints.Size(max = 120)
        public String cronExpression;

        public boolean enabled = true;

        @jakarta.validation.Valid
        public CreateTestRequest testRequest;
    }
}
