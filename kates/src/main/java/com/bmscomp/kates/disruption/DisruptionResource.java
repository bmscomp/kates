package com.bmscomp.kates.disruption;

import java.util.*;
import jakarta.annotation.Nullable;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;
import org.jboss.logging.Logger;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.chaos.DisruptionType;

/**
 * REST endpoints for Kubernetes-aware disruption testing
 * with Kafka intelligence, safety guardrails, SLA grading, and persistent storage.
 */
@Path("/api/disruptions")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Disruptions")
public class DisruptionResource {

    private static final Logger LOG = Logger.getLogger(DisruptionResource.class);

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionSafetyGuard safetyGuard;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @POST
    @Operation(
            summary = "Execute a disruption",
            description = "Runs a disruption plan against the Kafka cluster with safety guardrails")
    @APIResponse(responseCode = "200", description = "Disruption report")
    @APIResponse(responseCode = "422", description = "Disruption rejected by safety guard")
    public Response executeDisruption(
            DisruptionPlan plan,
            @Parameter(description = "If true, validates the plan without executing")
                    @QueryParam("dryRun")
                    @DefaultValue("false")
                    boolean dryRun) {

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
                    .entity(Map.of(
                            "id",
                            id,
                            "status",
                            "REJECTED",
                            "validationWarnings",
                            report.getValidationWarnings() != null ? report.getValidationWarnings() : List.of()))
                    .build();
        }

