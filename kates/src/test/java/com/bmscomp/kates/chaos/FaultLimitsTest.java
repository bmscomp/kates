package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.EnumSource;

class FaultLimitsTest {

    private final FaultLimits limits = new FaultLimits();

    /** A fault as the API reads it: the fields the JSON leaves out get the builder's defaults. */
    private static FaultSpec posted(String json) throws Exception {
        return new ObjectMapper().readValue(json, FaultSpec.class);
    }

    private static FaultSpec posted(String field, int value) throws Exception {
        return posted("{\"experimentName\":\"probe\",\"" + field + "\":" + value + "}");
    }

    @ParameterizedTest
    @EnumSource(DisruptionType.class)
    void theDefaultsAreWithinTheLimitsForEveryType(DisruptionType type) {
        assertEquals(
                Map.of(),
                limits.violations(
                        FaultSpec.builder("defaults").disruptionType(type).build()));
    }

    @ParameterizedTest
    @CsvSource({
        "chaosDurationSec,  3600,  kates.chaos.limits.max-duration-sec",
        "delayBeforeSec,    600,   kates.chaos.limits.max-delay-sec",
        "networkLatencyMs,  30000, kates.chaos.limits.max-network-latency-ms",
        "fillPercentage,    100,   kates.chaos.limits.max-fill-percentage",
        "cpuCores,          64,    kates.chaos.limits.max-cpu-cores",
        "memoryMb,          32768, kates.chaos.limits.max-memory-mb",
        "ioWorkers,         64,    kates.chaos.limits.max-io-workers",
        "gracePeriodSec,    300,   kates.chaos.limits.max-grace-period-sec",
    })
    void eachParameterMayReachItsLimitButNotPassIt(String field, int limit, String setting) throws Exception {
        assertEquals(Map.of(), limits.violations(posted(field, limit)));
        assertEquals(
                Map.of(field, (limit + 1) + " is above the limit of " + limit + " (" + setting + ")"),
                limits.violations(posted(field, limit + 1)));
    }

    @ParameterizedTest
    @CsvSource({
        "chaosDurationSec, -1, 0",
        "delayBeforeSec,   -1, 0",
        // 0 is every core to Litmus's stress-ng.
        "cpuCores,          0, 1",
        "networkLatencyMs,  0, 1",
        "fillPercentage,    0, 1",
        "memoryMb,          0, 1",
        "ioWorkers,         0, 1",
        "gracePeriodSec,   -1, 0",
    })
    void eachParameterHasAFloor(String field, int value, int floor) throws Exception {
        assertEquals(Map.of(field, value + " is below " + floor), limits.violations(posted(field, value)));
    }

    @ParameterizedTest
    @EnumSource(
            value = DisruptionType.class,
            names = {
                "NETWORK_PARTITION",
                "NETWORK_LATENCY",
                "CPU_STRESS",
                "MEMORY_STRESS",
                "IO_STRESS",
                "DNS_ERROR",
                "DISK_FILL",
                "NODE_DRAIN"
            })
    void aFaultUndoneWhenItsDurationEndsNeedsOne(DisruptionType type) {
        FaultSpec spec = FaultSpec.builder("forever")
                .disruptionType(type)
                .chaosDurationSec(0)
                .build();

        assertEquals(
                Map.of("chaosDurationSec", "0 is below 1, and a " + type + " is undone only when its duration ends"),
                limits.violations(spec));
    }

    /** A pod comes back by itself; a roll or a scale-down with 0 just does not wait. */
    @ParameterizedTest
    @EnumSource(
            value = DisruptionType.class,
            names = {"POD_KILL", "POD_DELETE", "LEADER_ELECTION", "ROLLING_RESTART", "SCALE_DOWN"})
    void aFaultThatEndsByItselfMayHaveNoDuration(DisruptionType type) {
        assertEquals(
                Map.of(),
                limits.violations(FaultSpec.builder("instant")
                        .disruptionType(type)
                        .chaosDurationSec(0)
                        .build()));
    }

    @Test
    void refusedOverridesAreNamedInKeyOrder() throws Exception {
        FaultSpec spec = posted("{\"experimentName\":\"probe\",\"disruptionType\":\"NETWORK_LATENCY\","
                + "\"envOverrides\":{\"TOTAL_CHAOS_DURATION\":\"86400\",\"RAMP_TIME\":\"3600\","
                + "\"NETWORK_INTERFACE\":\"eth0\"}}");

        assertEquals(
                List.of(
                        "envOverrides.NETWORK_INTERFACE",
                        "envOverrides.RAMP_TIME",
                        "envOverrides.TOTAL_CHAOS_DURATION"),
                List.copyOf(limits.violations(spec).keySet()));
    }

