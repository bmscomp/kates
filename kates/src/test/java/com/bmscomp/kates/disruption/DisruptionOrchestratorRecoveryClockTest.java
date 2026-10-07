package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyCollection;
import static org.mockito.ArgumentMatchers.anyMap;
import static org.mockito.Mockito.*;

import java.time.Duration;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.LongConsumer;
import java.util.stream.Stream;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.DescribeTopicsResult;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsResult;
import org.apache.kafka.clients.admin.ListOffsetsResult;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.TopicPartitionInfo;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.chaos.ChaosCoordinator;
import com.bmscomp.kates.chaos.ChaosOutcome;
import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.K8sPodWatcher;
import com.bmscomp.kates.chaos.StrimziStateTracker;
import com.bmscomp.kates.chaos.TestPodWatch;
import com.bmscomp.kates.engine.KatesMetrics;
import com.bmscomp.kates.service.KafkaAdminService;

/**
 * A step's recovery clocks (its pods Ready, its topic's ISR full, its group's
 * lag back, the Kafka resource Ready) start when the chaos provider reports
 * the fault going in. They used to start before the trigger, so the fault's
 * delayBeforeSec, which both providers wait out before they inject, counted
 * as recovery time: up to 600 s of it.
 */
class DisruptionOrchestratorRecoveryClockTest {

    private static final String TOPIC = "orders";
    private static final String GROUP = "orders-cg";
    private static final TopicPartition PARTITION = new TopicPartition(TOPIC, 0);
    private static final List<Node> REPLICAS =
            List.of(new Node(3, "broker-3", 9092), new Node(4, "broker-4", 9092), new Node(5, "broker-5", 9092));

    private static final Duration DELAY = Duration.ofSeconds(2);
    /** How long the fault keeps broker 3 down. */
    private static final Duration OUTAGE = Duration.ofMillis(300);
    /** How far the wall clock may fall behind System.nanoTime(), which times the sleeps. */
    private static final Duration DRIFT = Duration.ofMillis(5);

    private final TestPodWatch watch = new TestPodWatch("krafter-brokers-3", "krafter-brokers-4", "krafter-brokers-5");
    private final AtomicReference<List<Node>> isr = new AtomicReference<>(REPLICAS);
    private final AtomicLong committed = new AtomicLong(900);
    private final AtomicLong logEnd = new AtomicLong(1_000);
    private KafkaIntelligenceService intelligence;
    private DisruptionOrchestrator orchestrator;

