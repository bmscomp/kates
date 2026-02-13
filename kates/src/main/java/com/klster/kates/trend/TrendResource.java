package com.klster.kates.trend;

import com.klster.kates.api.ApiError;
import com.klster.kates.domain.TestType;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

/**
 * REST endpoint for querying historical performance trends.
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
            @QueryParam("baselineWindow") @DefaultValue("5") int baselineWindow) {

        if (typeStr == null || typeStr.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Query param 'type' is required")).build();
        }

        TestType type;
        try {
            type = TestType.valueOf(typeStr.toUpperCase());
        } catch (IllegalArgumentException e) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Unknown test type: " + typeStr)).build();
        }

        if (days < 1 || days > 365) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "days must be between 1 and 365")).build();
        }

        TrendResponse trend = trendService.computeTrend(type, metric, days, baselineWindow);
        return Response.ok(trend).build();
    }
}
