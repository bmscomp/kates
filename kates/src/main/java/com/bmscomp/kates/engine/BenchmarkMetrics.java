package com.bmscomp.kates.engine;

import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.DoubleAccumulator;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.FunctionCounter;
import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.Meter;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Tags;

import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult.TaskStatus;

/**
 * Bridges internal benchmark metrics to Micrometer for Prometheus export.
 * Each active run registers its own set of labeled meters.
 *
 * <p><b>Cardinality contract.</b> These meters are tagged with {@code run_id},
 * which is unbounded over time, so every meter registered here MUST be removed
 * when the run ends. {@link #endRun(String)} unregisters them; previously it
 * dropped only the map entry and left the meters in the registry forever, so
 * each run permanently added time series and Prometheus memory grew without
 * bound. The number of run_ids alive at once is bounded by the engine's
 * concurrency cap.
 *
 * <p><b>Published names.</b> Micrometer's Prometheus naming convention turns
 * the dots into underscores and appends {@code _total} to a counter whose name
 * does not already end in it, so the meters below publish as:
 *
 * <pre>
 *   kates.benchmark.active.runs        -&gt; kates_benchmark_active_runs
 *   kates.benchmark.throughput.rec.sec -&gt; kates_benchmark_throughput_rec_sec
 *   kates.benchmark.throughput.mb.sec  -&gt; kates_benchmark_throughput_mb_sec
 *   kates.benchmark.records.total      -&gt; kates_benchmark_records_total
 *   kates.benchmark.errors.total       -&gt; kates_benchmark_errors_total
 *   kates.benchmark.latency.ms         -&gt; kates_benchmark_latency_ms{quantile=…}
 *   kates.benchmark.latency.ms.max     -&gt; kates_benchmark_latency_ms_max
 *   kates.benchmark.sla.violations     -&gt; kates_benchmark_sla_violations
 *
 *   kates.integrity.result.producer.rto.seconds
 *                                      -&gt; kates_integrity_result_producer_rto_seconds
 *   kates.integrity.result.consumer.rto.seconds
 *                                      -&gt; kates_integrity_result_consumer_rto_seconds
 *   kates.integrity.result.max.rto.seconds
 *                                      -&gt; kates_integrity_result_max_rto_seconds
 *   kates.integrity.result.rpo.seconds -&gt; kates_integrity_result_rpo_seconds
 *   kates.integrity.result.data.loss.percent
 *                                      -&gt; kates_integrity_result_data_loss_percent
 *   kates.integrity.result.lost.records
 *                                      -&gt; kates_integrity_result_lost_records
 *   kates.integrity.result.duplicate.records
 *                                      -&gt; kates_integrity_result_duplicate_records
 * </pre>
 *
 * Those are the names {@code dashboards/kates-benchmark}, {@code kates-trend}
 * and {@code kates-chaos} read, and {@code BenchmarkMetricsTest} asserts every
 * one of them against a real {@code PrometheusMeterRegistry} — the boards read
 * the published spelling, not the registered one, and guessing the translation
 * is exactly what left four of these names unregistered for so long.
 */
@ApplicationScoped
public class BenchmarkMetrics {

    private final MeterRegistry registry;
    private final AtomicInteger activeRuns = new AtomicInteger(0);
    private final Map<String, RunMeters> runMeters = new ConcurrentHashMap<>();

    @Inject
    public BenchmarkMetrics(MeterRegistry registry) {
        this.registry = registry;
        Gauge.builder("kates.benchmark.active.runs", activeRuns, AtomicInteger::get)
                .description("Number of active benchmark runs")
                .register(registry);
    }

