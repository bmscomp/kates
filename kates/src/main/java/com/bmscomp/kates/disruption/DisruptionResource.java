package com.bmscomp.kates.disruption;

import java.util.*;

import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;
import org.jboss.logging.Logger;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.chaos.DisruptionType;

/**
 * Core disruption endpoints: execute, list, get, timeline, kafka-metrics, compare, types.
 *
 * Playbook, schedule, template, and analysis endpoints are in separate sub-resources.
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
        DisruptionPersistence.persistReport(id, report, repository, objectMapper);

        if ("REJECTED".equals(report.getStatus())) {
            return Response.status(422)
                    .entity(Map.of(
                            "id", id,
                            "status", "REJECTED",
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
        DisruptionReport report = DisruptionPersistence.loadReport(id, repository, objectMapper);
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
        DisruptionReport report = DisruptionPersistence.loadReport(id, repository, objectMapper);
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
        DisruptionReport report = DisruptionPersistence.loadReport(id, repository, objectMapper);
        if (report == null) {
            return Response.status(404)
                    .entity(ApiError.of(404, "Not Found", "No disruption report with ID: " + id))
                    .build();
        }

        var metrics = report.getStepReports().stream()
                .map(step -> {
                    Map<String, Object> entry = new LinkedHashMap<>();
                    entry.put("step", step.stepName());
                    entry.put("disruptionType",
                            step.disruptionType() != null ? step.disruptionType().name() : "unknown");

                    if (step.targetedLeaderBrokerId() != null) {
                        entry.put("targetedLeaderBrokerId", step.targetedLeaderBrokerId());
                    }

                    if (step.isrMetrics() != null) {
                        Map<String, Object> isr = new LinkedHashMap<>();
                        isr.put("timeToFullIsr",
                                step.isrMetrics().timeToFullIsr() != null
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
                        lag.put("timeToLagRecovery",
                                step.lagMetrics().timeToLagRecovery() != null
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

        DisruptionReport current = DisruptionPersistence.loadReport(id, repository, objectMapper);
        DisruptionReport baseline = DisruptionPersistence.loadReport(baselineId, repository, objectMapper);

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
                    durationDeltaMs(current.getSummary().worstRecovery(), baseline.getSummary().worstRecovery()));
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
    @Operation(
            summary = "List disruption types",
            description = "Returns all available disruption types with descriptions")
    public Response listTypes() {
        var types = Arrays.stream(DisruptionType.values())
                .map(t -> Map.of("name", t.name(), "description", describeType(t)))
                .toList();
        return Response.ok(types).build();
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
}
