package com.klster.kates.disruption;

import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.inject.Inject;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.sse.OutboundSseEvent;
import jakarta.ws.rs.sse.Sse;
import jakarta.ws.rs.sse.SseEventSink;

import java.util.function.Consumer;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * SSE endpoint for streaming real-time disruption test progress.
 * Clients connect and receive events as disruption steps execute.
 */
@Path("/api/disruptions")
public class DisruptionStreamResource {

    private static final Logger LOG = Logger.getLogger(DisruptionStreamResource.class.getName());

    @Inject
    DisruptionEventBus eventBus;

    @Inject
    ObjectMapper objectMapper;

    @GET
    @Path("/{id}/stream")
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public void stream(
            @PathParam("id") String disruptionId,
            @Context SseEventSink sink,
            @Context Sse sse) {

        LOG.info("SSE client connected for disruption: " + disruptionId);

        Consumer<DisruptionEventBus.DisruptionEvent> listener = event -> {
            if (!event.disruptionId().equals(disruptionId)) return;

            try {
                String json = objectMapper.writeValueAsString(event);
                OutboundSseEvent sseEvent = sse.newEventBuilder()
                        .name(event.type().name())
                        .data(json)
                        .build();
                sink.send(sseEvent);

                if (event.type() == DisruptionEventBus.EventType.COMPLETED
                        || event.type() == DisruptionEventBus.EventType.FAILED) {
                    sink.close();
                }
            } catch (Exception e) {
                LOG.log(Level.WARNING, "Failed to send SSE event", e);
            }
        };

        eventBus.subscribe(listener);

        sink.send(sse.newEventBuilder()
                .name("CONNECTED")
                .data("{\"disruptionId\":\"" + disruptionId + "\",\"status\":\"streaming\"}")
                .build());
    }
}
