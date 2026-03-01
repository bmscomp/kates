package com.bmscomp.kates.disruption;

import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.report.ReportSummary;

/**
 * Captures Kafka broker metrics from a Prometheus server during disruption windows.
 * Uses PromQL range queries to get before/after metrics for computing real impact deltas.
 */
@ApplicationScoped
public class PrometheusMetricsCapture {

    private static final Logger LOG = Logger.getLogger(PrometheusMetricsCapture.class);

    @ConfigProperty(name = "kates.prometheus.url", defaultValue = "http://prometheus.monitoring.svc:9090")
    String prometheusUrl;

    @Inject
    ObjectMapper objectMapper;

    private final HttpClient httpClient =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();

    private static final Map<String, String> METRIC_QUERIES = Map.of(
            "throughputRecPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_messagesin_total[1m]))",
            "avgLatencyMs",
            "avg(kafka_network_requestmetrics_totaltimems{request=\"Produce\"}) / 1000",
            "p99LatencyMs",
            "histogram_quantile(0.99, sum(rate(kafka_network_requestmetrics_totaltimems_bucket{request=\"Produce\"}[1m])) by (le)) / 1000",
            "underReplicatedPartitions",
            "sum(kafka_server_replicamanager_underreplicatedpartitions)",
            "activeControllerCount",
            "sum(kafka_controller_kafkacontroller_activecontrollercount)",
            "bytesInPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_bytesin_total[1m]))",
            "bytesOutPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_bytesout_total[1m]))",
            "produceRequestsPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_totalproducerequests_total[1m]))",
            "fetchRequestsPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_totalfetchrequests_total[1m]))",
            "isrShrinkPerSec",
            "sum(rate(kafka_server_replicamanager_isrshrinks_total[1m]))");

    public record MetricsSnapshot(Instant capturedAt, Duration window, Map<String, Double> values) {
        public double get(String metric) {
            return values.getOrDefault(metric, 0.0);
        }
    }

    /**
     * Captures a snapshot of Kafka broker metrics over the given window ending now.
     */
    public MetricsSnapshot capture(Duration window) {
        Instant end = Instant.now();

        Map<String, Double> values = new LinkedHashMap<>();

        for (Map.Entry<String, String> entry : METRIC_QUERIES.entrySet()) {
            try {
                double value = queryInstant(entry.getValue(), end);
                values.put(entry.getKey(), value);
            } catch (Exception e) {
                LOG.debug("Failed to query " + entry.getKey(), e);
                values.put(entry.getKey(), 0.0);
            }
        }

        LOG.info("Prometheus snapshot captured: " + values.size() + " metrics over " + window);
        return new MetricsSnapshot(end, window, values);
    }

    /**
     * Computes impact deltas between a baseline and post-disruption snapshot.
     */
    public Map<String, Double> computeDeltas(MetricsSnapshot baseline, MetricsSnapshot impact) {
        Map<String, Double> deltas = new LinkedHashMap<>();

        for (String metric : METRIC_QUERIES.keySet()) {
            double baseVal = baseline.get(metric);
            double impactVal = impact.get(metric);

            if (baseVal > 0) {
                double deltaPercent = ((impactVal - baseVal) / baseVal) * 100.0;
                deltas.put(metric, Math.round(deltaPercent * 100.0) / 100.0);
            } else {
                deltas.put(metric, impactVal);
            }
        }

        return deltas;
    }

    /**
     * Converts a metrics snapshot to a ReportSummary for storage in StepReport.
     */
    public ReportSummary toReportSummary(MetricsSnapshot snapshot, Duration observationDuration) {
        return new ReportSummary(
                0L,
                snapshot.get("throughputRecPerSec"),
                snapshot.get("throughputRecPerSec"),
                snapshot.get("bytesInPerSec") / (1024 * 1024),
                snapshot.get("avgLatencyMs"),
                0.0,
                0.0,
                snapshot.get("p99LatencyMs"),
                0.0,
                0.0,
                0L,
                0.0,
                observationDuration.toMillis());
    }

    /**
     * Checks if Prometheus is reachable.
     */
    public boolean isAvailable() {
        try {
            HttpRequest request = HttpRequest.newBuilder()
                    .uri(URI.create(prometheusUrl + "/-/healthy"))
                    .timeout(Duration.ofSeconds(3))
                    .GET()
                    .build();
            HttpResponse<String> response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());
            return response.statusCode() == 200;
        } catch (Exception e) {
            LOG.debug("Prometheus not available", e);
            return false;
        }
    }

    private double queryInstant(String promql, Instant time) throws Exception {
        String encoded = URLEncoder.encode(promql, StandardCharsets.UTF_8);
        String url = prometheusUrl + "/api/v1/query?query=" + encoded + "&time=" + time.getEpochSecond();

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .timeout(Duration.ofSeconds(10))
                .GET()
                .build();

        HttpResponse<String> response = httpClient.send(request, HttpResponse.BodyHandlers.ofString());

        if (response.statusCode() != 200) {
            throw new RuntimeException("Prometheus returned " + response.statusCode());
        }

        JsonNode root = objectMapper.readTree(response.body());
        JsonNode results = root.path("data").path("result");

        if (results.isArray() && !results.isEmpty()) {
            JsonNode value = results.get(0).path("value");
            if (value.isArray() && value.size() >= 2) {
                String strVal = value.get(1).asText();
                if ("NaN".equals(strVal) || "+Inf".equals(strVal) || "-Inf".equals(strVal)) {
                    return 0.0;
                }
                return Double.parseDouble(strVal);
            }
        }

        return 0.0;
    }
}
