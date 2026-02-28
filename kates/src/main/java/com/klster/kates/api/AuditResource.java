package com.klster.kates.api;

import java.util.List;
import java.util.Map;

import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.klster.kates.service.AuditService;

@Path("/api/audit")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Audit")
public class AuditResource {

    @Inject
    AuditService auditService;

    @GET
    @Operation(
            summary = "List audit events",
            description = "Returns recent mutation events, optionally filtered by type and time range")
    @APIResponse(responseCode = "200", description = "List of audit events")
    public Response listAuditEvents(
            @Parameter(description = "Maximum number of events") @QueryParam("limit") @DefaultValue("50") int limit,
            @Parameter(description = "Filter by event type (test, topic, disruption, resilience)") @QueryParam("type") String type,
            @Parameter(description = "Filter events after this ISO-8601 timestamp") @QueryParam("since") String since) {

        List<Map<String, Object>> events = auditService.list(
                Math.max(1, Math.min(limit, 500)),
                type,
                since
        );
        return Response.ok(events).build();
    }
}
