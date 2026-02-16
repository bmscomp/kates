package com.klster.kates.schedule;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.klster.kates.api.ApiError;
import com.klster.kates.domain.CreateTestRequest;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import java.util.UUID;

/**
 * REST API for managing scheduled/recurring test configurations.
 */
@Path("/api/schedules")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Schedules")
public class ScheduleResource {

    private static final ObjectMapper JSON = new ObjectMapper();

    @Inject
    ScheduledTestRunRepository repository;

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
        return repository.findById(id)
                .map(s -> Response.ok(s).build())
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id)).build());
    }

    @POST
    @Operation(summary = "Create a schedule", description = "Creates a new recurring test schedule")
    @APIResponse(responseCode = "201", description = "Schedule created")
    public Response createSchedule(CreateScheduleRequest request) {
        if (request.name == null || request.name.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'name' is required")).build();
        }
        if (request.cronExpression == null || request.cronExpression.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'cronExpression' is required")).build();
        }
        if (request.testRequest == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'testRequest' is required")).build();
        }

        try {
            ScheduledTestRun schedule = new ScheduledTestRun();
            schedule.setId(UUID.randomUUID().toString().substring(0, 8));
            schedule.setName(request.name);
            schedule.setCronExpression(request.cronExpression);
            schedule.setEnabled(request.enabled);
            schedule.setRequestJson(JSON.writeValueAsString(request.testRequest));
            repository.save(schedule);

            return Response.status(201).entity(schedule).build();
        } catch (JsonProcessingException e) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Invalid test request: " + e.getMessage())).build();
        }
    }

    @PUT
    @Path("/{id}")
    @Operation(summary = "Update a schedule")
    @APIResponse(responseCode = "200", description = "Schedule updated")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response updateSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id, CreateScheduleRequest request) {
        return repository.findById(id)
                .map(schedule -> {
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
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id)).build());
    }

    @DELETE
    @Path("/{id}")
    @Operation(summary = "Delete a schedule")
    @APIResponse(responseCode = "204", description = "Schedule deleted")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response deleteSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id) {
        return repository.findById(id)
                .map(s -> {
                    repository.delete(id);
                    return Response.noContent().build();
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id)).build());
    }

    public static class CreateScheduleRequest {
        public String name;
        public String cronExpression;
        public boolean enabled = true;
        public CreateTestRequest testRequest;
    }
}
