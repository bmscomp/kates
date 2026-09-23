package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The queries behind a disruption plan's throughput and latency checks, run
 * against a stub Prometheus. They used to read series the Strimzi exporter
 * rules do not produce (P99 from {@code _bucket}), count every message twice
 * (broker-wide plus per-topic series), span every cluster the Prometheus
 * scrapes, and turn an empty result into 0.
 */
class PrometheusMetricsCaptureTest {

    private HttpServer server;
    private final List<String> queries = new CopyOnWriteArrayList<>();
    private PrometheusMetricsCapture capture;

    private static String vector(String value) {
        return "{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":"
                + (value == null ? "[]" : "[{\"metric\":{},\"value\":[1758628800," + "\"" + value + "\"]}]")
                + "}}";
    }

    @BeforeEach
    void startPrometheus() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/api/v1/query", exchange -> {
            String raw = exchange.getRequestURI().getRawQuery();
            String query = URLDecoder.decode(
                    raw.substring(raw.indexOf("query=") + 6, raw.indexOf("&time=")), StandardCharsets.UTF_8);
            queries.add(query);
            // No P99 series; everything else has a value.
            String body = query.contains("totaltimems") ? vector(null) : vector("1200.5");
            byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(200, bytes.length);
            try (OutputStream out = exchange.getResponseBody()) {
                out.write(bytes);
            }
        });
        server.start();

        capture = new PrometheusMetricsCapture();
        capture.prometheusUrl = "http://127.0.0.1:" + server.getAddress().getPort();
        capture.kafkaNamespace = "kafka";
        capture.kafkaCluster = "krafter";
        capture.objectMapper = new ObjectMapper();
    }

    @AfterEach
    void stopPrometheus() {
        server.stop(0);
    }

    @Test
    void anEmptyResultIsUnmeasuredNotZero() {
        PrometheusMetricsCapture.MetricsSnapshot snapshot = capture.capture(Duration.ofSeconds(60));

        assertFalse(snapshot.has(PrometheusMetricsCapture.P99_LATENCY));
        assertEquals(List.of(PrometheusMetricsCapture.P99_LATENCY), capture.unmeasured(snapshot));
        assertEquals(1200.5, snapshot.get(PrometheusMetricsCapture.THROUGHPUT));
    }

    @Test
    void everyQueryIsScopedToTheCluster() {
        capture.capture(Duration.ofSeconds(60));

        assertEquals(9, queries.size());
        for (String q : queries) {
            assertTrue(q.contains("namespace=\"kafka\",strimzi_io_cluster=\"krafter\""), q);
        }
    }

    @Test
    void brokerTopicMetricsReadOnlyTheBrokerWideSeries() {
        capture.capture(Duration.ofSeconds(60));

        List<String> brokerTopic =
                queries.stream().filter(q -> q.contains("brokertopicmetrics")).toList();
        assertEquals(5, brokerTopic.size());
        for (String q : brokerTopic) {
            assertTrue(q.contains("topic=\"\""), q);
        }
    }

    @Test
    void p99ReadsTheExportersQuantileGaugeInMilliseconds() {
        assertEquals(
                "max(kafka_network_requestmetrics_totaltimems{request=\"Produce\",quantile=\"0.99\","
                        + "namespace=\"kafka\",strimzi_io_cluster=\"krafter\"})",
                capture.query(PrometheusMetricsCapture.P99_LATENCY));
    }

    @Test
    void deltasSkipAMetricMissingOnEitherSide() {
        var now = java.time.Instant.now();
        var baseline = new PrometheusMetricsCapture.MetricsSnapshot(
                now, Duration.ofSeconds(30), Map.of(PrometheusMetricsCapture.THROUGHPUT, 1000.0));
        var impact = new PrometheusMetricsCapture.MetricsSnapshot(
                now,
                Duration.ofSeconds(60),
                Map.of(PrometheusMetricsCapture.THROUGHPUT, 800.0, PrometheusMetricsCapture.P99_LATENCY, 40.0));

        assertEquals(Map.of(PrometheusMetricsCapture.THROUGHPUT, -20.0), capture.computeDeltas(baseline, impact));
    }
}
