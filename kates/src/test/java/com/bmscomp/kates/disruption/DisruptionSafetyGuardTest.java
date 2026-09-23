package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;

import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.KubernetesChaosProvider;
import com.bmscomp.kates.domain.SlaDefinition;

/**
 * Pins the SCALE_DOWN rollback fix (P0-4). The original guard derived the
 * "restore" target from {@code status/spec.replicas}, which at rollback time
 * already hold the REDUCED count — so it restored to the reduced value (a
 * silent no-op) and the cluster never recovered its brokers. The fix restores
 * from the {@code kates.io/original-replicas} snapshot the provider stamps at
 * scale-down time, then clears it.
 */
@EnableKubernetesMockClient(crud = true)
class DisruptionSafetyGuardTest {

    KubernetesMockServer server;
    KubernetesClient client;

    private DisruptionSafetyGuard guard;

    @BeforeEach
    void setup() {
        guard = new DisruptionSafetyGuard();
        guard.kubeClient = client;
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
}
