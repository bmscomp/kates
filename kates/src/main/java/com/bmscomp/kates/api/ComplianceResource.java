package com.bmscomp.kates.api;

import java.net.URI;
import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.smallrye.common.annotation.Blocking;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;
import org.jboss.logging.Logger;

import com.bmscomp.kates.audit.Actors;
import com.bmscomp.kates.audit.Audited;
import com.bmscomp.kates.compliance.ComplianceCheck;
import com.bmscomp.kates.compliance.ComplianceCheckEntity;
import com.bmscomp.kates.compliance.ComplianceCheckRepository;
import com.bmscomp.kates.compliance.ComplianceReport;
import com.bmscomp.kates.compliance.InvalidReferenceException;
import com.bmscomp.kates.compliance.KafkaReferenceReader;
import com.bmscomp.kates.security.Scopes;
import com.bmscomp.kates.service.AuditService;

/**
 * Checks the cluster against a KafkaReference, and keeps each report as
 * evidence. A check writes nothing to the cluster; it is a POST because the
 * reference is a body.
 */
@RolesAllowed(Scopes.READ)
@Path("/api/conformity")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
@Tag(name = "Compliance")
public class ComplianceResource {

    private static final Logger LOG = Logger.getLogger(ComplianceResource.class);

    private final KafkaReferenceReader reader;
    private final ComplianceCheck check;
    private final ComplianceCheckRepository checks;
    private final Actors actors;
    private final AuditService auditService;
    private final ObjectMapper objectMapper;

    @Inject
    public ComplianceResource(
            KafkaReferenceReader reader,
            ComplianceCheck check,
            ComplianceCheckRepository checks,
            Actors actors,
            AuditService auditService,
            ObjectMapper objectMapper) {
        this.reader = reader;
        this.check = check;
        this.checks = checks;
        this.actors = actors;
        this.auditService = auditService;
        this.objectMapper = objectMapper;
    }

    @Audited(action = "RUN", type = "compliance")
    @POST
    @Path("/check")
    @Consumes(MediaType.APPLICATION_JSON)
    @Operation(
            summary = "Check the cluster against a reference",
            description = "Reads the cluster read-only and reports each declared rule; the verdict is PASS, FAIL or"
                    + " UNKNOWN. The report is stored, and Location names it under /api/conformity/checks")
    @APIResponse(responseCode = "200", description = "The report, whatever the verdict")
    @APIResponse(responseCode = "400", description = "A reference the check cannot run, each field's reason by path")
    @APIResponse(responseCode = "409", description = "The reference is for another cluster, or its Kafka is missing")
    @APIResponse(responseCode = "413", description = "A reference over 256 KiB")
    @APIResponse(
            responseCode = "503",
            description = "The Kubernetes API does not answer, or refuses the Kates API the Kafka CR's get")
    public Response check(byte[] body) {
        ComplianceReport report;
        try {
            report = check.check(reader.read(body));
        } catch (InvalidReferenceException e) {
            return Response.status(e.getError().getStatus())
                    .entity(e.getError())
                    .build();
        }
        ComplianceCheckEntity stored;
        try {
            stored = checks.store(report, body, actors.current());
        } catch (RuntimeException e) {
            // The caller, often a CI gate, still gets its verdict; the missing
            // Location and this line say the evidence was not kept.
            LOG.errorf(e, "Compliance report of %s not stored", report.reference());
            return Response.ok(report).build();
        }
        auditService.record(
                "RUN",
                "compliance",
                stored.getId(),
                "verdict " + report.verdict() + ", report sha256 " + ComplianceCheckRepository.reportSha256(stored)
                        + ", reference sha256 " + stored.getReferenceSha256());
        return Response.ok(report)
                .location(URI.create("/api/conformity/checks/" + stored.getId()))
                .build();
    }

    @GET
    @Path("/checks")
    @Operation(
            summary = "List stored compliance checks",
            description = "Newest first: each check's id, when it ran, its reference, the cluster, the verdict, and"
                    + " who ran it")
    @APIResponse(responseCode = "200", description = "A page of checks")
    @APIResponse(responseCode = "400", description = "since or until is not an ISO-8601 instant")
    public Response list(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size,
            @Parameter(description = "Only checks run at or after this ISO-8601 instant") @QueryParam("since")
                    String since,
            @Parameter(description = "Only checks run before this ISO-8601 instant") @QueryParam("until")
                    String until) {
        Instant from;
        Instant to;
        try {
            from = since == null ? null : Instant.parse(since);
            to = until == null ? null : Instant.parse(until);
        } catch (DateTimeParseException e) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "since and until are ISO-8601 instants: " + e.getMessage()))
                    .build();
        }
        int effectivePage = Math.max(page, 0);
        int effectiveSize = Math.min(Math.max(size, 1), 200);
        List<Map<String, Object>> items = checks.list(from, to, effectivePage * effectiveSize, effectiveSize).stream()
                .map(ComplianceResource::summary)
                .toList();
        return Response.ok(new PagedResponse<>(items, effectivePage, effectiveSize, checks.count(from, to)))
                .build();
    }

    @GET
    @Path("/checks/{id}")
    @Operation(
            summary = "Get a stored compliance check",
            description = "The check's id, who ran it, the SHA-256 of the reference it was sent, its full report,"
                    + " and the SHA-256 of the report as stored now, to compare with the one its audit row recorded")
    @APIResponse(responseCode = "200", description = "The stored check")
    @APIResponse(responseCode = "404", description = "No check has this id")
    public Response get(@Parameter(description = "Check ID") @PathParam("id") String id) {
        ComplianceCheckEntity entity = checks.find(id).orElse(null);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Compliance check not found: " + id))
                    .build();
        }
        Map<String, Object> answer = summary(entity);
        answer.put("referenceSha256", entity.getReferenceSha256());
        answer.put("reportSha256", ComplianceCheckRepository.reportSha256(entity));
        try {
            answer.put("report", objectMapper.readTree(entity.getReportJson()));
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("stored compliance report " + id + " is not JSON", e);
        }
        return Response.ok(answer).build();
    }

    private static Map<String, Object> summary(ComplianceCheckEntity e) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", e.getId());
        m.put("checkedAt", e.getCheckedAt().toString());
        m.put("reference", e.getReference());
        m.put("clusterId", e.getClusterId());
        m.put("verdict", e.getVerdict());
        m.put("actor", e.getActor());
        m.put("principalType", e.getPrincipalType());
        return m;
    }
}
