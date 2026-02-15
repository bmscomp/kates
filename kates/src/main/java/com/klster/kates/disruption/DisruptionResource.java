package com.klster.kates.disruption;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.klster.kates.api.ApiError;
import com.klster.kates.chaos.DisruptionType;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import java.util.*;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * REST endpoints for Kubernetes-aware disruption testing
 * with Kafka intelligence, safety guardrails, SLA grading, and persistent storage.
 */
@Path("/api/disruptions")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class DisruptionResource {

    private static final Logger LOG = Logger.getLogger(DisruptionResource.class.getName());

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionSafetyGuard safetyGuard;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @POST
    public Response executeDisruption(
            DisruptionPlan plan,
            @QueryParam("dryRun") @DefaultValue("false") boolean dryRun) {

        if (plan.getSteps() == null || plan.getSteps().isEmpty()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "At least one disruption step is required"))
                    .build();
        }

        if (dryRun) {
            LOG.info("Dry-run for disruption plan: " + plan.getName());
            DisruptionSafetyGuard.DryRunResult result = safetyGuard.dryRun(plan);
            return Response.ok(result).build();
        }

        String id = UUID.randomUUID().toString().substring(0, 8);
        LOG.info("Starting disruption plan '" + plan.getName() + "' with ID: " + id);

        DisruptionReport report = orchestrator.execute(plan);

        persistReport(id, report);

        if ("REJECTED".equals(report.getStatus())) {
            return Response.status(422)
                    .entity(Map.of("id", id, "status", "REJECTED",
                            "validationWarnings", report.getValidationWarnings() != null
                                    ? report.getValidationWarnings() : List.of()))
                    .build();
        }

        return Response.ok(Map.of("id", id, "report", report)).build();
    }

    @GET
    public Response listReports(
            @QueryParam("limit") @DefaultValue("20") int limit,
            @QueryParam("planName") String planName) {

        List<DisruptionReportEntity> entities;
        if (planName != null && !planName.isBlank()) {
            entities = repository.findByPlanName(planName);
        } else {
            entities = repository.listRecent(limit);
        }

        var summaries = entities.stream()
                .map(e -> Map.of(
                        "id", e.getId(),
                        "planName", e.getPlanName(),
                        "status", e.getStatus(),
                        "slaGrade", e.getSlaGrade() != null ? e.getSlaGrade() : "-",
                        "createdAt", e.getCreatedAt().toString()))
                .toList();

        return Response.ok(summaries).build();
    }

    @GET
    @Path("/{id}")
    public Response getReport(@PathParam("id") String id) {
        DisruptionReport report = loadReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }
        return Response.ok(report).build();
    }

    @GET
    @Path("/{id}/timeline")
    public Response getTimeline(@PathParam("id") String id) {
        DisruptionReport report = loadReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }

        var timelines = report.getStepReports().stream()
                .map(step -> Map.of(
                        "step", step.stepName(),
                        "type", step.disruptionType() != null ? step.disruptionType().name() : "unknown",
                        "events", step.podTimeline(),
                        "timeToFirstReady", step.timeToFirstReady() != null
                                ? step.timeToFirstReady().toMillis() + "ms" : "N/A",
                        "timeToAllReady", step.timeToAllReady() != null
                                ? step.timeToAllReady().toMillis() + "ms" : "N/A"))
                .toList();

        return Response.ok(timelines).build();
    }

    @GET
    @Path("/{id}/kafka-metrics")
    public Response getKafkaMetrics(@PathParam("id") String id) {
        DisruptionReport report = loadReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }

        var metrics = report.getStepReports().stream()
                .map(step -> {
                    Map<String, Object> entry = new LinkedHashMap<>();
                    entry.put("step", step.stepName());
                    entry.put("disruptionType", step.disruptionType() != null
                            ? step.disruptionType().name() : "unknown");

                    if (step.targetedLeaderBrokerId() != null) {
                        entry.put("targetedLeaderBrokerId", step.targetedLeaderBrokerId());
                    }

                    if (step.isrMetrics() != null) {
                        Map<String, Object> isr = new LinkedHashMap<>();
                        isr.put("timeToFullIsr", step.isrMetrics().timeToFullIsr() != null
                                ? step.isrMetrics().timeToFullIsr().toMillis() + "ms" : "N/A");
                        isr.put("minIsrDepth", step.isrMetrics().minIsrDepth());
                        isr.put("underReplicatedPeakCount", step.isrMetrics().underReplicatedPeakCount());
                        isr.put("totalPartitions", step.isrMetrics().totalPartitions());
                        entry.put("isr", isr);
                    }

                    if (step.lagMetrics() != null) {
                        Map<String, Object> lag = new LinkedHashMap<>();
                        lag.put("baselineLag", step.lagMetrics().baselineLag());
                        lag.put("peakLag", step.lagMetrics().peakLag());
                        lag.put("lagSpike", step.lagMetrics().lagSpike());
                        lag.put("timeToLagRecovery", step.lagMetrics().timeToLagRecovery() != null
                                ? step.lagMetrics().timeToLagRecovery().toMillis() + "ms" : "N/A");
                        entry.put("lag", lag);
                    }

                    if (step.rolledBack()) {
                        entry.put("rolledBack", true);
                        entry.put("rollbackReason", step.rollbackReason());
                    }

                    return entry;
                })
                .toList();

        return Response.ok(metrics).build();
    }

    @GET
    @Path("/{id}/compare")
    public Response compareReports(
            @PathParam("id") String id,
            @QueryParam("baselineId") String baselineId) {

        if (baselineId == null || baselineId.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "baselineId query parameter is required"))
                    .build();
        }

        DisruptionReport current = loadReport(id);
        DisruptionReport baseline = loadReport(baselineId);

        if (current == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No report with ID: " + id))
                    .build();
        }
        if (baseline == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No baseline report with ID: " + baselineId))
                    .build();
        }

        Map<String, Object> comparison = new LinkedHashMap<>();
        comparison.put("currentId", id);
        comparison.put("baselineId", baselineId);

        if (current.getSummary() != null && baseline.getSummary() != null) {
            Map<String, Object> deltas = new LinkedHashMap<>();

            deltas.put("recoveryDeltaMs",
                    durationDeltaMs(current.getSummary().worstRecovery(),
                            baseline.getSummary().worstRecovery()));
            deltas.put("throughputDeltaPercent",
                    current.getSummary().avgThroughputDegradation()
                            - baseline.getSummary().avgThroughputDegradation());
            deltas.put("p99DeltaPercent",
                    current.getSummary().maxP99LatencySpike()
                            - baseline.getSummary().maxP99LatencySpike());

            comparison.put("deltas", deltas);

            comparison.put("current", Map.of(
                    "status", current.getStatus(),
                    "slaGrade", current.getSlaVerdict() != null ? current.getSlaVerdict().grade() : "-",
                    "passedSteps", current.getSummary().passedSteps() + "/" + current.getSummary().totalSteps()));
            comparison.put("baseline", Map.of(
                    "status", baseline.getStatus(),
                    "slaGrade", baseline.getSlaVerdict() != null ? baseline.getSlaVerdict().grade() : "-",
                    "passedSteps", baseline.getSummary().passedSteps() + "/" + baseline.getSummary().totalSteps()));
        }

        return Response.ok(comparison).build();
    }

    @GET
    @Path("/types")
    public Response listTypes() {
        var types = Arrays.stream(DisruptionType.values())
                .map(t -> Map.of("name", t.name(), "description", describeType(t)))
                .toList();
        return Response.ok(types).build();
    }

    private void persistReport(String id, DisruptionReport report) {
        try {
            String grade = report.getSlaVerdict() != null ? report.getSlaVerdict().grade() : null;
            String reportJson = objectMapper.writeValueAsString(report);
            String summaryJson = report.getSummary() != null
                    ? objectMapper.writeValueAsString(report.getSummary()) : null;

            DisruptionReportEntity entity = new DisruptionReportEntity(
                    id, report.getPlanName(), report.getStatus(),
                    grade, reportJson, summaryJson);
            repository.save(entity);
            LOG.info("Persisted disruption report: " + id);
        } catch (JsonProcessingException e) {
            LOG.log(Level.WARNING, "Failed to serialize report for persistence", e);
        }
    }

    private DisruptionReport loadReport(String id) {
        DisruptionReportEntity entity = repository.findById(id);
        if (entity == null) return null;

        try {
            return objectMapper.readValue(entity.getReportJson(), DisruptionReport.class);
        } catch (JsonProcessingException e) {
            LOG.log(Level.WARNING, "Failed to deserialize report: " + id, e);
            return null;
        }
    }

    private long durationDeltaMs(java.time.Duration a, java.time.Duration b) {
        long aMs = a != null ? a.toMillis() : 0;
        long bMs = b != null ? b.toMillis() : 0;
        return aMs - bMs;
    }

    private String describeType(DisruptionType type) {
        return switch (type) {
            case POD_KILL -> "Force-delete a broker pod (SIGKILL, gracePeriod=0)";
            case POD_DELETE -> "Gracefully delete a broker pod (SIGTERM with grace period)";
            case NETWORK_PARTITION -> "Isolate a broker from peers via NetworkPolicy";
            case NETWORK_LATENCY -> "Inject network latency on broker interfaces";
            case CPU_STRESS -> "Exhaust CPU on broker container";
            case DISK_FILL -> "Fill broker log directory to simulate storage pressure";
            case ROLLING_RESTART -> "Trigger StatefulSet rolling restart";
            case LEADER_ELECTION -> "Kill the controller broker to force leader election";
            case SCALE_DOWN -> "Reduce StatefulSet replica count";
            case NODE_DRAIN -> "Drain the Kubernetes node hosting a broker";
        };
    }

    @Inject
    DisruptionPlaybookCatalog playbookCatalog;

    @Inject
    jakarta.persistence.EntityManager em;

    @GET
    @Path("/playbooks")
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
    @Path("/playbooks/{name}")
    public Response runPlaybook(@PathParam("name") String name) {
        return playbookCatalog.findByName(name)
                .map(entry -> {
                    DisruptionPlan plan = playbookCatalog.toPlan(entry);
                    String id = UUID.randomUUID().toString().substring(0, 8);
                    DisruptionReport report = orchestrator.execute(plan);
                    persistReport(id, report);
                    return Response.ok(Map.of("id", id, "report", report)).build();
                })
                .orElseGet(() -> Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Playbook not found: " + name))
                        .build());
    }

    @GET
    @Path("/schedules")
    public Response listSchedules() {
        List<DisruptionScheduleEntity> schedules = em.createQuery(
                "SELECT s FROM DisruptionScheduleEntity s ORDER BY s.createdAt DESC",
                DisruptionScheduleEntity.class).getResultList();

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
        return Response.ok(entries).build();
    }

    @POST
    @Path("/schedules")
    @jakarta.transaction.Transactional
    public Response createSchedule(CreateDisruptionScheduleRequest request) {
        if (request.name == null || request.name.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'name' is required")).build();
        }
        if (request.cronExpression == null || request.cronExpression.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Field 'cronExpression' is required")).build();
        }
        if ((request.playbookName == null || request.playbookName.isBlank()) && request.plan == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Either 'playbookName' or 'plan' is required")).build();
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
                        .entity(ApiError.of(400, "Bad Request", "Invalid plan: " + e.getMessage())).build();
            }
        }

        em.persist(entity);
        return Response.status(201).entity(entity).build();
    }

    @PUT
    @Path("/schedules/{id}")
    @jakarta.transaction.Transactional
    public Response updateSchedule(@PathParam("id") String id, CreateDisruptionScheduleRequest request) {
        DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id)).build();
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
                        .entity(ApiError.of(400, "Bad Request", "Invalid plan")).build();
            }
        }
        em.merge(entity);
        return Response.ok(entity).build();
    }

    @DELETE
    @Path("/schedules/{id}")
    @jakarta.transaction.Transactional
    public Response deleteSchedule(@PathParam("id") String id) {
        DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id)).build();
        }
        em.remove(entity);
        return Response.noContent().build();
    }

    public static class CreateDisruptionScheduleRequest {
        public String name;
        public String cronExpression;
        public boolean enabled = true;
        public String playbookName;
        public DisruptionPlan plan;
    }
}
