package com.bmscomp.kates.disruption;

import java.util.List;
import java.util.Map;
import java.util.UUID;

import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.PUT;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;

@Path("/api/disruptions/schedules")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Disruptions")
public class DisruptionScheduleResource {

    @Inject
    EntityManager em;

    @Inject
    ObjectMapper objectMapper;

    @GET
    @Operation(summary = "List disruption schedules")
    public Response listSchedules(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {

        int effectiveSize = Math.min(Math.max(size, 1), 200);
        List<DisruptionScheduleEntity> schedules = em.createQuery(
                        "SELECT s FROM DisruptionScheduleEntity s ORDER BY s.createdAt DESC",
                        DisruptionScheduleEntity.class)
                .setFirstResult(page * effectiveSize)
                .setMaxResults(effectiveSize)
                .getResultList();

        var entries = schedules.stream()
                .map(s -> Map.of(
                        "id", s.getId(),
                        "name", s.getName(),
                        "cronExpression", s.getCronExpression(),
                        "enabled", s.isEnabled(),
                        "playbookName", s.getPlaybookName() != null ? s.getPlaybookName() : "",
                        "lastRunId", s.getLastRunId() != null ? s.getLastRunId() : "",
                        "lastRunAt", s.getLastRunAt() != null ? s.getLastRunAt().toString() : "",
                        "createdAt", s.getCreatedAt().toString()))
                .toList();
        return Response.ok(Map.of("page", page, "size", effectiveSize, "count", entries.size(), "items", entries))
                .build();
    }

    @POST
    @Transactional
    @Operation(
            summary = "Create a disruption schedule",
            description = "Creates a recurring disruption schedule from a playbook or plan")
    @APIResponse(responseCode = "201", description = "Schedule created")
    public Response createSchedule(DisruptionDtos.CreateDisruptionScheduleRequest request) {
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
        if ((request.playbookName == null || request.playbookName.isBlank()) && request.plan == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Either 'playbookName' or 'plan' is required"))
                    .build();
        }

        DisruptionScheduleEntity entity = new DisruptionScheduleEntity();
        entity.setId(UUID.randomUUID().toString().substring(0, 8));
        entity.setName(request.name);
        entity.setCronExpression(request.cronExpression);
        entity.setEnabled(request.enabled);
        entity.setPlaybookName(request.playbookName);

        if (request.plan != null) {
            try {
                entity.setPlanJson(objectMapper.writeValueAsString(request.plan));
            } catch (JsonProcessingException e) {
                return Response.status(400)
                        .entity(ApiError.of(400, "Bad Request", "Invalid plan: " + e.getMessage()))
                        .build();
            }
        }

        em.persist(entity);
        return Response.status(201).entity(entity).build();
    }

    @PUT
    @Path("/{id}")
    @Transactional
    @Operation(summary = "Update a disruption schedule")
    @APIResponse(responseCode = "200", description = "Schedule updated")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response updateSchedule(
            @Parameter(description = "Schedule ID") @PathParam("id") String id,
            DisruptionDtos.CreateDisruptionScheduleRequest request) {
        DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                    .build();
        }

        if (request.name != null) entity.setName(request.name);
        if (request.cronExpression != null) entity.setCronExpression(request.cronExpression);
        entity.setEnabled(request.enabled);
        if (request.playbookName != null) entity.setPlaybookName(request.playbookName);
        if (request.plan != null) {
            try {
                entity.setPlanJson(objectMapper.writeValueAsString(request.plan));
            } catch (JsonProcessingException e) {
                return Response.status(400)
                        .entity(ApiError.of(400, "Bad Request", "Invalid plan"))
                        .build();
            }
        }
        em.merge(entity);
        return Response.ok(entity).build();
    }

    @DELETE
    @Path("/{id}")
    @Transactional
    @Operation(summary = "Delete a disruption schedule")
    @APIResponse(responseCode = "204", description = "Schedule deleted")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response deleteSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id) {
        DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                    .build();
        }
        em.remove(entity);
        return Response.noContent().build();
    }
}
