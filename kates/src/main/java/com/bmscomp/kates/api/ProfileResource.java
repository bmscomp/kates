package com.bmscomp.kates.api;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.stream.Collectors;
import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.audit.Audited;
import com.bmscomp.kates.persistence.ProfileEntity;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.security.Scopes;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.util.MetricUtils;

@RolesAllowed(Scopes.READ)
@Path("/api/profiles")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Profiles")
public class ProfileResource {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository testRepo;

    record SaveProfileRequest(String name, String runId) {}

    @GET
    @Operation(summary = "List all profiles")
    public Response list(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {
        int effectiveSize = Math.min(Math.max(size, 1), 200);
        var profiles = em
                .createQuery("FROM ProfileEntity ORDER BY createdAt DESC", ProfileEntity.class)
                .setFirstResult(page * effectiveSize)
                .setMaxResults(effectiveSize)
                .getResultList()
                .stream()
                .map(this::toMap)
                .collect(Collectors.toList());
        return Response.ok(java.util.Map.of(
                        "page", page, "size", effectiveSize, "count", profiles.size(), "items", profiles))
                .build();
    }

    @GET
    @Path("/{name}")
    @Operation(summary = "Get profile by name")
    public Response get(@Parameter(description = "Profile name") @PathParam("name") String name) {
        var results = em.createQuery("FROM ProfileEntity WHERE name = :name", ProfileEntity.class)
                .setParameter("name", name)
                .getResultList();
        if (results.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(ApiError.of(404, "Not Found", "Profile not found: " + name))
                    .build();
        }
        return Response.ok(toMap(results.getFirst())).build();
    }

    @Audited(action = "UPDATE", type = "profile")
    @RolesAllowed(Scopes.ADMIN)
    @POST
    @Consumes(MediaType.APPLICATION_JSON)
    @Operation(summary = "Save a profile from a test run")
    @Transactional
    public Response save(SaveProfileRequest req) {
        if (req.name() == null || req.runId() == null) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity(ApiError.of(400, "Bad Request", "name and runId are required"))
                    .build();
        }

        return testRepo.findById(req.runId())
                .map(run -> {
                    ProfileEntity profile = new ProfileEntity(
                            req.name(),
                            run.getTestType() != null ? run.getTestType().name() : "UNKNOWN",
                            req.runId());

                    if (!run.getResults().isEmpty()) {
                        // The run's summary, so a profile's latencies match its
                        // report's rather than averaging the consumer's 0s in.
                        ReportSummary summary = MetricUtils.computeSummary(run.getResults());
                        profile.setThroughput(summary.avgThroughputRecPerSec());
                        profile.setP50Ms(summary.p50LatencyMs());
                        profile.setP95Ms(summary.p95LatencyMs());
                        profile.setP99Ms(summary.p99LatencyMs());
                        profile.setAvgMs(summary.avgLatencyMs());
                        profile.setRecords((double) summary.totalRecords());
                    }

                    em.persist(profile);
                    return Response.status(Response.Status.CREATED)
                            .entity(toMap(profile))
                            .build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(ApiError.of(404, "Not Found", "Test run not found: " + req.runId()))
                        .build());
    }

    @Audited(action = "DELETE", type = "profile")
    @RolesAllowed(Scopes.ADMIN)
    @DELETE
    @Path("/{name}")
    @Operation(summary = "Delete a profile")
    @Transactional
    public Response delete(@Parameter(description = "Profile name") @PathParam("name") String name) {
        int deleted = em.createQuery("DELETE FROM ProfileEntity WHERE name = :name")
                .setParameter("name", name)
                .executeUpdate();
        if (deleted == 0) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(ApiError.of(404, "Not Found", "Profile not found: " + name))
                    .build();
        }
        return Response.noContent().build();
    }

    private Map<String, Object> toMap(ProfileEntity p) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("name", p.getName());
        m.put("testType", p.getTestType());
        m.put("runId", p.getRunId());
        m.put("throughput", p.getThroughput());
        m.put("p50Ms", p.getP50Ms());
        m.put("p95Ms", p.getP95Ms());
        m.put("p99Ms", p.getP99Ms());
        m.put("avgMs", p.getAvgMs());
        m.put("records", p.getRecords());
        m.put("createdAt", p.getCreatedAt() != null ? p.getCreatedAt().toString() : null);
        return m;
    }
}
