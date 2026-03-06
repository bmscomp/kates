package com.bmscomp.kates.resource;

import java.util.List;

import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.bmscomp.kates.service.ShareGroupConsumerService;

import io.smallrye.common.annotation.Blocking;

/**
 * REST endpoint for managing the Kafka 4.2 Share Groups consumer.
 * Share Groups (KIP-932) provide work-queue semantics for kates-results processing.
 */
@Path("/api/share-groups")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
public class ShareGroupResource {

    @Inject
    ShareGroupConsumerService shareGroupService;

    @POST
    @Path("/start")
    public Response start() {
        boolean started = shareGroupService.start();
        if (started) {
            return Response.ok(new ActionResult("started", "Share Group consumer started")).build();
        }
        return Response.status(Response.Status.CONFLICT)
                .entity(new ActionResult("already_running", "Share Group consumer is already running"))
                .build();
    }

    @POST
    @Path("/stop")
    public Response stop() {
        boolean stopped = shareGroupService.stop();
        if (stopped) {
            return Response.ok(new ActionResult("stopped", "Share Group consumer stopped")).build();
        }
        return Response.status(Response.Status.CONFLICT)
                .entity(new ActionResult("not_running", "Share Group consumer is not running"))
                .build();
    }

    @GET
    @Path("/status")
    public ShareGroupStatus getStatus() {
        return new ShareGroupStatus(
                shareGroupService.isRunning(),
                shareGroupService.getProcessedCount(),
                shareGroupService.getFailedCount(),
                shareGroupService.getRecentResults()
        );
    }

    public record ActionResult(String status, String message) {}

    public record ShareGroupStatus(
            boolean running,
            long processedCount,
            long failedCount,
            List<String> recentResults
    ) {}
}
