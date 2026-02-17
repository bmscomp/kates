package com.klster.kates.disruption;

import jakarta.enterprise.context.ApplicationScoped;

import java.time.Instant;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.Consumer;
import org.jboss.logging.Logger;

/**
 * In-memory event bus for broadcasting disruption progress events.
 * Supports SSE subscribers that receive real-time updates during disruption execution.
 */
@ApplicationScoped
public class DisruptionEventBus {

    private static final Logger LOG = Logger.getLogger(DisruptionEventBus.class);

    private final List<Consumer<DisruptionEvent>> subscribers = new CopyOnWriteArrayList<>();

    public void subscribe(Consumer<DisruptionEvent> listener) {
        subscribers.add(listener);
    }

    public void unsubscribe(Consumer<DisruptionEvent> listener) {
        subscribers.remove(listener);
    }

    public void emit(DisruptionEvent event) {
        LOG.debugf("Event: %s — %s", event.type(), event.message());
        for (Consumer<DisruptionEvent> sub : subscribers) {
            try {
                sub.accept(event);
            } catch (Exception e) {
                LOG.warn("Subscriber error: " + e.getMessage());
            }
        }
    }

    public void emit(String disruptionId, EventType type, String stepName, String message) {
        emit(new DisruptionEvent(disruptionId, type, stepName, message, Instant.now().toString(), null));
    }

    public void emit(String disruptionId, EventType type, String stepName, String message, Object data) {
        emit(new DisruptionEvent(disruptionId, type, stepName, message, Instant.now().toString(), data));
    }

    public enum EventType {
        STARTED,
        STEP_STARTED,
        METRICS_BASELINE,
        FAULT_INJECTED,
        RECOVERY_WAITING,
        METRICS_CAPTURED,
        STEP_COMPLETED,
        ROLLBACK,
        SLA_GRADED,
        COMPLETED,
        FAILED
    }

    public record DisruptionEvent(
            String disruptionId,
            EventType type,
            String stepName,
            String message,
            String timestamp,
            Object data
    ) {}
}
