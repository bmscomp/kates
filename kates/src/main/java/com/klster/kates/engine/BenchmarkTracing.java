package com.klster.kates.engine;

import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanKind;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.context.Scope;
import jakarta.enterprise.context.ApplicationScoped;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.Supplier;

/**
 * Creates OpenTelemetry spans for benchmark execution lifecycle.
 * REST endpoints are auto-instrumented by Quarkus; this handles
 * the internal orchestration spans (run, phase, topic creation).
 *
 * Gracefully degrades to no-op when OTel is disabled.
 */
@ApplicationScoped
public class BenchmarkTracing {

    private final Tracer tracer;
    private final boolean enabled;
    private final Map<String, Span> activeRunSpans = new ConcurrentHashMap<>();

    public BenchmarkTracing(
            @ConfigProperty(name = "quarkus.otel.enabled", defaultValue = "false") boolean otelEnabled) {
        this.enabled = otelEnabled;
        this.tracer = otelEnabled
                ? GlobalOpenTelemetry.getTracer("kates", "1.0.0")
                : null;
    }

    public void startRunSpan(String runId, String testType, String backend) {
        if (!enabled) return;
        Span span = tracer.spanBuilder("kates.benchmark.run")
                .setSpanKind(SpanKind.INTERNAL)
                .setAttribute("kates.run_id", runId)
                .setAttribute("kates.test_type", testType)
                .setAttribute("kates.backend", backend)
                .startSpan();
        activeRunSpans.put(runId, span);
    }

    public void endRunSpan(String runId, boolean success) {
        if (!enabled) return;
        Span span = activeRunSpans.remove(runId);
        if (span != null) {
            span.setStatus(success ? StatusCode.OK : StatusCode.ERROR);
            span.end();
        }
    }

    public <T> T tracePhase(String runId, String phaseName, String phaseType, Supplier<T> work) {
        if (!enabled) return work.get();

        Span parentSpan = activeRunSpans.get(runId);
        var builder = tracer.spanBuilder("kates.benchmark.phase")
                .setSpanKind(SpanKind.INTERNAL)
                .setAttribute("kates.run_id", runId)
                .setAttribute("kates.phase_name", phaseName)
                .setAttribute("kates.phase_type", phaseType);

        if (parentSpan != null) {
            builder.setParent(io.opentelemetry.context.Context.current().with(parentSpan));
        }

        Span phaseSpan = builder.startSpan();
        try (Scope ignored = phaseSpan.makeCurrent()) {
            return work.get();
        } catch (Exception e) {
            phaseSpan.setStatus(StatusCode.ERROR, e.getMessage());
            phaseSpan.recordException(e);
            throw e;
        } finally {
            phaseSpan.end();
        }
    }

    public void traceTopicCreation(String runId, String topicName) {
        if (!enabled) return;
        Span span = tracer.spanBuilder("kates.benchmark.topic.create")
                .setSpanKind(SpanKind.INTERNAL)
                .setAttribute("kates.run_id", runId)
                .setAttribute("kates.topic", topicName)
                .startSpan();
        span.end();
    }
}