    /**
     * Registers a run's meters, replacing any the same run id already had.
     *
     * <p><b>Unregister-then-register, atomically.</b> Micrometer identifies a
     * meter by name plus tags, and every meter here is tagged with the run id —
     * so an old set and a new set for the same run ARE the same ids. Two
     * consequences, both of which have to be handled in one step:
     *
     * <ul>
     *   <li>Registering first and cleaning up after deletes the meters just
     *       registered, leaving a restarted run exporting nothing at all.
     *   <li>Registering a gauge whose id already exists does NOT rebind it:
     *       Micrometer keeps the first state object and silently drops the new
     *       one. The gauge then reports a {@code DoubleAccumulator} nothing
     *       writes to any more, frozen at its last value.
     * </ul>
     *
     * <p>Doing this as a bare remove followed by a put leaves a window where a
     * concurrent start or end for the same run interleaves and produces exactly
     * that second case. {@code compute} holds the map's per-key lock across the
     * whole transition, so the two can no longer overlap.
     */
    public void startRun(String runId, String testType, String backend) {
        runMeters.compute(runId, (id, previous) -> {
            if (previous != null) {
                // A double start (retry, replayed event). Without this the
                // active-run gauge drifts upward and the old meters leak.
                previous.unregister();
            } else {
                activeRuns.incrementAndGet();
            }
            return new RunMeters(runId, testType, backend);
        });
    }

    /**
     * Ends a run and UNREGISTERS its meters. Idempotent: the terminal
     * transition, the timeout reaper and the submission-failure path may all
     * reach here for the same run.
     *
     * <p>Also under {@code compute}, so it cannot land between a restart's
     * unregister and its re-register and take the new meters with it.
     */
    public void endRun(String runId) {
        runMeters.compute(runId, (id, meters) -> {
            if (meters != null) {
                activeRuns.decrementAndGet();
                meters.unregister();
            }
            return null;
        });
    }

