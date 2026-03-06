package com.bmscomp.kates.disruption;

import java.util.Map;

import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;

@Path("/api/disruptions/templates")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Disruptions")
public class DisruptionTemplateResource {

    @Inject
    ChaosTemplateCatalog templateCatalog;

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @GET
    @Operation(
            summary = "List chaos experiment templates",
            description = "Returns pre-built chaos templates with sensible defaults for common Kafka failure scenarios")
    public Response listTemplates() {
        return Response.ok(templateCatalog.listTemplates()).build();
    }

    @POST
    @Path("/{id}")
    @Operation(summary = "Run a template", description = "Executes a chaos template with optional override parameters")
    @APIResponse(responseCode = "200", description = "Disruption report from template execution")
    @APIResponse(responseCode = "404", description = "Template not found")
    public Response runTemplate(
            @Parameter(description = "Template ID") @PathParam("id") String templateId,
            Map<String, Object> overrides) {
        try {
            DisruptionPlan plan = templateCatalog.buildPlan(templateId, overrides != null ? overrides : Map.of());
            String id = java.util.UUID.randomUUID().toString().substring(0, 8);
            DisruptionReport report = orchestrator.execute(plan);
            DisruptionPersistence.persistReport(id, report, repository, objectMapper);
            return Response.ok(Map.of("id", id, "template", templateId, "report", report))
                    .build();
        } catch (IllegalArgumentException e) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", e.getMessage()))
                    .build();
        }
    }
}