        return Response.ok(Map.of("id", id, "report", report)).build();
    }

    @GET
    @Operation(
            summary = "List disruption reports",
            description = "Returns recent disruption reports, optionally filtered by plan name")
    public Response listReports(
            @Parameter(description = "Filter by plan name") @QueryParam("planName") String planName,
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {

        int effectiveSize = Math.min(Math.max(size, 1), 200);
        List<DisruptionReportEntity> entities;
        if (planName != null && !planName.isBlank()) {
            entities = repository.findByPlanName(planName);
        } else {
            entities = repository.listRecent(effectiveSize * (page + 1));
        }

        int start = page * effectiveSize;
        var paged = entities.stream().skip(start).limit(effectiveSize).toList();

        var summaries = paged.stream()
                .map(e -> Map.of(
                        "id", e.getId(),
                        "planName", e.getPlanName(),
                        "status", e.getStatus(),
                        "slaGrade", e.getSlaGrade() != null ? e.getSlaGrade() : "-",
                        "createdAt", e.getCreatedAt().toString()))
                .toList();

        return Response.ok(Map.of("page", page, "size", effectiveSize, "count", summaries.size(), "items", summaries))
                .build();
    }

    @GET
    @Path("/{id}")
    @Operation(summary = "Get a disruption report")
    @APIResponse(responseCode = "200", description = "Disruption report")
    @APIResponse(responseCode = "404", description = "Report not found")
    public Response getReport(@Parameter(description = "Report ID") @PathParam("id") String id) {
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
    @Operation(
            summary = "Get disruption timeline",
            description = "Returns pod-level events and recovery times per step")
    @APIResponse(responseCode = "200", description = "Timeline events")
    @APIResponse(responseCode = "404", description = "Report not found")
    public Response getTimeline(@Parameter(description = "Report ID") @PathParam("id") String id) {
        DisruptionReport report = loadReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }

        var timelines = report.getStepReports().stream()
                .map(step -> Map.of(
                        "step", step.stepName(),
                        "type",
                                step.disruptionType() != null
                                        ? step.disruptionType().name()
                                        : "unknown",
                        "events", step.podTimeline(),
                        "timeToFirstReady",
                                step.timeToFirstReady() != null
                                        ? step.timeToFirstReady().toMillis() + "ms"
                                        : "N/A",
                        "timeToAllReady",
                                step.timeToAllReady() != null
                                        ? step.timeToAllReady().toMillis() + "ms"
                                        : "N/A"))
                .toList();

        return Response.ok(timelines).build();
    }

    @GET
    @Path("/{id}/kafka-metrics")
    @Operation(
            summary = "Get Kafka metrics for a disruption",
            description = "Returns ISR, lag, and broker metrics captured during disruption")
    @APIResponse(responseCode = "200", description = "Kafka metrics per step")
    @APIResponse(responseCode = "404", description = "Report not found")
    public Response getKafkaMetrics(@Parameter(description = "Report ID") @PathParam("id") String id) {
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
                    entry.put(
                            "disruptionType",
                            step.disruptionType() != null
                                    ? step.disruptionType().name()
                                    : "unknown");

                    if (step.targetedLeaderBrokerId() != null) {
                        entry.put("targetedLeaderBrokerId", step.targetedLeaderBrokerId());
                    }

                    if (step.isrMetrics() != null) {
                        Map<String, Object> isr = new LinkedHashMap<>();
                        isr.put(
                                "timeToFullIsr",
                                step.isrMetrics().timeToFullIsr() != null
                                        ? step.isrMetrics().timeToFullIsr().toMillis() + "ms"
                                        : "N/A");
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
                        lag.put(
                                "timeToLagRecovery",
                                step.lagMetrics().timeToLagRecovery() != null
                                        ? step.lagMetrics().timeToLagRecovery().toMillis() + "ms"
                                        : "N/A");
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
    @Operation(
            summary = "Compare disruption reports",
            description = "Compares current report against a baseline for regression detection")
    @APIResponse(responseCode = "200", description = "Comparison results")
    @APIResponse(responseCode = "404", description = "One or both reports not found")
    public Response compareReports(
            @Parameter(description = "Current report ID") @PathParam("id") String id,
            @Parameter(description = "Baseline report ID for comparison", required = true) @QueryParam("baselineId")
                    String baselineId) {

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

            deltas.put(
                    "recoveryDeltaMs",
                    durationDeltaMs(
                            current.getSummary().worstRecovery(),
                            baseline.getSummary().worstRecovery()));
            deltas.put(
                    "throughputDeltaPercent",
                    current.getSummary().avgThroughputDegradation()
                            - baseline.getSummary().avgThroughputDegradation());
            deltas.put(
                    "p99DeltaPercent",
                    current.getSummary().maxP99LatencySpike()
                            - baseline.getSummary().maxP99LatencySpike());

            comparison.put("deltas", deltas);

            comparison.put(
                    "current",
                    Map.of(
                            "status",
                            current.getStatus(),
                            "slaGrade",
                            current.getSlaVerdict() != null
                                    ? current.getSlaVerdict().grade()
                                    : "-",
                            "passedSteps",
                            current.getSummary().passedSteps() + "/"
                                    + current.getSummary().totalSteps()));
            comparison.put(
                    "baseline",
                    Map.of(
                            "status",
                            baseline.getStatus(),
                            "slaGrade",
                            baseline.getSlaVerdict() != null
                                    ? baseline.getSlaVerdict().grade()
                                    : "-",
                            "passedSteps",
                            baseline.getSummary().passedSteps() + "/"
                                    + baseline.getSummary().totalSteps()));
        }

        return Response.ok(comparison).build();
    }

    @GET
    @Path("/types")
    @Operation(
            summary = "List disruption types",
            description = "Returns all available disruption types with descriptions")
    public Response listTypes() {
        var types = Arrays.stream(DisruptionType.values())
                .map(t -> Map.of("name", t.name(), "description", describeType(t)))
                .toList();
        return Response.ok(types).build();
    }

    private void persistReport(String id, DisruptionReport report) {
        try {
            String grade =
                    report.getSlaVerdict() != null ? report.getSlaVerdict().grade() : null;
            String reportJson = objectMapper.writeValueAsString(report);
            String summaryJson =
                    report.getSummary() != null ? objectMapper.writeValueAsString(report.getSummary()) : null;

            DisruptionReportEntity entity = new DisruptionReportEntity(
                    id, report.getPlanName(), report.getStatus(), grade, reportJson, summaryJson);
            repository.save(entity);
            LOG.info("Persisted disruption report: " + id);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize report for persistence", e);
        }
    }

    private DisruptionReport loadReport(String id) {
        DisruptionReportEntity entity = repository.findById(id);
        if (entity == null) return null;

        try {
            return objectMapper.readValue(entity.getReportJson(), DisruptionReport.class);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to deserialize report: " + id, e);
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
            case MEMORY_STRESS -> "Consume memory on broker container to simulate OOM pressure";
            case IO_STRESS -> "Inject disk I/O pressure on broker storage";
            case DNS_ERROR -> "Inject DNS resolution failures on broker pods";
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
    @Path("/playbooks/{name}")
    @Operation(summary = "Run a playbook", description = "Executes a pre-defined disruption playbook by name")
    @APIResponse(responseCode = "200", description = "Disruption report from playbook execution")
    @APIResponse(responseCode = "404", description = "Playbook not found")
    public Response runPlaybook(@Parameter(description = "Playbook name") @PathParam("name") String name) {
        return playbookCatalog
                .findByName(name)
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
    @Path("/schedules")
    @jakarta.transaction.Transactional
    @Operation(
            summary = "Create a disruption schedule",
            description = "Creates a recurring disruption schedule from a playbook or plan")
    @APIResponse(responseCode = "201", description = "Schedule created")
    public Response createSchedule(CreateDisruptionScheduleRequest request) {
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
    @Path("/schedules/{id}")
    @jakarta.transaction.Transactional
    @Operation(summary = "Update a disruption schedule")
    @APIResponse(responseCode = "200", description = "Schedule updated")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response updateSchedule(
            @Parameter(description = "Schedule ID") @PathParam("id") String id,
            CreateDisruptionScheduleRequest request) {
        @Nullable DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
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
    @Path("/schedules/{id}")
    @jakarta.transaction.Transactional
    @Operation(summary = "Delete a disruption schedule")
    @APIResponse(responseCode = "204", description = "Schedule deleted")
    @APIResponse(responseCode = "404", description = "Schedule not found")
    public Response deleteSchedule(@Parameter(description = "Schedule ID") @PathParam("id") String id) {
        @Nullable DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, id);
        if (entity == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "Schedule not found: " + id))
                    .build();
        }
        em.remove(entity);
        return Response.noContent().build();
    }

    @Inject
    ChaosTemplateCatalog templateCatalog;

    @Inject
    DisruptionImpactScorer impactScorer;

    @Inject
    com.bmscomp.kates.chaos.CompoundChaosOrchestrator compoundOrchestrator;

    @GET
    @Path("/templates")
    @Operation(
            summary = "List chaos experiment templates",
            description = "Returns pre-built chaos templates with sensible defaults for common Kafka failure scenarios")
    public Response listTemplates() {
        return Response.ok(templateCatalog.listTemplates()).build();
    }

    @POST
    @Path("/templates/{id}")
    @Operation(summary = "Run a template", description = "Executes a chaos template with optional override parameters")
    @APIResponse(responseCode = "200", description = "Disruption report from template execution")
    @APIResponse(responseCode = "404", description = "Template not found")
    public Response runTemplate(
            @Parameter(description = "Template ID") @PathParam("id") String templateId, Map<String, Object> overrides) {

        try {
            DisruptionPlan plan = templateCatalog.buildPlan(templateId, overrides != null ? overrides : Map.of());
            String id = UUID.randomUUID().toString().substring(0, 8);
            DisruptionReport report = orchestrator.execute(plan);
            persistReport(id, report);
            return Response.ok(Map.of("id", id, "template", templateId, "report", report))
                    .build();
        } catch (IllegalArgumentException e) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/{id}/impact")
    @Operation(
            summary = "Get disruption impact score",
            description = "Computes a composite 0-100 severity score across 5 dimensions")
    @APIResponse(responseCode = "200", description = "Impact score with breakdown")
    @APIResponse(responseCode = "404", description = "Report not found")
    public Response getImpactScore(@Parameter(description = "Report ID") @PathParam("id") String id) {
        DisruptionReport report = loadReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }

        DisruptionImpactScorer.ImpactScore score = impactScorer.score(report);
        return Response.ok(impactScorer.toMap(score)).build();
    }

    @GET
    @Path("/history")
    @Operation(
            summary = "Disruption history by topic",
            description = "Returns all disruption reports that targeted a specific topic")
    public Response historyByTopic(
            @Parameter(description = "Topic name", required = true) @QueryParam("topic") String topic,
            @Parameter(description = "Max results") @QueryParam("limit") @DefaultValue("20") int limit) {

        if (topic == null || topic.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Query parameter 'topic' is required"))
                    .build();
        }

        List<DisruptionReportEntity> recent = repository.listRecent(100);
        var matching = recent.stream()
                .filter(e -> {
                    try {
                        DisruptionReport r = objectMapper.readValue(e.getReportJson(), DisruptionReport.class);
                        return r.getStepReports().stream()
                                .anyMatch(step -> step.chaosOutcome() != null
                                        && step.chaosOutcome().experimentName() != null
                                        && step.chaosOutcome().experimentName().contains(topic));
                    } catch (Exception ignored) {
                        String rj = e.getReportJson();
                        return rj != null && rj.contains(topic);
                    }
                })
                .limit(limit)
                .map(e -> Map.of(
                        "id", e.getId(),
                        "planName", e.getPlanName(),
                        "status", e.getStatus(),
                        "slaGrade", e.getSlaGrade() != null ? e.getSlaGrade() : "-",
                        "createdAt", e.getCreatedAt().toString()))
                .toList();

        return Response.ok(Map.of("topic", topic, "count", matching.size(), "reports", matching))
                .build();
    }

    @POST
    @Path("/compound")
    @Operation(
            summary = "Execute compound chaos",
            description = "Runs multiple faults concurrently across different chaos providers")
    public Response executeCompound(CompoundChaosRequest request) {
        if (request.faults == null || request.faults.isEmpty()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "At least one fault is required"))
                    .build();
        }

        var faults = request.faults.stream()
                .map(f -> new com.bmscomp.kates.chaos.CompoundChaosOrchestrator.CompoundFault(
                        f.faultSpec, f.providerName != null ? f.providerName : "kubernetes"))
                .toList();

        var outcome = request.sequential
                ? compoundOrchestrator.executeSequential(faults, request.delayBetweenSec)
                : compoundOrchestrator.executeConcurrent(faults, request.timeoutSec);

        return Response.ok(Map.of(
                        "allSucceeded", outcome.allSucceeded(),
                        "mode", request.sequential ? "sequential" : "concurrent",
                        "results", outcome.results()))
                .build();
    }

    @GET
    @Path("/providers")
    @Operation(
            summary = "List chaos providers",
            description = "Returns all registered chaos providers and their availability")
    public Response listProviders() {
        return Response.ok(compoundOrchestrator.availableProviders()).build();
    }

    public static class CompoundChaosRequest {
        public List<CompoundFaultEntry> faults;
        public boolean sequential = false;
        public int timeoutSec = 120;
        public int delayBetweenSec = 5;
    }

    public static class CompoundFaultEntry {
        public com.bmscomp.kates.chaos.FaultSpec faultSpec;
        public String providerName;
    }

    public static class CreateDisruptionScheduleRequest {
        public String name;
        public String cronExpression;
        public boolean enabled = true;
        public String playbookName;
        public DisruptionPlan plan;
    }
}
