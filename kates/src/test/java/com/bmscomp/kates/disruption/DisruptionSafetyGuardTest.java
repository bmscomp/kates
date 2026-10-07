package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.Map;

import io.fabric8.kubernetes.api.model.NodeBuilder;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.api.model.authorization.v1.ResourceAttributes;
import io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReview;
import io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.KubernetesChaosProvider;
import com.bmscomp.kates.chaos.StrimziTestCluster;
import com.bmscomp.kates.chaos.VertxPerMockClient;
import com.bmscomp.kates.domain.SlaDefinition;

/**
 * Pins the SCALE_DOWN rollback fix (P0-4). The original guard derived the
 * "restore" target from {@code status/spec.replicas}, which at rollback time
 * already hold the REDUCED count — so it restored to the reduced value (a
 * silent no-op) and the cluster never recovered its brokers. The fix restores
 * from the {@code kates.io/original-replicas} snapshot the provider stamps at
 * scale-down time, then clears it.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class DisruptionSafetyGuardTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private DisruptionSafetyGuard guard;

    @BeforeEach
    void setup() {
        guard = new DisruptionSafetyGuard();
        guard.kubeClient = client;
        guard.limits = new FaultLimits();
        guard.kafkaNamespace = "kafka";
        guard.kafkaLabel = "strimzi.io/component-type=kafka";
    }

    @Test
    void restoreUsesSnapshotAndClearsIt() {
        // A StatefulSet already scaled down to 1, carrying the original-count
        // snapshot (3) that the provider stamped when it scaled down.
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka")
                .withNamespace("default")
                .addToLabels("app", "kafka")
                .addToAnnotations(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, "3")
                .endMetadata()
                .withNewSpec()
                .withReplicas(1)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("default").resource(ss).create();

        FaultSpec spec = FaultSpec.builder("restore")
                .targetNamespace("default")
                .targetLabel("app=kafka")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        guard.restoreReplicaCount(spec);

        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka")
                .get();
        assertEquals(3, after.getSpec().getReplicas(), "restored to the ORIGINAL replica count, not the reduced one");
        assertNull(
                after.getMetadata().getAnnotations().get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION),
                "snapshot cleared so a later scale-down captures a fresh baseline");
    }

    @Test
    void restoreWithoutSnapshotDoesNotInventReplicas() {
        // No snapshot annotation: the guard must not scale a StatefulSet UP to a
        // fabricated count — it can only fall back to the current value.
        StatefulSet ss = new StatefulSetBuilder()
                .withNewMetadata()
                .withName("kafka2")
                .withNamespace("default")
                .addToLabels("app", "kafka2")
                .endMetadata()
                .withNewSpec()
                .withReplicas(1)
                .endSpec()
                .build();
        client.apps().statefulSets().inNamespace("default").resource(ss).create();

        FaultSpec spec = FaultSpec.builder("restore-none")
                .targetNamespace("default")
                .targetLabel("app=kafka2")
                .disruptionType(DisruptionType.SCALE_DOWN)
                .build();

        guard.restoreReplicaCount(spec);

        StatefulSet after = client.apps()
                .statefulSets()
                .inNamespace("default")
                .withName("kafka2")
                .get();
        assertEquals(1, after.getSpec().getReplicas());
    }

    // ── Blast-radius counting for multi-pod targets ─────────────────────────

    /** Two brokers in zone alpha, one each in sigma and gamma, as the chart labels them. */
    private void createZonedBrokers() {
        String[][] brokers = {
            {"krafter-brokers-0", "alpha"}, {"krafter-brokers-1", "alpha"},
            {"krafter-brokers-2", "sigma"}, {"krafter-brokers-3", "gamma"}
        };
        for (String[] b : brokers) {
            client.pods()
                    .inNamespace("kafka")
                    .resource(new PodBuilder()
                            .withNewMetadata()
                            .withName(b[0])
                            .withNamespace("kafka")
                            .addToLabels("strimzi.io/component-type", "kafka")
                            .addToLabels("zone", b[1])
                            .endMetadata()
                            .build())
                    .create();
        }
    }

    private static DisruptionPlan plan(int maxAffectedBrokers, FaultSpec... specs) {
        DisruptionPlan plan = new DisruptionPlan();
        plan.setMaxAffectedBrokers(maxAffectedBrokers);
        int i = 0;
        for (FaultSpec spec : specs) {
            plan.getSteps().add(new DisruptionPlan.DisruptionStep("step-" + i++, spec, 0, 0, false));
        }
        return plan;
    }

    private static FaultSpec zoneKill(String selector) {
        return FaultSpec.builder("az")
                .targetLabel(selector)
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();
    }

    @Test
    void targetAllCountsEveryBrokerOfTheZone() {
        createZonedBrokers();

        var result = guard.validatePlan(plan(1, zoneKill("strimzi.io/component-type=kafka,zone=alpha")));

        // Counted as ONE broker before targetAll existed, so this passed.
        assertFalse(result.safe());
        assertEquals(List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"), result.errors());
        assertTrue(guard.validatePlan(plan(2, zoneKill("strimzi.io/component-type=kafka,zone=alpha")))
                .safe());
    }

    @Test
    void targetAllOverEveryBrokerIsRejected() {
        createZonedBrokers();

        var result = guard.validatePlan(plan(-1, zoneKill("zone in (alpha,sigma,gamma)")));

        assertFalse(result.safe());
        assertTrue(
                result.errors().getFirst().startsWith("Plan would affect ALL 4 brokers"),
                result.errors().toString());
    }

    @Test
    void stepsTargetingTheSameBrokersCountThemOnce() {
        createZonedBrokers();

        FaultSpec killOne = FaultSpec.builder("kill-1")
                .targetBrokerId(1)
                .disruptionType(DisruptionType.POD_KILL)
                .build();
        var result = guard.validatePlan(plan(2, zoneKill("zone=alpha"), killOne));

        assertTrue(result.safe(), result.errors().toString());
    }

    @Test
    void malformedSelectorRejectsThePlan() {
        createZonedBrokers();

        var result = guard.validatePlan(plan(-1, zoneKill("zone in (alpha")));

        assertFalse(result.safe());
        assertTrue(
                result.errors().getFirst().startsWith("Step 'step-0': Invalid label selector"),
                result.errors().toString());
    }

    // ── Chaos limits ────────────────────────────────────────────────────────

    @Test
    void aPartitionWithoutADurationIsRefusedNotWarnedAbout() {
        createZonedBrokers();

        // Only a warning before, and the kubernetes provider then never removed it.
        FaultSpec forever = FaultSpec.builder("split")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(0)
                .build();
        var result = guard.validatePlan(plan(1, forever));

        assertFalse(result.safe());
        assertEquals(
                List.of("Step 'step-0': chaosDurationSec 0 is below 1, and a NETWORK_PARTITION is undone only when"
                        + " its duration ends"),
                result.errors());
        assertTrue(result.warnings().isEmpty(), result.warnings().toString());
    }

    @Test
    void aFaultPastALimitIsRefusedNamingItsStepAndTheSetting() {
        createZonedBrokers();

        FaultSpec stress = FaultSpec.builder("stress")
                .targetBrokerId(2)
                .disruptionType(DisruptionType.CPU_STRESS)
                .cpuCores(4)
                .chaosDurationSec(86_400)
                .build();
        var result = guard.validatePlan(plan(2, kill(0), stress));

        assertEquals(
                List.of("Step 'step-1': chaosDurationSec 86400 is above the limit of 3600"
                        + " (kates.chaos.limits.max-duration-sec)"),
                result.errors());
        assertFalse(guard.dryRun(plan(2, kill(0), stress)).wouldSucceed(), "the dry run says so too");
    }

    @Test
    void faultInAnotherNamespaceAffectsNoBroker() {
        createZonedBrokers();

        FaultSpec consumers = FaultSpec.builder("consumers")
                .targetNamespace("default")
                .targetLabel("strimzi.io/component-type=kafka")
                .targetAll(true)
                .disruptionType(DisruptionType.NETWORK_PARTITION)
                .chaosDurationSec(60)
                .build();

        assertTrue(guard.validatePlan(plan(1, consumers)).safe());
    }

    private static FaultSpec rollingRestart(String selector, int chaosDurationSec) {
        return FaultSpec.builder("roll")
                .targetLabel(selector)
                .disruptionType(DisruptionType.ROLLING_RESTART)
                .chaosDurationSec(chaosDurationSec)
                .build();
    }

    @Test
    void rollingRestartCountsOneBrokerAtATime() {
        createZonedBrokers();

        // The built-in playbook: every broker, maxAffectedBrokers 1.
        var result = guard.validatePlan(plan(1, rollingRestart("strimzi.io/component-type=kafka", 600)));

        assertTrue(result.safe(), result.errors().toString());
    }

    @Test
    void dryRunListsEveryBrokerARollingRestartRestarts() {
        createZonedBrokers();

        var all = guard.dryRun(plan(1, rollingRestart("strimzi.io/component-type=kafka", 600)))
                .steps()
                .getFirst();
        // Used to list one broker (the random or broker-id pick) and then
        // every broker again, whatever the selector.
        assertEquals(
                List.of("krafter-brokers-0", "krafter-brokers-1", "krafter-brokers-2", "krafter-brokers-3"),
                all.affectedPods().stream().sorted().toList());
        assertTrue(all.warnings().isEmpty(), all.warnings().toString());

        var zone =
                guard.dryRun(plan(1, rollingRestart("zone=alpha", 0))).steps().getFirst();
        assertEquals(
                List.of("krafter-brokers-0", "krafter-brokers-1"),
                zone.affectedPods().stream().sorted().toList());
        assertTrue(
                zone.warnings().getFirst().contains("does not wait"),
                zone.warnings().toString());
    }

    @Test
    void dryRunListsTheZoneAndFlagsASelectorThatHitsNoBroker() {
        createZonedBrokers();

        var fixed = guard.dryRun(plan(3, zoneKill("strimzi.io/component-type=kafka,zone=alpha")));
        var step = fixed.steps().getFirst();
        assertEquals(
                List.of("krafter-brokers-0", "krafter-brokers-1"),
                step.affectedPods().stream().sorted().toList());
        assertTrue(step.warnings().isEmpty(), step.warnings().toString());

        // The pre-fix az-failure selector: the dry run used to report a
        // "(random selection)" broker for it; it hits nothing.
        var old = guard.dryRun(plan(3, zoneKill("strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a")));
        var oldStep = old.steps().getFirst();
        assertTrue(oldStep.affectedPods().isEmpty());
        assertNull(oldStep.targetPod());
        assertTrue(
                oldStep.warnings().getFirst().contains("matches no broker pod"),
                oldStep.warnings().toString());
    }

    // ── SCALE_DOWN on Strimzi ───────────────────────────────────────────────

    private StrimziTestCluster strimzi() {
        return new StrimziTestCluster(server, client)
                .pool("controllers", "controller", 0, 1, 2)
                .pool("brokers", "broker", 3, 4, 5)
                .pool("brokers-sigma", "broker", 6);
    }

    private static FaultSpec scaleDown(String selector) {
        return FaultSpec.builder("scale-down")
                .targetLabel(selector)
                .disruptionType(DisruptionType.SCALE_DOWN)
                .chaosDurationSec(300)
                .build();
    }

    @Test
    void dryRunNamesTheBrokerEachNodePoolLoses() {
        strimzi();

        var step = guard.dryRun(plan(-1, scaleDown("strimzi.io/component-type=kafka")))
                .steps()
                .getFirst();

        // Used to list every Kafka pod, controllers included, for any SCALE_DOWN.
        assertEquals(List.of("krafter-brokers-5", "krafter-brokers-sigma-6"), step.affectedPods());
        assertTrue(
                step.warnings().stream().anyMatch(w -> w.contains("controllers runs KRaft controllers")),
                step.warnings().toString());
    }

    @Test
    void validatePlanCountsOneBrokerPerNodePool() {
        strimzi();

        var result = guard.validatePlan(plan(1, scaleDown("strimzi.io/broker-role=true")));

        assertFalse(result.safe());
        assertEquals(List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"), result.errors());
        assertTrue(guard.validatePlan(plan(1, scaleDown("strimzi.io/pool-name=brokers")))
                .safe());
    }

    @Test
    void dryRunWarnsThatTargetPodOnlyPicksTheNodePool() {
        strimzi();
        FaultSpec spec = scaleDown("strimzi.io/component-type=kafka").toBuilder()
                .targetPod("krafter-brokers-3")
                .build();

        var step = guard.dryRun(plan(-1, spec)).steps().getFirst();

        assertEquals(List.of("krafter-brokers-5"), step.affectedPods());
        assertTrue(
                step.warnings().stream().anyMatch(w -> w.contains("not krafter-brokers-3")),
                step.warnings().toString());
    }

    @Test
    void dryRunWarnsThatANodePoolScaleDownWithoutABudgetDoesNotWait() {
        strimzi();
        FaultSpec spec = scaleDown("strimzi.io/pool-name=brokers").toBuilder()
                .chaosDurationSec(0)
                .build();

        var step = guard.dryRun(plan(-1, spec)).steps().getFirst();

        assertTrue(
                step.warnings().stream().anyMatch(w -> w.startsWith("chaosDurationSec is 0")),
                step.warnings().toString());
    }

    @Test
    void restoreGivesANodePoolScaledToZeroItsReplicasBack() {
        StrimziTestCluster cluster = strimzi();
        // brokers-sigma after a SCALE_DOWN from 1 to 0: no pod left for any
        // selector to find it by.
        cluster.scaledDown("brokers-sigma", 0);

        guard.restoreReplicaCount(scaleDown("strimzi.io/pool-name=brokers-sigma"));

        assertEquals(1, cluster.replicas("brokers-sigma"));
        assertTrue(cluster.annotations("brokers-sigma").isEmpty(), "snapshot cleared");
        assertEquals(3, cluster.replicas("brokers"), "a pool without a snapshot is left alone");
    }

    /** Answers every access review with allowed, and returns what each one asked. */
    private List<ResourceAttributes> accessReviews(FaultSpec spec) throws InterruptedException {
        server.expect()
                .post()
                .withPath("/apis/authorization.k8s.io/v1/selfsubjectaccessreviews")
                .andReturn(
                        201,
                        new SelfSubjectAccessReviewBuilder()
                                .withNewStatus()
                                .withAllowed(true)
                                .endStatus()
                                .build())
                .always();

        assertTrue(guard.checkRbacPermissions(spec));

        List<ResourceAttributes> asked = new java.util.ArrayList<>();
        for (int i = server.getRequestCount(); i > 0; i--) {
            var request = server.takeRequest();
            if (request.getPath().endsWith("/selfsubjectaccessreviews")) {
                asked.add(client.getKubernetesSerialization()
                        .unmarshal(request.getUtf8Body(), SelfSubjectAccessReview.class)
                        .getSpec()
                        .getResourceAttributes());
            }
        }
        return asked;
    }

    private static String review(ResourceAttributes a) {
        return a.getVerb() + " " + a.getGroup() + "/" + a.getResource()
                + (a.getSubresource() != null ? "/" + a.getSubresource() : "");
    }

    @Test
    void rbacCheckAsksToPatchTheKafkaNodePool() throws InterruptedException {
        strimzi();

        List<ResourceAttributes> asked = accessReviews(scaleDown("strimzi.io/pool-name=brokers"));

        // Used to ask about updating StatefulSets, which Strimzi does not create.
        assertEquals(
                List.of("patch kafka.strimzi.io/kafkanodepools"),
                asked.stream().map(DisruptionSafetyGuardTest::review).toList());
        assertEquals("kafka", asked.getFirst().getNamespace());
    }

    @Test
    void rbacCheckOnAStatefulSetAsksToPatchItAndSetItsScale() throws InterruptedException {
        client.pods()
                .inNamespace("kafka")
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName("kafka-0")
                        .withNamespace("kafka")
                        .addToLabels("app", "kafka")
                        .addNewOwnerReference()
                        .withApiVersion("apps/v1")
                        .withKind("StatefulSet")
                        .withName("kafka")
                        .withUid("sts-uid")
                        .endOwnerReference()
                        .endMetadata()
                        .build())
                .create();

        List<ResourceAttributes> asked = accessReviews(scaleDown("app=kafka"));

        assertEquals(
                List.of("patch apps/statefulsets", "update apps/statefulsets/scale"),
                asked.stream().map(DisruptionSafetyGuardTest::review).toList());
    }

    // ── KRaft controllers are not brokers ───────────────────────────────────

    /**
     * The default Kind cluster: brokers are nodes 0–2 and dedicated KRaft
     * controllers 3–5. strimzi.io/component-type=kafka matches all six.
     */
    private StrimziTestCluster threeBrokersThreeControllers() {
        return new StrimziTestCluster(server, client)
                .pool("brokers-alpha", "broker", 0)
                .pool("brokers-gamma", "broker", 1)
                .pool("brokers-sigma", "broker", 2)
                .pool("controllers-alpha", "controller", 3)
                .pool("controllers-gamma", "controller", 4)
                .pool("controllers-sigma", "controller", 5);
    }

    private static final List<String> THREE_BROKERS =
            List.of("krafter-brokers-alpha-0", "krafter-brokers-gamma-1", "krafter-brokers-sigma-2");

    private static FaultSpec killAll(String selector) {
        return FaultSpec.builder("kill-all")
                .targetLabel(selector)
                .targetAll(true)
                .disruptionType(DisruptionType.POD_KILL)
                .build();
    }

    private static FaultSpec kill(int brokerId) {
        return FaultSpec.builder("kill-" + brokerId)
                .targetBrokerId(brokerId)
                .disruptionType(DisruptionType.POD_KILL)
                .build();
    }

    @Test
    void aPlanThatTargetsAllThreeBrokersIsRejected() {
        threeBrokersThreeControllers();

        // Both passed: the controllers made it 3 brokers down of 6.
        var byLabel = guard.validatePlan(plan(-1, killAll("strimzi.io/broker-role=true")));
        var byId = guard.validatePlan(plan(-1, kill(0), kill(1), kill(2)));

        for (var result : List.of(byLabel, byId)) {
            assertFalse(result.safe());
            assertEquals(List.of("Plan would affect ALL 3 brokers — cluster would lose availability"), result.errors());
        }
    }

    @Test
    void blastRadiusCountsThreeBrokersNotSix() {
        threeBrokersThreeControllers();

        assertEquals(3, guard.dryRun(plan(1, kill(1))).totalBrokers());
        assertTrue(guard.validatePlan(plan(1, kill(1))).safe());
        // Never fired: two of six left four.
        assertEquals(
                List.of("Only 1 broker would remain after disruption — high risk of data loss"),
                guard.validatePlan(plan(-1, kill(0), kill(1))).warnings());
    }

    @Test
    void targetBrokerIdResolvesToABrokerNeverAController() {
        threeBrokersThreeControllers();

        var broker = guard.dryRun(plan(1, kill(1))).steps().getFirst();
        assertEquals("krafter-brokers-gamma-1", broker.targetPod());
        assertTrue(broker.warnings().isEmpty(), broker.warnings().toString());

        // Node 3 is a controller, and used to be the target.
        var controller = guard.dryRun(plan(1, kill(3))).steps().getFirst();
        assertTrue(THREE_BROKERS.contains(controller.targetPod()), controller.targetPod());
        assertTrue(
                controller.warnings().stream().anyMatch(w -> w.startsWith("targetBrokerId 3 matches no broker pod")),
                controller.warnings().toString());
    }

    @Test
    void controllersAStepHitsAreListedButNotCounted() {
        threeBrokersThreeControllers();
        // What az-failure kills in zone alpha: a broker and a controller.
        FaultSpec zoneAlpha = killAll("strimzi.io/pool-name in (brokers-alpha,controllers-alpha)");

        assertTrue(guard.validatePlan(plan(1, zoneAlpha)).safe());
        assertEquals(
                List.of("krafter-brokers-alpha-0", "krafter-controllers-alpha-3"),
                guard.dryRun(plan(1, zoneAlpha)).steps().getFirst().affectedPods().stream()
                        .sorted()
                        .toList());
    }

    @Test
    void aNodeWithBothRolesCountsAsABroker() {
        new StrimziTestCluster(server, client).pool("dual-role", "broker,controller", 0, 1, 2);

        var result = guard.validatePlan(plan(-1, killAll("strimzi.io/controller-role=true")));

        assertFalse(result.safe());
        assertEquals(List.of("Plan would affect ALL 3 brokers — cluster would lose availability"), result.errors());
    }

    @Test
    void anOverriddenKafkaLabelCountsOnlyItsBrokers() {
        threeBrokersThreeControllers();
        guard.kafkaLabel = "strimzi.io/cluster=krafter";

        assertEquals(3, guard.dryRun(plan(-1, kill(1))).totalBrokers());
        assertFalse(guard.validatePlan(plan(-1, killAll("strimzi.io/broker-role=true")))
                .safe());
    }

    // ── Leader-aware steps ──────────────────────────────────────────────────

    private static FaultSpec leaderKill(int partition) {
        return FaultSpec.builder("leader-" + partition)
                .targetTopic("orders")
                .targetPartition(partition)
                .disruptionType(DisruptionType.POD_KILL)
                .build();
    }

    private void leaders(int... leaderOfPartition) {
        guard.intelligence = mock(KafkaIntelligenceService.class);
        for (int p = 0; p < leaderOfPartition.length; p++) {
            when(guard.intelligence.resolveLeaderBrokerId("orders", p)).thenReturn(leaderOfPartition[p]);
        }
    }

    @Test
    void dryRunPreviewsTheLeadersPod() {
        createZonedBrokers();
        leaders(2);

        var step = guard.dryRun(plan(1, leaderKill(0))).steps().getFirst();

        // It previewed the pod for the spec's own targetBrokerId, here a
        // random pick, while resolvedLeaderId named the leader.
        assertEquals(2, step.resolvedLeaderId());
        assertEquals("krafter-brokers-2", step.targetPod());
        assertEquals(List.of("krafter-brokers-2"), step.affectedPods());
    }

    @Test
    void blastRadiusCountsEachStepsLeader() {
        createZonedBrokers();
        leaders(1, 3);

        var result = guard.validatePlan(plan(1, leaderKill(0), leaderKill(1)));

        // Both steps counted as the same random pick, so this passed.
        assertFalse(result.safe());
        assertEquals(List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"), result.errors());
    }

    @Test
    void aFailedLeaderLookupKeepsTheSpecAsPosted() {
        createZonedBrokers();
        leaders(-1);

        var step = guard.dryRun(
                        plan(1, leaderKill(0).toBuilder().targetBrokerId(3).build()))
                .steps()
                .getFirst();

        // What the orchestrator runs when its own lookup fails.
        assertNull(step.resolvedLeaderId());
        assertEquals("krafter-brokers-3", step.targetPod());
        assertTrue(
                step.warnings().contains("Could not resolve leader for orders-0"),
                step.warnings().toString());
    }

    @Test
    void slaGatesAPlanCannotEvaluateAreFlaggedBeforeAnyFault() {
        createZonedBrokers();
        SlaDefinition sla = new SlaDefinition();
        sla.setMaxDataLossPercent(0.0);
        sla.setMaxRpoMs(0L);
        sla.setMaxP99LatencyMs(100.0);
        DisruptionPlan plan = plan(-1);
        plan.setSla(sla);

        DisruptionSafetyGuard.ValidationResult result = guard.validatePlan(plan);

        // A warning, not a rejection: plans carrying these fields ran before.
        assertTrue(result.safe());
        List<String> slaWarnings =
                result.warnings().stream().filter(w -> w.startsWith("SLA ")).toList();
        assertEquals(2, slaWarnings.size(), "p99 is evaluable, the other two are not: " + slaWarnings);
        assertTrue(slaWarnings.get(0).contains("maxDataLossPercent"), slaWarnings.get(0));
        assertTrue(slaWarnings.get(1).contains("maxRpoMs"), slaWarnings.get(1));
    }

    // ── NODE_DRAIN counts every broker on the node it drains ────────────────

    /**
     * Brokers 0 and 1 and controller 4 on alpha, broker 2 and controller 5 on
     * sigma, broker 3 on gamma, and a consumer in namespace kates on sigma.
     * Each node carries its zone label, as on the Kind lab, and sigma and
     * gamma a drainable label. The mock API server ignores set-based
     * selectors, so the tests list by equality.
     */
    private void createKafkaOnNodes() {
        for (String node : List.of("alpha", "sigma", "gamma")) {
            client.nodes()
                    .resource(new NodeBuilder()
                            .withNewMetadata()
                            .withName(node)
                            .addToLabels("topology.kubernetes.io/zone", node)
                            .addToLabels("drainable", String.valueOf(!node.equals("alpha")))
                            .endMetadata()
                            .build())
                    .create();
        }
        String[][] pods = {
            {"krafter-brokers-0", "alpha", "true"},
            {"krafter-brokers-1", "alpha", "true"},
            {"krafter-brokers-2", "sigma", "true"},
            {"krafter-brokers-3", "gamma", "true"},
            {"krafter-controllers-4", "alpha", "false"},
            {"krafter-controllers-5", "sigma", "false"}
        };
        for (String[] p : pods) {
            client.pods()
                    .inNamespace("kafka")
                    .resource(new PodBuilder()
                            .withNewMetadata()
                            .withName(p[0])
                            .withNamespace("kafka")
                            .addToLabels("strimzi.io/component-type", "kafka")
                            .addToLabels("strimzi.io/broker-role", p[2])
                            .addToLabels("zone", p[1])
                            .endMetadata()
                            .withNewSpec()
                            .withNodeName(p[1])
                            .endSpec()
                            .build())
                    .create();
        }
        client.pods()
                .inNamespace("kates")
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName("consumer-0")
                        .withNamespace("kates")
                        .addToLabels("app", "consumer")
                        .endMetadata()
                        .withNewSpec()
                        .withNodeName("sigma")
                        .endSpec()
                        .build())
                .create();
    }

    private static FaultSpec.Builder drain() {
        return FaultSpec.builder("drain").disruptionType(DisruptionType.NODE_DRAIN);
    }

    @Test
    void aDrainCountsEveryBrokerOnTheNodeItDrains() {
        createKafkaOnNodes();
        FaultSpec drainBroker0 = drain().targetBrokerId(0).build();

        // Counted broker 0 alone, though draining alpha evicts broker 1 too.
        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, drainBroker0)).errors());
        assertTrue(guard.validatePlan(plan(2, drainBroker0)).safe());
    }

    @Test
    void theDryRunListsEveryKafkaPodOnTheDrainedNodeAndNamesIt() {
        createKafkaOnNodes();

        var step = guard.dryRun(plan(-1, drain().targetBrokerId(2).build()))
                .steps()
                .getFirst();

        assertEquals("krafter-brokers-2", step.targetPod());
        assertEquals(List.of("krafter-brokers-2", "krafter-controllers-5"), step.affectedPods());
        assertEquals(
                List.of("NODE_DRAIN drains node sigma, the node of krafter-brokers-2, and evicts every pod on it"),
                step.warnings());
    }

    @Test
    void aRandomDrainCountsTheNodeThatRunsTheMostBrokers() {
        createKafkaOnNodes();
        FaultSpec random = drain().build();

        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, random)).errors());
        var step = guard.dryRun(plan(2, random)).steps().getFirst();
        assertNull(step.targetPod());
        assertEquals(List.of("krafter-brokers-0", "krafter-brokers-1", "krafter-controllers-4"), step.affectedPods());
        assertEquals(
                List.of("NODE_DRAIN drains the node of a pod picked at random; the count takes the worst case, node"
                        + " alpha, which runs 2 brokers"),
                step.warnings());
    }

    @Test
    void aTargetAllDrainOverSeveralNodesIsRefused() {
        createKafkaOnNodes();
        // The two controllers, on alpha and sigma.
        FaultSpec controllers = drain().targetLabel("strimzi.io/broker-role=false")
                .targetAll(true)
                .build();
        String refusal = "NODE_DRAIN with targetAll picks pods on 2 nodes (alpha, sigma), and node-drain drains one."
                + " Narrow targetLabel to the pods of one node, or name the node in envOverrides.TARGET_NODE";

        // Passed, and its step then failed without draining.
        assertEquals(
                List.of("Step 'step-0': " + refusal),
                guard.validatePlan(plan(-1, controllers)).errors());
        var dryRun = guard.dryRun(plan(-1, controllers));
        assertFalse(dryRun.wouldSucceed());
        assertEquals(List.of(refusal), dryRun.steps().getFirst().warnings());
    }

    @Test
    void aTargetAllDrainOfOneNodesPodsCountsItsBrokers() {
        createKafkaOnNodes();
        FaultSpec zone = drain().targetLabel("zone=alpha").targetAll(true).build();

        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, zone)).errors());
    }

    @Test
    void aDrainAimedAtAPodInAnotherNamespaceCountsTheBrokersOnItsNode() {
        createKafkaOnNodes();
        FaultSpec consumers =
                drain().targetNamespace("kates").targetLabel("app=consumer").build();

        // The consumer runs on sigma, with broker 2: with broker 0 that is two.
        // A step in another namespace counted no broker.
        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, consumers, kill(0))).errors());
        assertEquals(
                List.of("krafter-brokers-2", "krafter-controllers-5"),
                guard.dryRun(plan(-1, consumers)).steps().getFirst().affectedPods());
    }

    @Test
    void aTargetNodeOverrideCountsThatNode() {
        createKafkaOnNodes();
        FaultSpec gamma = drain().targetBrokerId(0)
                .envOverrides(Map.of("TARGET_NODE", "gamma"))
                .build();

        var step = guard.dryRun(plan(1, gamma)).steps().getFirst();

        assertTrue(guard.validatePlan(plan(1, gamma, kill(3))).safe(), "both hit broker 3, on gamma");
        assertEquals(List.of("krafter-brokers-3"), step.affectedPods());
        assertEquals(
                List.of(
                        "NODE_DRAIN drains node gamma, which envOverrides.TARGET_NODE names, and evicts every pod on it"),
                step.warnings());
    }

    @Test
    void aNodeLabelOverrideCountsTheWorstNodeItMatches() {
        createKafkaOnNodes();
        // sigma and gamma, one broker each; alpha, with two, is not among them.
        FaultSpec byLabel =
                drain().envOverrides(Map.of("NODE_LABEL", "drainable=true")).build();

        assertTrue(guard.validatePlan(plan(1, byLabel)).safe());
        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, byLabel, kill(0))).errors());
        assertEquals(
                List.of("NODE_DRAIN drains a node Litmus picks by envOverrides.NODE_LABEL 'drainable=true'; the count"
                        + " takes the worst case, node gamma, which runs 1 broker"),
                guard.dryRun(plan(-1, byLabel)).steps().getFirst().warnings());
    }

    @Test
    void aDrainWhosePodIsOnNoNodeCountsNoneAndSaysSo() {
        createKafkaOnNodes();
        client.pods()
                .inNamespace("kafka")
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName("krafter-brokers-6")
                        .withNamespace("kafka")
                        .addToLabels("strimzi.io/component-type", "kafka")
                        .addToLabels("strimzi.io/broker-role", "true")
                        .endMetadata()
                        .build())
                .create();
        FaultSpec pending = drain().targetPod("krafter-brokers-6").build();

        var step = guard.dryRun(plan(-1, pending)).steps().getFirst();

        assertTrue(guard.validatePlan(plan(1, pending)).safe());
        assertEquals("krafter-brokers-6", step.targetPod());
        assertTrue(step.affectedPods().isEmpty());
        assertEquals(
                List.of("pod krafter-brokers-6 is not on a node yet, and the step fails without draining if it still"
                        + " isn't when the step runs"),
                step.warnings());
    }

    @Test
    void aStepWithoutATypeThatNamesNodeDrainCountsAsADrain() {
        createKafkaOnNodes();
        FaultSpec typeless = FaultSpec.builder("node-drain").targetBrokerId(0).build();

        assertEquals(
                List.of("Plan would affect 2 brokers but maxAffectedBrokers=1"),
                guard.validatePlan(plan(1, typeless)).errors());
    }
}
