package com.bmscomp.kates.api;

import java.util.concurrent.ConcurrentLinkedQueue;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.ObservesAsync;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.sse.OutboundSseEvent;
import jakarta.ws.rs.sse.Sse;
import jakarta.ws.rs.sse.SseEventSink;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;
import org.jboss.logging.Logger;

import com.bmscomp.kates.engine.TestLifecycleEvent;

/**
 * SSE endpoint for real-time test lifecycle events.
 * Clients connect via GET /api/events/stream and receive
 * JSON events as tests are created, started, completed, or failed.
 */
@Path("/api/events")
@ApplicationScoped
@Tag(name = "Events")
public class EventStreamResource {

    private static final Logger LOG = Logger.getLogger(EventStreamResource.class);

    private final ConcurrentLinkedQueue<SseSubscription> subscribers = new ConcurrentLinkedQueue<>();

    @Context
    Sse sse;

    record SseSubscription(SseEventSink sink, String filterType, String filterId) {
        boolean matches(TestLifecycleEvent event) {
            if (filterType != null && !filterType.isEmpty() && !filterType.equalsIgnoreCase(event.getTestType())) {
                return false;
            }
            if (filterId != null && !filterId.isEmpty() && !filterId.equals(event.getRunId())) {
                return false;
            }
            return true;
        }
    }

    @GET
    @Path("/stream")
    @Produces(MediaType.SERVER_SENT_EVENTS)
    @Operation(
            summary = "Real-time event stream",
            description = "SSE stream of test lifecycle events. Filter by test type or run ID.")
    public void stream(@Context SseEventSink sink, @QueryParam("type") String type, @QueryParam("id") String id) {

        var sub = new SseSubscription(sink, type, id);
        subscribers.add(sub);

        LOG.infof("SSE subscriber connected (type=%s, id=%s), total=%d", type, id, subscribers.size());

        OutboundSseEvent welcome = sse.newEventBuilder()
                .name("connected")
                .data("{\"message\":\"connected\",\"filters\":{\"type\":\""
                        + (type != null ? type : "") + "\",\"id\":\""
                        + (id != null ? id : "") + "\"}}")
                .build();
        sink.send(welcome);
    }

    /**
     * Observes CDI async events from the orchestrator and broadcasts to all SSE subscribers.
     */
    public void onTestEvent(@ObservesAsync TestLifecycleEvent event) {
        var iter = subscribers.iterator();
        while (iter.hasNext()) {
            var sub = iter.next();
            if (sub.sink().isClosed()) {
                iter.remove();
                continue;
            }
            if (!sub.matches(event)) {
                continue;
            }
            try {
                OutboundSseEvent sseEvent = sse.newEventBuilder()
                        .name(event.getKind().name().toLowerCase())
                        .data(event.toJson())
                        .build();
                sub.sink().send(sseEvent);
            } catch (Exception e) {
                LOG.debugf("Failed to send SSE event to subscriber: %s", e.getMessage());
                iter.remove();
            }
        }
    }
}
