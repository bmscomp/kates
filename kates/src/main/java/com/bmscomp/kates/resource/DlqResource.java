package com.bmscomp.kates.resource;

import java.time.Instant;
import java.util.Map;
import java.util.stream.Collectors;

import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

import com.bmscomp.kates.service.DeadLetterQueueService;

import io.smallrye.common.annotation.Blocking;

/**
 * REST endpoint for Dead Letter Queue monitoring and inspection.
 */
@Path("/api/dlq")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
public class DlqResource {

    @Inject
    DeadLetterQueueService dlqService;

    @GET
    @Path("/stats")
    public DlqStats getStats() {
        Instant last = dlqService.getLastDlqMessage();
        Map<String, Long> bySource = dlqService.getDlqBySource().entrySet().stream()
                .collect(Collectors.toMap(Map.Entry::getKey, e -> e.getValue().get()));

        return new DlqStats(
                dlqService.getTotalDlqMessages(),
                last != null ? last.toString() : null,
                bySource
        );
    }

    public record DlqStats(
            long totalMessages,
            String lastMessageAt,
            Map<String, Long> messagesBySource
    ) {}
}