    public void recordThroughput(String runId, String phaseName, double recPerSec, double mbPerSec) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.throughputRecPerSec.accumulate(recPerSec);
        meters.throughputMBPerSec.accumulate(mbPerSec);
    }

    /**
     * Publishes one task's polled status for its phase.
     *
     * <p>This is what shapes the error series, and it shapes it the way the
     * record and latency series beside it are shaped:
     *
     * <ul>
     *   <li>The phase's error counter is registered the first time the phase
     *       reports at all, so a healthy run publishes a zero. The benchmark
     *       board's error-rate panel promises a flat zero for a healthy run,
     *       and a series that does not exist reads as No data, which is not
     *       the same promise.
     *   <li>A task is counted the first time it is seen FAILED and never
     *       again. Keyed by task id, so a poll repeated after a lost save and
     *       the terminal transition walking every result one last time both
     *       find the task already counted. That is what lets the count move on
     *       the poll that observes the failure: the terminal transition used to
     *       be the only place failures were counted, and it unregisters the
     *       run's meters in the same call, so a task that died while its
     *       siblings ran on was published for the microseconds between the two
     *       and never scraped.
     * </ul>
     */
    public void recordTaskStatus(String runId, String taskId, String phaseName, TaskStatus status) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        Counter counter = meters.errorCount(phaseName);
        // null once the run's meters have been unregistered — the failure still
        // happened, but there is no live series to add it to.
        if (counter == null) return;
        if (status == TaskStatus.FAILED && meters.failedTasks.add(taskId == null ? "default" : taskId)) {
            counter.increment();
        }
    }

    /**
     * Publishes one task's CUMULATIVE record count for its phase.
     *
     * <p>Cumulative, not a delta: every caller has the running total the
     * backend reports, and a counter fed with deltas double-counts the moment
     * two polls of the same task overlap. The phase's counter reads the sum of
     * the latest total per task, and each task's total only ever moves up
     * ({@code Math::max}), so the series is monotonic — which is what makes
     * {@code rate(kates_benchmark_records_total[30s])} on the benchmark board
     * mean anything. A backend that restarts a task and re-counts from zero
     * therefore holds the counter flat rather than resetting it.
     */
    public void recordRecords(String runId, String taskId, String phaseName, long recordsProcessed) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        PhaseRecords records = meters.phaseRecords(phaseName);
        if (records != null) {
            records.observe(taskId, recordsProcessed);
        }
    }

    /**
     * Publishes one task's latency percentiles for its phase, in milliseconds.
     *
     * <p>These are <b>gauges carrying a {@code quantile} label</b>, not a
     * Micrometer {@code DistributionSummary}. They publish under exactly the
     * names a summary would — {@code kates_benchmark_latency_ms{quantile="…"}}
     * and {@code kates_benchmark_latency_ms_max} — but the values are the
     * backend's own percentiles rather than percentiles Micrometer computed.
     * That is deliberate: the engine never sees individual latencies, only the
     * aggregates a poll returns, and feeding those aggregates into a summary
     * would publish percentiles OF AVERAGES under a name that claims to be a
     * latency distribution. Pre-computed percentile gauges are the same shape
     * the Kafka JMX exporter publishes, and {@code dashboards/METRICS.md}
     * documents the rule that goes with them: select the quantile, never
     * {@code histogram_quantile()}.
     *
     * <p>A phase with several tasks reports the worst task's value for each
     * quantile, recomputed on every poll — so a phase recovers on the board
     * when its slowest task recovers, which a last-writer-wins gauge would not
     * show.
     *
     * <p>A non-positive value means the backend has not measured that quantile
     * yet and is ignored, so an unmeasured p99.9 reads as absent rather than as
     * a zero-millisecond p99.9.
     */
    public void recordLatency(
            String runId,
            String taskId,
            String phaseName,
            double p50Ms,
            double p95Ms,
            double p99Ms,
            double p999Ms,
            double maxMs) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        PhaseLatency latency = meters.phaseLatency(phaseName);
        if (latency != null) {
            latency.observe(taskId, p50Ms, p95Ms, p99Ms, p999Ms, maxMs);
        }
    }

    /**
     * Publishes the run's currently violated SLA constraints, one gauge per
     * (metric, severity), valued 1 while violated and 0 once it recovers.
     *
     * <p>Zero rather than removal: the benchmark board graphs
     * {@code sum by (metric, severity)} over time, and a constraint that stops
     * publishing leaves a gap that reads the same as a constraint that was
     * never evaluated. A constraint that has never been violated is never
     * registered at all, so the board shows only what actually broke, and the
     * meter set is bounded by the number of constraints an SLA can define.
     *
     * <p>Called once per poll with every task's violations merged, not once per
     * task: called per task, the last task polled would clear the violations of
     * the ones before it.
     */
    public void recordSlaViolations(String runId, List<SlaViolation> violations) {
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.applySlaViolations(violations == null ? List.of() : violations);
    }

    /**
     * Publishes one integrity verification's RTO, RPO and data-loss figures.
     *
     * <p>The verifier has computed these since it was written and returned them
     * over the REST API, where a human could read them. Nothing registered them
     * with Micrometer, so the six {@code kafka:chaos:*} recording rules in
     * {@code charts/monitoring/templates/prometheus-chaos-rules.yaml} recorded
     * nothing, the four SLA alerts built on those rules installed cleanly and
     * could never fire, and the {@code kates-chaos} board's RTO/RPO row was
     * collapsed behind a note saying so.
     *
     * <p><b>Milliseconds in, seconds out.</b> {@link IntegrityResult} measures
     * in {@code Duration} and exposes milliseconds; the series names and the
     * alert thresholds are in seconds — {@code KafkaRTOExceedsSLA} fires above
     * 30, meaning thirty seconds. Publishing the millisecond value under a
     * {@code _seconds} name would have made that alert fire at 30ms, which is
     * every run. The division is the whole point of this method existing rather
     * than the caller passing the numbers straight through.
     *
     * <p>Data loss is a percentage in both places, so it is passed through.
     * Called on every poll that carries a verification result: the values are
     * absolute, not cumulative, so re-publishing the same figures is a no-op
     * and a later verification supersedes an earlier one.
     */
    public void recordIntegrity(String runId, com.bmscomp.kates.domain.IntegrityResult integrity) {
        if (integrity == null) return;
        RunMeters meters = runMeters.get(runId);
        if (meters == null) return;

        meters.applyIntegrity(integrity);
    }

    /**
     * One verification's figures, in the units the series names promise.
     *
     * <p>Gauges rather than counters even for the two record counts: a
     * verification reports the totals it found over the window it examined, and
     * the next verification of the same run reports its own totals rather than
     * an increment. A counter fed that way would either double-count or go
     * backwards, and {@code rate()} over a counter that goes backwards invents
     * a spike.
     */
    private static final class IntegrityFigures {
        private volatile double producerRtoSeconds;
        private volatile double consumerRtoSeconds;
        private volatile double maxRtoSeconds;
        private volatile double rpoSeconds;
        private volatile double dataLossPercent;
        private volatile double lostRecords;
        private volatile double duplicateRecords;

        void observe(com.bmscomp.kates.domain.IntegrityResult r) {
            producerRtoSeconds = r.producerRtoMs() / 1000.0;
            consumerRtoSeconds = r.consumerRtoMs() / 1000.0;
            maxRtoSeconds = r.maxRtoMs() / 1000.0;
            rpoSeconds = r.rpoMs() / 1000.0;
            dataLossPercent = r.dataLossPercent();
            lostRecords = r.lostRecords();
            duplicateRecords = r.duplicateRecords();
        }
    }

    /**
     * The latest cumulative record count of every task in one phase.
     *
     * <p>Held per task so the phase total is a sum rather than whichever task
     * was polled last, and clamped upward so the sum can only ever grow: a
     * counter that goes backwards reads to Prometheus as a counter reset, and
     * {@code rate()} over a reset invents a spike that never happened.
     */
    private static final class PhaseRecords {
        private final Map<String, Long> perTask = new ConcurrentHashMap<>();

        void observe(String taskId, long cumulative) {
            if (cumulative < 0) return;
            perTask.merge(taskId == null ? "default" : taskId, cumulative, Math::max);
        }

        double total() {
            long sum = 0;
            for (long v : perTask.values()) {
                sum += v;
            }
            return sum;
        }
    }

    /**
     * The latest latency aggregates of every task in one phase.
     *
     * <p>Each gauge reads the worst task's value for its quantile, recomputed
     * on every read, so a phase's p99 falls again when its slowest task
     * recovers. A task that has not reported a given quantile contributes
     * nothing rather than contributing a zero.
     */
    private static final class PhaseLatency {
        static final int P50 = 0;
        static final int P95 = 1;
        static final int P99 = 2;
        static final int P999 = 3;
        static final int MAX = 4;

        private final Map<String, double[]> perTask = new ConcurrentHashMap<>();

        void observe(String taskId, double p50, double p95, double p99, double p999, double max) {
            perTask.put(taskId == null ? "default" : taskId, new double[] {p50, p95, p99, p999, max});
        }

        double worst(int index) {
            double worst = 0;
            for (double[] sample : perTask.values()) {
                if (sample[index] > worst) {
                    worst = sample[index];
                }
            }
            return worst;
        }
    }

    private class RunMeters {
        final String runId;
        final String testType;
        final DoubleAccumulator throughputRecPerSec;
        final DoubleAccumulator throughputMBPerSec;
        private final Map<String, Counter> errorCounters = new ConcurrentHashMap<>();
        /** Tasks already counted into their phase's error counter. */
        private final java.util.Set<String> failedTasks = ConcurrentHashMap.newKeySet();

        private final Map<String, PhaseRecords> phaseRecords = new ConcurrentHashMap<>();
        private final Map<String, PhaseLatency> phaseLatencies = new ConcurrentHashMap<>();
        private final Map<String, AtomicInteger> slaViolationGauges = new ConcurrentHashMap<>();
        private final java.util.concurrent.atomic.AtomicReference<IntegrityFigures> integrity =
                new java.util.concurrent.atomic.AtomicReference<>();

        /** Every meter this run registered, so endRun can remove all of them. */
        private final List<Meter.Id> meterIds = new CopyOnWriteArrayList<>();

        /** Set by unregister(); stops a late error from registering a new meter. */
        private volatile boolean unregistered;

        RunMeters(String runId, String testType, String backend) {
            this.runId = runId;
            this.testType = testType;

            this.throughputRecPerSec = new DoubleAccumulator((a, b) -> b, 0);
            this.throughputMBPerSec = new DoubleAccumulator((a, b) -> b, 0);

            Tags baseTags = Tags.of("run_id", runId, "test_type", testType, "backend", backend);
            meterIds.add(
                    Gauge.builder("kates.benchmark.throughput.rec.sec", throughputRecPerSec, DoubleAccumulator::get)
                            .tags(baseTags)
                            .description("Current throughput in records/sec")
                            .register(registry)
                            .getId());
            meterIds.add(Gauge.builder("kates.benchmark.throughput.mb.sec", throughputMBPerSec, DoubleAccumulator::get)
                    .tags(baseTags)
                    .description("Current throughput in MB/sec")
                    .register(registry)
                    .getId());
        }

        Counter errorCount(String phase) {
            // Returns null once the run is over. A late recordTaskStatus racing
            // unregister() used to register a fresh counter AFTER the id list
            // had been cleared, so that run_id series stayed in the registry
            // for the life of the process — the leak unregister() exists to
            // prevent, reintroduced by the last error of every run that fails
            // at the finish line.
            if (unregistered) {
                return null;
            }
            return errorCounters.computeIfAbsent(phase == null ? "default" : phase, p -> {
                Counter counter = Counter.builder("kates.benchmark.errors.total")
                        .tags("run_id", runId, "test_type", testType, "phase", p)
                        .description("Total errors")
                        .register(registry);
                meterIds.add(counter.getId());
                return counter;
            });
        }

        /**
         * The record counter for a phase, registered on first sight. Null once
         * the run is over, for the same reason {@link #errorCount(String)} is:
         * a late poll must not register a fresh meter after the id list has
         * been cleared, which would leave that run_id series in the registry
         * for the life of the process.
         */
        PhaseRecords phaseRecords(String phase) {
            if (unregistered) {
                return null;
            }
            return phaseRecords.computeIfAbsent(phase == null ? "default" : phase, p -> {
                PhaseRecords records = new PhaseRecords();
                meterIds.add(FunctionCounter.builder("kates.benchmark.records.total", records, PhaseRecords::total)
                        .tags("run_id", runId, "test_type", testType, "phase", p)
                        .description("Records processed by this run's phase")
                        .register(registry)
                        .getId());
                return records;
            });
        }

        /** The five latency gauges for a phase, registered on first sight. */
        PhaseLatency phaseLatency(String phase) {
            if (unregistered) {
                return null;
            }
            return phaseLatencies.computeIfAbsent(phase == null ? "default" : phase, p -> {
                PhaseLatency latency = new PhaseLatency();
                Tags phaseTags = Tags.of("run_id", runId, "test_type", testType, "phase", p);
                registerQuantile(latency, phaseTags, "0.5", PhaseLatency.P50);
                registerQuantile(latency, phaseTags, "0.95", PhaseLatency.P95);
                registerQuantile(latency, phaseTags, "0.99", PhaseLatency.P99);
                registerQuantile(latency, phaseTags, "0.999", PhaseLatency.P999);
                meterIds.add(Gauge.builder("kates.benchmark.latency.ms.max", latency, l -> l.worst(PhaseLatency.MAX))
                        .tags(phaseTags)
                        .description("Worst latency observed in this run's phase, ms")
                        .register(registry)
                        .getId());
                return latency;
            });
        }

        private void registerQuantile(PhaseLatency latency, Tags phaseTags, String quantile, int index) {
            meterIds.add(Gauge.builder("kates.benchmark.latency.ms", latency, l -> l.worst(index))
                    .tags(phaseTags.and("quantile", quantile))
                    .description("Latency percentile for this run's phase, ms")
                    .register(registry)
                    .getId());
        }

        /**
         * Sets every violated constraint to 1 and every constraint that has
         * ever been violated in this run and is not in {@code violations} back
         * to 0.
         */
        void applySlaViolations(List<SlaViolation> violations) {
            if (unregistered) {
                return;
            }
            java.util.Set<String> violated = new java.util.HashSet<>();
            for (SlaViolation violation : violations) {
                if (violation == null || violation.metric() == null) {
                    continue;
                }
                String severity = violation.severity() == null
                        ? "unknown"
                        : violation.severity().name().toLowerCase(java.util.Locale.ROOT);
                String key = violation.metric() + "\u0000" + severity;
                violated.add(key);
                AtomicInteger state = slaViolationGauge(violation.metric(), severity, key);
                if (state != null) {
                    state.set(1);
                }
            }
            for (Map.Entry<String, AtomicInteger> entry : slaViolationGauges.entrySet()) {
                if (!violated.contains(entry.getKey())) {
                    entry.getValue().set(0);
                }
            }
        }

        /**
         * Registers the seven integrity gauges on the first verification this
         * run reports, then updates their backing figures.
         *
         * <p>Registered lazily for the same reason the SLA gauges are: a run
         * that never verifies integrity — every plain load test — should not
         * publish seven zero-valued series claiming a perfect RTO it never
         * measured. An empty panel there means "not verified"; a zero would
         * mean "verified, recovered instantly".
         */
        void applyIntegrity(com.bmscomp.kates.domain.IntegrityResult result) {
            if (unregistered) {
                return;
            }
            IntegrityFigures figures = integrity.get();
            if (figures == null) {
                IntegrityFigures created = new IntegrityFigures();
                if (integrity.compareAndSet(null, created)) {
                    Tags tags = Tags.of("run_id", runId, "test_type", testType);
                    registerIntegrityGauge(
                            "kates.integrity.result.producer.rto.seconds",
                            created,
                            tags,
                            f -> f.producerRtoSeconds,
                            "Seconds the producer took to recover after the fault");
                    registerIntegrityGauge(
                            "kates.integrity.result.consumer.rto.seconds",
                            created,
                            tags,
                            f -> f.consumerRtoSeconds,
                            "Seconds the consumer took to recover after the fault");
                    registerIntegrityGauge(
                            "kates.integrity.result.max.rto.seconds",
                            created,
                            tags,
                            f -> f.maxRtoSeconds,
                            "The worse of the producer and consumer recovery times, seconds");
                    registerIntegrityGauge(
                            "kates.integrity.result.rpo.seconds",
                            created,
                            tags,
                            f -> f.rpoSeconds,
                            "Width of the window whose writes did not survive, seconds");
                    registerIntegrityGauge(
                            "kates.integrity.result.data.loss.percent",
                            created,
                            tags,
                            f -> f.dataLossPercent,
                            "Percentage of acknowledged records that never arrived");
                    registerIntegrityGauge(
                            "kates.integrity.result.lost.records",
                            created,
                            tags,
                            f -> f.lostRecords,
                            "Acknowledged records the consumer never saw");
                    registerIntegrityGauge(
                            "kates.integrity.result.duplicate.records",
                            created,
                            tags,
                            f -> f.duplicateRecords,
                            "Records the consumer saw more than once");
                    figures = created;
                } else {
                    // Another poll of the same run registered them first.
                    figures = integrity.get();
                }
            }
            if (figures != null) {
                figures.observe(result);
            }
        }

        private void registerIntegrityGauge(
                String name,
                IntegrityFigures figures,
                Tags tags,
                java.util.function.ToDoubleFunction<IntegrityFigures> read,
                String description) {
            meterIds.add(Gauge.builder(name, figures, read)
                    .tags(tags)
                    .description(description)
                    .register(registry)
                    .getId());
        }

        private AtomicInteger slaViolationGauge(String metric, String severity, String key) {
            if (unregistered) {
                return null;
            }
            return slaViolationGauges.computeIfAbsent(key, k -> {
                AtomicInteger state = new AtomicInteger();
                meterIds.add(Gauge.builder("kates.benchmark.sla.violations", state, AtomicInteger::get)
                        .tags("run_id", runId, "test_type", testType, "metric", metric, "severity", severity)
                        .description("1 while this SLA constraint is violated by the run, 0 once it recovers")
                        .register(registry)
                        .getId());
                return state;
            });
        }

        void unregister() {
            unregistered = true;
            for (Meter.Id id : meterIds) {
                registry.remove(id);
            }
            meterIds.clear();
            errorCounters.clear();
            failedTasks.clear();
            phaseRecords.clear();
            phaseLatencies.clear();
            slaViolationGauges.clear();
            integrity.set(null);
        }
    }
}
