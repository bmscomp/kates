package com.bmscomp.kates.trend;

import java.util.List;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.domain.TestType;

/**
 * REST endpoint for querying historical performance trends.
 * Supports overall and phase-level metric analysis.
 */
@Path("/api/trends")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Trends")
public class TrendResource {

    @Inject
    TrendService trendService;

    @GET
    @Operation(summary = "Get performance trend", description = "Returns time-series metrics with baseline comparison")
    public Response getTrend(
            @Parameter(description = "Test type (required)", required = true) @QueryParam("type") String typeStr,
            @Parameter(description = "Metric name") @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec")
                    String metric,
            @Parameter(description = "Lookback window in days") @QueryParam("days") @DefaultValue("30") int days,
            @Parameter(description = "Number of runs for baseline average")
                    @QueryParam("baselineWindow")
                    @DefaultValue("5")
                    int baselineWindow,
            @Parameter(description = "Phase name filter") @QueryParam("phase") String phase) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(
                    typeStr == null || typeStr.isBlank()
                            ? "Query param 'type' is required"
                            : "Unknown test type: " + typeStr);
        }

        Response validation = validateDays(days);
        if (validation != null) return validation;

        String resolvedPhase = (phase != null && !phase.isBlank()) ? phase : null;
        TrendResponse trend = trendService.computeTrend(type, metric, days, baselineWindow, resolvedPhase);
        return Response.ok(trend).build();
    }

    @GET
    @Path("/phases")
    @Operation(summary = "Discover test phases", description = "Lists distinct phase names observed in recent runs")
    public Response getPhases(
            @Parameter(description = "Test type (required)", required = true) @QueryParam("type") String typeStr,
            @Parameter(description = "Lookback window in days") @QueryParam("days") @DefaultValue("30") int days) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(
                    typeStr == null || typeStr.isBlank()
                            ? "Query param 'type' is required"
                            : "Unknown test type: " + typeStr);
        }

        Response validation = validateDays(days);
        if (validation != null) return validation;

        List<String> phases = trendService.discoverPhases(type, days);
        return Response.ok(phases).build();
    }

    @GET
    @Path("/breakdown")
    @Operation(summary = "Phase-level trend breakdown", description = "Returns per-phase metric trends")
    public Response getBreakdown(
            @Parameter(description = "Test type (required)", required = true) @QueryParam("type") String typeStr,
            @Parameter(description = "Metric name") @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec")
                    String metric,
            @Parameter(description = "Lookback window in days") @QueryParam("days") @DefaultValue("30") int days,
            @Parameter(description = "Number of runs for baseline average")
                    @QueryParam("baselineWindow")
                    @DefaultValue("5")
                    int baselineWindow) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(
                    typeStr == null || typeStr.isBlank()
                            ? "Query param 'type' is required"
                            : "Unknown test type: " + typeStr);
        }

        Response validation = validateDays(days);
        if (validation != null) return validation;

        PhaseTrendResponse breakdown = trendService.computeBreakdown(type, metric, days, baselineWindow);
        return Response.ok(breakdown).build();
    }

    @GET
    @Path("/broker")
    @Operation(summary = "Broker-level performance trend")
    public Response getBrokerTrend(
            @Parameter(description = "Test type (required)", required = true) @QueryParam("type") String typeStr,
            @Parameter(description = "Metric name") @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec")
                    String metric,
            @Parameter(description = "Target broker ID") @QueryParam("brokerId") @DefaultValue("0") int brokerId,
            @Parameter(description = "Lookback window in days") @QueryParam("days") @DefaultValue("30") int days,
            @Parameter(description = "Number of runs for baseline average")
                    @QueryParam("baselineWindow")
                    @DefaultValue("5")
                    int baselineWindow) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(
                    typeStr == null || typeStr.isBlank()
                            ? "Query param 'type' is required"
                            : "Unknown test type: " + typeStr);
        }

        Response validation = validateDays(days);
        if (validation != null) return validation;

        BrokerTrendResponse trend = trendService.computeBrokerTrend(type, metric, brokerId, days, baselineWindow);
        return Response.ok(trend).build();
    }

    private TestType parseType(String typeStr) {
        if (typeStr == null || typeStr.isBlank()) return null;
        try {
            return TestType.valueOf(typeStr.toUpperCase());
        } catch (IllegalArgumentException e) {
            return null;
        }
    }

    private Response validateDays(int days) {
        if (days < 1 || days > 365) {
            return badRequest("days must be between 1 and 365");
        }
        return null;
    }

    private Response badRequest(String message) {
        return Response.status(400)
                .entity(ApiError.of(400, "Bad Request", message))
                .build();
    }
}
