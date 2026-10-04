package com.bmscomp.kates.chaos;

import java.util.EnumSet;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;

import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * The range each parameter of a fault has to be in. A fault with one outside
 * it is refused before any chaos provider sees it: the safety guard refuses
 * the plan (plans, playbooks, templates, schedules), the coordinator refuses
 * the fault (those, and resilience runs), and the compound orchestrator, which
 * calls the providers itself, refuses its whole run.
 *
 * <p>Nothing bounded them before. A CPU or IO stress on the kubernetes provider
 * runs in an ephemeral container, which nothing can remove, so the duration it
 * was given is all that stops it. A NETWORK_PARTITION of 0 seconds was never
 * removed. {@code cpuCores} 0 is every core to Litmus's stress-ng.
 *
 * <p>The ceilings are generous on purpose: they stop a typo or a runaway value,
 * not a fault a game day means to run. Raise one in configuration to go past it.
 */
@ApplicationScoped
public class FaultLimits {

    static final String MAX_DURATION_SEC = "kates.chaos.limits.max-duration-sec";
    static final String MAX_DELAY_SEC = "kates.chaos.limits.max-delay-sec";
    static final String MAX_NETWORK_LATENCY_MS = "kates.chaos.limits.max-network-latency-ms";
    static final String MAX_FILL_PERCENTAGE = "kates.chaos.limits.max-fill-percentage";
    static final String MAX_CPU_CORES = "kates.chaos.limits.max-cpu-cores";
    static final String MAX_MEMORY_MB = "kates.chaos.limits.max-memory-mb";
    static final String MAX_IO_WORKERS = "kates.chaos.limits.max-io-workers";
    static final String MAX_GRACE_PERIOD_SEC = "kates.chaos.limits.max-grace-period-sec";

    /**
     * Faults that stay in place until their duration ends, so a duration of 0
     * leaves nothing to end them. The kubernetes provider removes a partition
     * when the duration ends, and a stress container stops at its timeout;
     * Litmus undoes each of these after TOTAL_CHAOS_DURATION. The others end
     * by themselves (a deleted pod comes back), or read 0 as "do not wait"
     * (ROLLING_RESTART, SCALE_DOWN).
     */
    static final Set<DisruptionType> UNDONE_WHEN_DURATION_ENDS = EnumSet.of(
            DisruptionType.NETWORK_PARTITION,
            DisruptionType.NETWORK_LATENCY,
            DisruptionType.CPU_STRESS,
            DisruptionType.MEMORY_STRESS,
            DisruptionType.IO_STRESS,
            DisruptionType.DNS_ERROR,
            DisruptionType.DISK_FILL,
            DisruptionType.NODE_DRAIN);

    /**
     * Litmus settings an override would take past the limits. Kates sets
     * TOTAL_CHAOS_DURATION from chaosDurationSec, and an override comes after
     * it; RAMP_TIME makes Litmus wait before the fault and after it, longer
     * than Kates waits for the result.
     */
    private static final Map<String, String> UNBOUNDED_LITMUS_ENV = Map.of(
            "RAMP_TIME", "would delay the Litmus fault, with no limit, past the time Kates waits for it",
            "TOTAL_CHAOS_DURATION", "would set the fault's duration past its limit; set chaosDurationSec instead");

    // Each default is written twice: in the annotation, for the application,
    // and as the field's value, for a FaultLimits made with new, as tests make it.

    @ConfigProperty(name = MAX_DURATION_SEC, defaultValue = "3600")
    int maxDurationSec = 3600;

    @ConfigProperty(name = MAX_DELAY_SEC, defaultValue = "600")
    int maxDelaySec = 600;

    @ConfigProperty(name = MAX_NETWORK_LATENCY_MS, defaultValue = "30000")
    int maxNetworkLatencyMs = 30_000;

    @ConfigProperty(name = MAX_FILL_PERCENTAGE, defaultValue = "100")
    int maxFillPercentage = 100;

    @ConfigProperty(name = MAX_CPU_CORES, defaultValue = "64")
    int maxCpuCores = 64;

    @ConfigProperty(name = MAX_MEMORY_MB, defaultValue = "32768")
    int maxMemoryMb = 32_768;

    @ConfigProperty(name = MAX_IO_WORKERS, defaultValue = "64")
    int maxIoWorkers = 64;

    @ConfigProperty(name = MAX_GRACE_PERIOD_SEC, defaultValue = "300")
    int maxGracePeriodSec = 300;

    /**
     * Each parameter of {@code spec} outside its range, with why, keyed by the
     * field's name in the order FaultSpec declares them; empty when every one
     * is in range. Every parameter is checked, whatever the fault's type: the
     * builder's defaults are all in range, so only a value a caller set fails.
     */
    public Map<String, String> violations(FaultSpec spec) {
        Map<String, String> found = new LinkedHashMap<>();
        DisruptionType type = spec.disruptionType();
        if (UNDONE_WHEN_DURATION_ENDS.contains(type) && spec.chaosDurationSec() < 1) {
            found.put(
                    "chaosDurationSec",
                    spec.chaosDurationSec() + " is below 1, and a " + type + " is undone only when its duration ends");
        } else {
            range(found, "chaosDurationSec", spec.chaosDurationSec(), 0, maxDurationSec, MAX_DURATION_SEC);
        }
        range(found, "delayBeforeSec", spec.delayBeforeSec(), 0, maxDelaySec, MAX_DELAY_SEC);
        if (spec.envOverrides() != null) {
            UNBOUNDED_LITMUS_ENV.entrySet().stream()
                    .filter(e -> spec.envOverrides().containsKey(e.getKey()))
                    .sorted(Map.Entry.comparingByKey())
                    .forEach(e -> found.put("envOverrides." + e.getKey(), e.getValue()));
        }
        range(found, "networkLatencyMs", spec.networkLatencyMs(), 1, maxNetworkLatencyMs, MAX_NETWORK_LATENCY_MS);
        range(found, "fillPercentage", spec.fillPercentage(), 1, maxFillPercentage, MAX_FILL_PERCENTAGE);
        range(found, "cpuCores", spec.cpuCores(), 1, maxCpuCores, MAX_CPU_CORES);
        range(found, "memoryMb", spec.memoryMb(), 1, maxMemoryMb, MAX_MEMORY_MB);
        range(found, "ioWorkers", spec.ioWorkers(), 1, maxIoWorkers, MAX_IO_WORKERS);
        range(found, "gracePeriodSec", spec.gracePeriodSec(), 0, maxGracePeriodSec, MAX_GRACE_PERIOD_SEC);
        return found;
    }

    /**
     * Refuses {@code spec} when a parameter is outside its range.
     *
     * @throws IllegalArgumentException naming each one
     */
    public void check(FaultSpec spec) {
        Map<String, String> found = violations(spec);
        if (!found.isEmpty()) {
            throw new IllegalArgumentException(
                    "Fault '" + spec.experimentName() + "' is outside the chaos limits: " + describe(found));
        }
    }

    /** The violations as a sentence each: the field, then why. */
    public static String describe(Map<String, String> violations) {
        return violations.entrySet().stream()
                .map(e -> e.getKey() + " " + e.getValue())
                .collect(Collectors.joining("; "));
    }

    private static void range(Map<String, String> found, String field, int value, int min, int max, String key) {
        if (value < min) {
            found.put(field, value + " is below " + min);
        } else if (value > max) {
            found.put(field, value + " is above the limit of " + max + " (" + key + ")");
        }
    }
}
