package com.klster.kates.api;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.klster.kates.domain.TestResult;
import com.klster.kates.persistence.ProfileEntity;
import com.klster.kates.service.TestRunRepository;

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
    public List<Map<String, Object>> list() {
        return em.createQuery("FROM ProfileEntity ORDER BY createdAt DESC", ProfileEntity.class)
                .getResultList()
                .stream()
                .map(this::toMap)
                .collect(Collectors.toList());
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
                        double throughput = 0, p50 = 0, p95 = 0, p99 = 0, avg = 0, records = 0;
                        int n = run.getResults().size();
                        for (TestResult r : run.getResults()) {
                            throughput += r.getThroughputRecordsPerSec();
                            p50 += r.getP50LatencyMs();
                            p95 += r.getP95LatencyMs();
                            p99 += r.getP99LatencyMs();
                            avg += r.getAvgLatencyMs();
                            records += r.getRecordsSent();
                        }
                        profile.setThroughput(throughput / n);
                        profile.setP50Ms(p50 / n);
                        profile.setP95Ms(p95 / n);
                        profile.setP99Ms(p99 / n);
                        profile.setAvgMs(avg / n);
                        profile.setRecords(records);
                    }

                    em.persist(profile);
                    return Response.status(Response.Status.CREATED).entity(toMap(profile)).build();
                })
                .orElse(Response.status(Response.Status.NOT_FOUND)
                        .entity(ApiError.of(404, "Not Found", "Test run not found: " + req.runId()))
                        .build());
    }

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
