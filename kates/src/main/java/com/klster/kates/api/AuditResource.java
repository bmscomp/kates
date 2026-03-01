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
            description = "Returns paginated mutation events, optionally filtered by type and time range")
    @APIResponse(responseCode = "200", description = "Paginated list of audit events")
    public Response listAuditEvents(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size,
            @Parameter(description = "Filter by event type (test, topic, disruption, resilience)") @QueryParam("type") String type,
            @Parameter(description = "Filter events after this ISO-8601 timestamp") @QueryParam("since") String since) {

        int effectiveSize = Math.min(Math.max(size, 1), 200);
        List<Map<String, Object>> allEvents = auditService.list(500, type, since);
        int start = Math.min(page * effectiveSize, allEvents.size());
        int end = Math.min(start + effectiveSize, allEvents.size());
        List<Map<String, Object>> paged = allEvents.subList(start, end);
        return Response.ok(Map.of("page", page, "size", effectiveSize,
                "total", allEvents.size(), "count", paged.size(), "items", paged)).build();
    }
}
