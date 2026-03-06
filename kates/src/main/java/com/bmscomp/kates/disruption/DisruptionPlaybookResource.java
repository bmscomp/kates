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

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;

@Path("/api/disruptions/playbooks")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Disruptions")
public class DisruptionPlaybookResource {

    @Inject
    DisruptionPlaybookCatalog playbookCatalog;

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    com.fasterxml.jackson.databind.ObjectMapper objectMapper;

    @GET
    @Operation(summary = "List disruption playbooks", description = "Returns pre-defined disruption scenarios")
    public Response listPlaybooks() {
        var entries = playbookCatalog.listAll().stream()
                .map(p -> Map.of(
                        "name", p.name,
                        "description", p.description,
                        "category", p.category,
                        "steps", p.steps != null ? p.steps.size() : 0))
                .toList();
        return Response.ok(entries).build();
    }

    @POST
    @Path("/{name}")
    @Operation(summary = "Run a playbook", description = "Executes a pre-defined disruption playbook by name")
    @APIResponse(responseCode = "200", description = "Disruption report from playbook execution")
    @APIResponse(responseCode = "404", description = "Playbook not found")
    public Response runPlaybook(@Parameter(description = "Playbook name") @PathParam("name") String name) {
        return playbookCatalog
                .findByName(name)
                .map(entry -> {
                    DisruptionPlan plan = playbookCatalog.toPlan(entry);
                    String id = java.util.UUID.randomUUID().toString().substring(0, 8);
                    DisruptionReport report = orchestrator.execute(plan);
                    DisruptionPersistence.persistReport(id, report, repository, objectMapper);
                    return Response.ok(Map.of("id", id, "report", report)).build();
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Playbook not found: " + name))
                        .build());
    }
}
