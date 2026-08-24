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
    DisruptionLauncher launcher;

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
    @Operation(
            summary = "Run a playbook",
            description = "Validates a pre-defined disruption playbook and starts it asynchronously."
                    + " Returns 202 with a report id; poll GET /api/disruptions/{id} for progress"
                    + " and the final report.")
    @APIResponse(responseCode = "202", description = "Playbook accepted for execution")
    @APIResponse(responseCode = "404", description = "Playbook not found")
    @APIResponse(responseCode = "409", description = "Another disruption is already running against this cluster")
    @APIResponse(responseCode = "422", description = "Playbook rejected by safety guard")
    public Response runPlaybook(@Parameter(description = "Playbook name") @PathParam("name") String name) {
        return playbookCatalog
                .findByName(name)
                .map(entry -> {
                    DisruptionPlan plan = playbookCatalog.toPlan(entry);
                    // Goes through the same launcher as POST /api/disruptions.
                    // This path used to call the orchestrator directly: no safety
                    // validation at all, and a synchronous call that held the
                    // request open for the whole plan.
                    return DisruptionResource.toResponse(launcher.launch(plan), plan.getName());
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Playbook not found: " + name))
                        .build());
    }
}
