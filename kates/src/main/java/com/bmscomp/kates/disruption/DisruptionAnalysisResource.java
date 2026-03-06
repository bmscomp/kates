package com.bmscomp.kates.disruption;

import java.util.List;
import java.util.Map;

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

import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;

@Path("/api/disruptions")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Disruptions")
public class DisruptionAnalysisResource {

    @Inject
    DisruptionImpactScorer impactScorer;

    @Inject
    com.bmscomp.kates.chaos.CompoundChaosOrchestrator compoundOrchestrator;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @GET
    @Path("/{id}/impact")
    @Operation(
            summary = "Get disruption impact score",
            description = "Computes a composite 0-100 severity score across 5 dimensions")
    @APIResponse(responseCode = "200", description = "Impact score with breakdown")
    @APIResponse(responseCode = "404", description = "Report not found")
    public Response getImpactScore(@Parameter(description = "Report ID") @PathParam("id") String id) {
        DisruptionReport report = DisruptionPersistence.loadReport(id, repository, objectMapper);
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
    public Response executeCompound(DisruptionDtos.CompoundChaosRequest request) {
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
}
