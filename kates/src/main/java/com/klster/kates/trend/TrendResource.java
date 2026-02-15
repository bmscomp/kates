package com.klster.kates.trend;

import com.klster.kates.api.ApiError;
import com.klster.kates.domain.TestType;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import java.util.List;

/**
 * REST endpoint for querying historical performance trends.
 * Supports overall and phase-level metric analysis.
 */
@Path("/api/trends")
@Produces(MediaType.APPLICATION_JSON)
public class TrendResource {

    @Inject
    TrendService trendService;

    @GET
    public Response getTrend(
            @QueryParam("type") String typeStr,
            @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec") String metric,
            @QueryParam("days") @DefaultValue("30") int days,
            @QueryParam("baselineWindow") @DefaultValue("5") int baselineWindow,
            @QueryParam("phase") String phase) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(typeStr == null || typeStr.isBlank()
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
    public Response getPhases(
            @QueryParam("type") String typeStr,
            @QueryParam("days") @DefaultValue("30") int days) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(typeStr == null || typeStr.isBlank()
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
    public Response getBreakdown(
            @QueryParam("type") String typeStr,
            @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec") String metric,
            @QueryParam("days") @DefaultValue("30") int days,
            @QueryParam("baselineWindow") @DefaultValue("5") int baselineWindow) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(typeStr == null || typeStr.isBlank()
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
    public Response getBrokerTrend(
            @QueryParam("type") String typeStr,
            @QueryParam("metric") @DefaultValue("avgThroughputRecPerSec") String metric,
            @QueryParam("brokerId") @DefaultValue("0") int brokerId,
            @QueryParam("days") @DefaultValue("30") int days,
            @QueryParam("baselineWindow") @DefaultValue("5") int baselineWindow) {

        TestType type = parseType(typeStr);
        if (type == null) {
            return badRequest(typeStr == null || typeStr.isBlank()
                    ? "Query param 'type' is required"
                    : "Unknown test type: " + typeStr);
        }

        Response validation = validateDays(days);
        if (validation != null) return validation;

        BrokerTrendResponse trend = trendService.computeBrokerTrend(
                type, metric, brokerId, days, baselineWindow);
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
                .entity(ApiError.of(400, "Bad Request", message)).build();
    }
}
