package com.bmscomp.kates.config;

import java.time.Duration;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;

import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.config.MeterFilter;
import io.micrometer.core.instrument.distribution.DistributionStatisticConfig;

/**
 * Buckets on the HTTP server timer, so {@code http_server_requests_seconds_bucket}
 * exists and the boards' latency percentiles can be drawn.
 *
 * <p>Quarkus publishes the timer as {@code _count}, {@code _sum} and
 * {@code _max} only; {@code histogram_quantile()} over a series that does not
 * exist is an empty panel that looks exactly like an idle service, which is
 * what the kates-application and kates-overview boards drew. Fixed SLO
 * boundaries rather than Micrometer's percentile histogram: the latter is
 * about seventy buckets per {@code uri × method × status × outcome}, and the
 * percentiles the boards ask for (p50, p95, p99) interpolate well enough
 * inside eleven.
 *
 * <p>The boundaries are also the resolution: {@code histogram_quantile()}
 * interpolates inside the bucket a quantile falls in, so a p99 of 7 ms means
 * "between 5 and 10". Changing them changes what every existing panel reads,
 * and {@code dashboards/METRICS.md} lists them — keep the two in step.
 */
public class HttpLatencyHistogram {

    static final Duration[] BOUNDARIES = {
        Duration.ofMillis(5), Duration.ofMillis(10), Duration.ofMillis(25),
        Duration.ofMillis(50), Duration.ofMillis(100), Duration.ofMillis(250),
        Duration.ofMillis(500), Duration.ofSeconds(1), Duration.ofMillis(2500),
        Duration.ofSeconds(5), Duration.ofSeconds(10),
    };

    @Produces
    @Singleton
    public MeterFilter httpServerRequestsBuckets() {
        return new MeterFilter() {
            @Override
            public DistributionStatisticConfig configure(Meter.Id id, DistributionStatisticConfig config) {
                if (!"http.server.requests".equals(id.getName())) {
                    return config;
                }
                return DistributionStatisticConfig.builder()
                        .serviceLevelObjectives(nanos())
                        .build()
                        .merge(config);
            }
        };
    }

    private static double[] nanos() {
        double[] out = new double[BOUNDARIES.length];
        for (int i = 0; i < BOUNDARIES.length; i++) {
            out[i] = BOUNDARIES[i].toNanos();
        }
        return out;
    }
}
