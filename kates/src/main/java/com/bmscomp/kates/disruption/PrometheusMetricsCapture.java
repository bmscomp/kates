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

    @ConfigProperty(
            name = "kates.prometheus.url",
            defaultValue = "http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090")
    String prometheusUrl;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @ConfigProperty(name = "kates.chaos.kafka.cluster", defaultValue = "krafter")
    String kafkaCluster;

    @Inject
    ObjectMapper objectMapper;

    private final HttpClient httpClient =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();

    public static final String THROUGHPUT = "throughputRecPerSec";
    public static final String P99_LATENCY = "p99LatencyMs";

    /**
     * PromQL for each captured metric. {@code %s} stands for the matchers that
     * scope it to this cluster ({@link #clusterMatchers()}); without them a
     * Prometheus that scrapes several Kafka clusters summed all of them.
     *
     * <p>The names are the series the Strimzi JMX exporter rules produce
     * ({@code charts/kafka-cluster/files/metrics/kafka-metrics.yaml}):
     * <ul>
     *   <li>A BrokerTopicMetrics meter has a broker-wide series and one per
     *       topic. {@code topic=""} keeps the broker-wide one; summing both
     *       counted every message twice.
     *   <li>A request-time histogram is one gauge per quantile, in
     *       milliseconds, plus a count: no {@code _bucket}, sum or mean. P99
     *       reads the 0.99 gauge of the slowest broker. The old
     *       {@code histogram_quantile} over {@code _bucket} found no series,
     *       and there is no average produce latency to read at all.
     * </ul>
     */
    private static final Map<String, String> METRIC_QUERIES = Map.of(
            THROUGHPUT,
            "sum(rate(kafka_server_brokertopicmetrics_messagesin_total{topic=\"\",%s}[1m]))",
            P99_LATENCY,
            "max(kafka_network_requestmetrics_totaltimems{request=\"Produce\",quantile=\"0.99\",%s})",
            "underReplicatedPartitions",
            "sum(kafka_server_replicamanager_underreplicatedpartitions{%s})",
            "activeControllerCount",
            "sum(kafka_controller_kafkacontroller_activecontrollercount{%s})",
            "bytesInPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_bytesin_total{topic=\"\",%s}[1m]))",
            "bytesOutPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_bytesout_total{topic=\"\",%s}[1m]))",
            "produceRequestsPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_totalproducerequests_total{topic=\"\",%s}[1m]))",
            "fetchRequestsPerSec",
            "sum(rate(kafka_server_brokertopicmetrics_totalfetchrequests_total{topic=\"\",%s}[1m]))",
            "isrShrinkPerSec",
            "sum(rate(kafka_server_replicamanager_isrshrinks_total{%s}[1m]))");

    /**
     * The captured values. A metric Prometheus returned nothing for is absent,
     * not 0: a 0 passed every maximum and failed every minimum.
     */
    public record MetricsSnapshot(Instant capturedAt, Duration window, Map<String, Double> values) {
        public double get(String metric) {
            return values.getOrDefault(metric, 0.0);
        }

        public boolean has(String metric) {
            return values.containsKey(metric);
        }
    }

    /**
     * The labels the chart's PodMonitors attach to every Kafka series (the
     * relabelings Strimzi's dashboards expect), for this cluster.
     */
    String clusterMatchers() {
        return "namespace=\"" + kafkaNamespace + "\",strimzi_io_cluster=\"" + kafkaCluster + "\"";
    }

    /** The query for one metric, scoped to this cluster. */
    String query(String metric) {
        return String.format(METRIC_QUERIES.get(metric), clusterMatchers());
    }

    /**
     * Captures a snapshot of Kafka broker metrics over the given window ending now.
     */
    public MetricsSnapshot capture(Duration window) {
        Instant end = Instant.now();

        Map<String, Double> values = new LinkedHashMap<>();

        for (String metric : METRIC_QUERIES.keySet()) {
            try {
                queryInstant(query(metric), end).ifPresent(v -> values.put(metric, v));
            } catch (Exception e) {
                LOG.debug("Failed to query " + metric, e);
            }
        }

        MetricsSnapshot snapshot = new MetricsSnapshot(end, window, values);
        LOG.info("Prometheus snapshot captured: " + values.size() + " metrics over " + window);
        List<String> missing = unmeasured(snapshot);
        if (!missing.isEmpty()) {
            LOG.warn("Prometheus returned no data for " + missing + " (scoped to " + clusterMatchers() + ")");
        }
        return snapshot;
    }

    /** The captured metrics Prometheus returned nothing for, sorted. */
    public List<String> unmeasured(MetricsSnapshot snapshot) {
        return METRIC_QUERIES.keySet().stream()
                .filter(m -> !snapshot.has(m))
                .sorted()
                .toList();
    }

    /**
     * Computes impact deltas between a baseline and post-disruption snapshot.
     */
    public Map<String, Double> computeDeltas(MetricsSnapshot baseline, MetricsSnapshot impact) {
        Map<String, Double> deltas = new LinkedHashMap<>();

        for (String metric : METRIC_QUERIES.keySet()) {
            if (!baseline.has(metric) || !impact.has(metric)) {
                continue;
            }
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
     * Fields the capture has no value for hold 0; {@link #unmeasured} names
     * the captured ones that came back empty.
     */
    public ReportSummary toReportSummary(MetricsSnapshot snapshot, Duration observationDuration) {
        return new ReportSummary(
                0L,
                snapshot.get(THROUGHPUT),
                snapshot.get(THROUGHPUT),
                snapshot.get("bytesInPerSec") / (1024 * 1024),
                0.0,
                0.0,
                0.0,
                snapshot.get(P99_LATENCY),
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

    /** The query's first sample, or empty when it returned none or a non-finite one. */
    private OptionalDouble queryInstant(String promql, Instant time) throws Exception {
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
                    return OptionalDouble.empty();
                }
                return OptionalDouble.of(Double.parseDouble(strVal));
            }
        }

        return OptionalDouble.empty();
    }
}