    /**
     * Litmus applies an override after the env Kates sets: its duration, the
     * namespace and pods (litmus-go and chaos-runner 3.28). Others reach its
     * helper pods, which run privileged as root: their image, their service
     * account, and values the helpers paste into a shell.
     */
    @ParameterizedTest
    @CsvSource({
        "TOTAL_CHAOS_DURATION",
        "RAMP_TIME",
        "CHAOS_NAMESPACE",
        "TARGETS",
        "APP_NAMESPACE",
        "APP_LABEL",
        "APP_KIND",
        "TARGET_PODS",
        "LIB_IMAGE",
        "CHAOS_SERVICE_ACCOUNT",
        "SOCKET_PATH",
        "CHAOS_TYPE",
        "NETWORK_INTERFACE"
    })
    void anOverrideMaySetOnlyTheNodeADrainTakes(String key) {
        FaultSpec spec = FaultSpec.builder("probe")
                .envOverrides(Map.of(key, "kube-system", "TARGET_NODE", "gamma", "NODE_LABEL", "drainable=true"))
                .build();

        assertEquals(
                List.of("envOverrides." + key),
                List.copyOf(limits.violations(spec).keySet()));
    }

    /** On litmus-crd, a DNS_ERROR's targetTopic goes into a shell command in Litmus's root helper pod. */
    @Test
    void aTargetTopicMustBeAKafkaTopicName() {
        FaultSpec.Builder dns = FaultSpec.builder("dns").disruptionType(DisruptionType.DNS_ERROR);

        assertEquals(
                List.of("targetTopic"),
                List.copyOf(
                        limits.violations(dns.targetTopic("x'; reboot -f; '").build())
                                .keySet()));
        assertEquals(
                List.of("targetTopic"),
                List.copyOf(limits.violations(dns.targetTopic("a".repeat(250)).build())
                        .keySet()));
        assertEquals(
                Map.of(),
                limits.violations(dns.targetTopic("__consumer_offsets").build()));
        assertEquals(
                Map.of(),
                limits.violations(
                        dns.targetTopic("krafter-kafka-bootstrap.kafka.svc").build()));
        assertEquals(Map.of(), limits.violations(dns.targetTopic("").build()));
        assertEquals(Map.of(), limits.violations(dns.targetTopic(null).build()));
    }

    /** On litmus-crd a fault's cmdProbe ran in Litmus's experiment pod as litmus-admin. */
    @Test
    void aFaultMayCarryNoCmdProbe() {
        FaultSpec spec = FaultSpec.builder("probed")
                .probes(List.of(
                        KafkaProbes.isrHealth(),
                        ProbeSpec.builder("shell")
                                .command("kubectl delete pods -n kube-system --all")
                                .build(),
                        KafkaProbes.clusterReady()))
                .build();

        assertEquals(List.of("probes[1]"), List.copyOf(limits.violations(spec).keySet()));
    }

    @Test
    void checkRefusesNamingEveryParameterInFieldOrder() throws Exception {
        FaultSpec spec = posted("{\"experimentName\":\"runaway\",\"disruptionType\":\"CPU_STRESS\","
                + "\"cpuCores\":1000,\"chaosDurationSec\":86400}");

        IllegalArgumentException refused = assertThrows(IllegalArgumentException.class, () -> limits.check(spec));

        assertEquals(
                "Fault 'runaway' is outside the chaos limits: chaosDurationSec 86400 is above the limit of 3600"
                        + " (kates.chaos.limits.max-duration-sec); cpuCores 1000 is above the limit of 64"
                        + " (kates.chaos.limits.max-cpu-cores)",
                refused.getMessage());
    }

    @Test
    void aRaisedLimitLetsThroughWhatTheDefaultRefuses() throws Exception {
        FaultSpec soak = posted("chaosDurationSec", 7200);
        assertFalse(limits.violations(soak).isEmpty());

        limits.maxDurationSec = 7200;

        assertEquals(Map.of(), limits.violations(soak));
        assertDoesNotThrow(() -> limits.check(soak));
    }
}