    @BeforeEach
    void setUp() {
        AdminClient admin = mock(AdminClient.class);
        DescribeTopicsResult topics = mock(DescribeTopicsResult.class);
        when(topics.allTopicNames()).thenAnswer(inv -> {
            List<Node> inSync = isr.get();
            TopicPartitionInfo partition = new TopicPartitionInfo(0, inSync.getFirst(), REPLICAS, inSync);
            return KafkaFuture.completedFuture(Map.of(TOPIC, new TopicDescription(TOPIC, false, List.of(partition))));
        });
        when(admin.describeTopics(anyCollection())).thenReturn(topics);
        ListConsumerGroupOffsetsResult offsets = mock(ListConsumerGroupOffsetsResult.class);
        when(offsets.partitionsToOffsetAndMetadata())
                .thenAnswer(
                        inv -> KafkaFuture.completedFuture(Map.of(PARTITION, new OffsetAndMetadata(committed.get()))));
        when(admin.listConsumerGroupOffsets(GROUP)).thenReturn(offsets);
        ListOffsetsResult ends = mock(ListOffsetsResult.class);
        when(ends.all())
                .thenAnswer(inv -> KafkaFuture.completedFuture(Map.of(
                        PARTITION, new ListOffsetsResult.ListOffsetsResultInfo(logEnd.get(), -1L, Optional.empty()))));
        when(admin.listOffsets(anyMap())).thenReturn(ends);
        KafkaAdminService adminService = mock(KafkaAdminService.class);
        when(adminService.sharedAdminClient()).thenReturn(admin);
        intelligence = new KafkaIntelligenceService(adminService);

        orchestrator = new DisruptionOrchestrator();
        orchestrator.chaosCoordinator = mock(ChaosCoordinator.class);
        orchestrator.podWatcher = mock(K8sPodWatcher.class);
        orchestrator.strimziTracker = mock(StrimziStateTracker.class);
        orchestrator.intelligence = intelligence;
        orchestrator.safetyGuard = mock(DisruptionSafetyGuard.class);
        orchestrator.prometheusCapture = mock(PrometheusMetricsCapture.class);
        orchestrator.slaGrader = mock(SlaGrader.class);
        orchestrator.eventBus = mock(DisruptionEventBus.class);
        orchestrator.katesMetrics = mock(KatesMetrics.class);
        orchestrator.concurrencyGuard = new DisruptionConcurrencyGuard();
        orchestrator.recoveryTimeoutSec = 5;
        when(orchestrator.safetyGuard.validatePlan(any()))
                .thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of()));
        when(orchestrator.safetyGuard.verifyClusterState()).thenReturn(true);
        when(orchestrator.podWatcher.startWatching(any(), any())).thenReturn(watch.session());
        when(orchestrator.strimziTracker.measureRecoveryTime(any(), any(), any(), any()))
                .thenReturn(Duration.ZERO);
    }

    @AfterEach
    void stopTrackers() {
        intelligence.shutdown();
    }

    /** One step that kills broker 3 and waits for recovery; the plan tracks the topic's ISR and the group's lag. */
    private static DisruptionPlan plan(Duration delay) {
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("kill-broker-3");
        plan.setIsrTrackingTopic(TOPIC);
        plan.setIsrPollIntervalMs(10);
        plan.setLagTrackingGroupId(GROUP);
        plan.setLagPollIntervalMs(10);
        plan.getSteps()
                .add(new DisruptionPlan.DisruptionStep(
                        "kill",
                        FaultSpec.builder("kill")
                                .disruptionType(DisruptionType.POD_KILL)
                                .targetBrokerId(3)
                                .delayBeforeSec((int) delay.toSeconds())
                                .chaosDurationSec(30)
                                .build(),
                        0,
                        0,
                        true));
        return plan;
    }

    /**
     * Injects the fault as both chaos providers do, on a thread of its own:
     * it waits out the delay, reports the moment the fault goes in, and only
     * then changes the cluster. Broker 3 is down for {@link #OUTAGE}, taking
     * the partition's ISR down to two, while the group's consumer falls
     * behind; then all three recover, and the fault runs on a little, as a
     * Litmus experiment runs to the end of its duration, so the trackers
     * sample the recovery before the step stops them.
     */
    private CompletableFuture<ChaosOutcome> inject(FaultSpec spec, LongConsumer onInject) {
        CompletableFuture<ChaosOutcome> outcome = new CompletableFuture<>();
        Thread.ofVirtual().start(() -> {
            try {
                Thread.sleep(spec.delayBeforeSec() * 1000L);
                long injectedAt = System.nanoTime();
                onInject.accept(injectedAt);
                Instant start = Instant.now();
                watch.notReady("krafter-brokers-3");
                isr.set(REPLICAS.subList(1, 3));
                logEnd.set(6_000);
                Thread.sleep(OUTAGE.toMillis());
                watch.ready("krafter-brokers-3");
                isr.set(REPLICAS);
                committed.set(5_900);
                Thread.sleep(100);
                outcome.complete(ChaosOutcome.success(
                        "kill-1", spec.experimentName(), start, Instant.now(), injectedAt, null, null, null));
            } catch (InterruptedException e) {
                outcome.completeExceptionally(e);
            }
        });
        return outcome;
    }

    @Test
    void aDelayedFaultsRecoveryClocksStartWhenItGoesIn() {
        AtomicReference<Instant> triggeredAt = new AtomicReference<>();
        when(orchestrator.chaosCoordinator.triggerFault(any(), any())).thenAnswer(inv -> {
            triggeredAt.set(Instant.now());
            return inject(inv.getArgument(0), inv.getArgument(1));
        });

        DisruptionReport report = orchestrator.execute(plan(DELAY));

        DisruptionReport.StepReport step = report.getStepReports().getFirst();
        assertEquals("Pass", step.chaosOutcome().verdict());
        // Each time is about as long as the outage. Counted from before the
        // trigger, each was the delay and the outage together.
        Map<String, Duration> recovery = new LinkedHashMap<>();
        recovery.put("timeToFirstReady", step.timeToFirstReady());
        recovery.put("timeToAllReady", step.timeToAllReady());
        recovery.put("timeToFullIsr", step.isrMetrics().timeToFullIsr());
        recovery.put("timeToLagRecovery", step.lagMetrics().timeToLagRecovery());
        recovery.forEach((name, time) -> {
            assertNotNull(time, name);
            assertTrue(time.compareTo(OUTAGE.minus(DRIFT)) >= 0, name + " " + time + " is shorter than the outage");
            assertTrue(time.compareTo(DELAY) < 0, name + " " + time + " counts the delay");
        });
        assertEquals(100, step.lagMetrics().baselineLag());

        ArgumentCaptor<Instant> from = ArgumentCaptor.forClass(Instant.class);
        verify(orchestrator.strimziTracker).measureRecoveryTime(any(), any(), from.capture(), any());
        Duration sinceTrigger = Duration.between(triggeredAt.get(), from.getValue());
        assertTrue(
                sinceTrigger.compareTo(DELAY.minus(DRIFT)) >= 0,
                "the Kafka resource's recovery counts from " + sinceTrigger + " after the trigger");
    }

    static Stream<ChaosOutcome> faultsThatNeverWentIn() {
        Instant now = Instant.now();
        return Stream.of(
                // What noop answers for every fault.
                ChaosOutcome.skipped("No chaos provider configured — inject faults manually"),
                // A fault that failed before the provider injected it.
                ChaosOutcome.failure(
                        "kill-1", "kill", now, now, System.nanoTime(), "no pod matches", null, null, null));
    }

    @ParameterizedTest
    @MethodSource("faultsThatNeverWentIn")
    void aFaultThatNeverWentInLeavesNothingToRecoverFrom(ChaosOutcome outcome) {
        when(orchestrator.chaosCoordinator.triggerFault(any(), any()))
                .thenReturn(CompletableFuture.completedFuture(outcome));

        DisruptionReport report = orchestrator.execute(plan(Duration.ZERO));

        DisruptionReport.StepReport step = report.getStepReports().getFirst();
        assertEquals(outcome.verdict(), step.chaosOutcome().verdict());
        // Such a step used to wait for a recovery from nothing, roll back when
        // none came, and report times measured on a cluster nothing disturbed.
        assertNull(step.timeToFirstReady());
        assertNull(step.timeToAllReady());
        assertNull(step.unrecoveredAfter());
        assertNull(step.strimziRecoveryTime());
        assertNull(step.isrMetrics());
        assertNull(step.lagMetrics());
        assertFalse(step.rolledBack());
        verify(orchestrator.safetyGuard, never()).rollback(any(), any());
        verify(orchestrator.strimziTracker, never()).measureRecoveryTime(any(), any(), any(), any());
    }
}
