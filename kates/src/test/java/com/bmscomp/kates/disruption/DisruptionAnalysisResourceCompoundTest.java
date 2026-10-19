package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.*;

import java.util.ArrayList;
import java.util.List;
import jakarta.ws.rs.core.Response;

import io.fabric8.kubernetes.client.KubernetesClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.chaos.CompoundChaosOrchestrator;
import com.bmscomp.kates.chaos.CompoundChaosOrchestrator.CompoundOutcome;
import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultLimits;
import com.bmscomp.kates.chaos.FaultSpec;

/**
 * A compound run goes through the safety guard and takes the lease a plan
 * takes. It called the providers past both: a POD_KILL with targetAll took
 * down every broker at once, which the same fault as a plan step is refused
 * for, and it ran on top of a plan's faults.
 */
class DisruptionAnalysisResourceCompoundTest {

    private final DisruptionAnalysisResource resource = new DisruptionAnalysisResource();

    private final DisruptionConcurrencyGuard guard = new DisruptionConcurrencyGuard();

    private static final FaultSpec EVERY_BROKER = FaultSpec.builder("kill")
            .disruptionType(DisruptionType.POD_KILL)
            .targetAll(true)
            .build();

    @BeforeEach
    void wire() {
        guard.kafkaNamespace = "kafka";
        guard.kafkaCluster = "krafter";
        resource.concurrencyGuard = guard;
        resource.compoundOrchestrator = mock(CompoundChaosOrchestrator.class);
        resource.safetyGuard = mock(DisruptionSafetyGuard.class);
        when(resource.safetyGuard.validatePlan(any())).thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of()));
        when(resource.compoundOrchestrator.executeConcurrent(any(), anyInt()))
                .thenReturn(new CompoundOutcome(true, List.of()));
    }

    private static DisruptionDtos.CompoundChaosRequest request(FaultSpec... specs) {
        DisruptionDtos.CompoundChaosRequest request = new DisruptionDtos.CompoundChaosRequest();
        request.faults = new ArrayList<>();
        for (FaultSpec spec : specs) {
            DisruptionDtos.CompoundFaultEntry entry = new DisruptionDtos.CompoundFaultEntry();
            entry.faultSpec = spec;
            request.faults.add(entry);
        }
        return request;
    }

    private void assertNoFault() {
        verify(resource.compoundOrchestrator, never()).executeConcurrent(any(), anyInt());
        verify(resource.compoundOrchestrator, never()).executeSequential(any(), anyInt());
    }

    @Test
    void anUnsafeCompoundRunIsRefusedBeforeAnyFault() {
        when(resource.safetyGuard.validatePlan(any()))
                .thenReturn(DisruptionSafetyGuard.ValidationResult.rejected(
                        List.of(), List.of("Plan would affect ALL 3 brokers — cluster would lose availability")));

        Response response = resource.executeCompound(request(EVERY_BROKER));

        assertEquals(400, response.getStatus());
        assertEquals(
                "No fault was triggered: Plan would affect ALL 3 brokers — cluster would lose availability",
                ((ApiError) response.getEntity()).getMessage());
        assertNoFault();
    }

    /**
     * The guard refuses a fault outside the Kafka namespace and the Kates
     * API's own in either mode, and says so even when it finds no broker.
     */
    @Test
    void aFaultInAnyOtherNamespaceIsRefusedBeforeAnyFault() {
        DisruptionSafetyGuard safetyGuard = new DisruptionSafetyGuard();
        safetyGuard.kubeClient = mock(KubernetesClient.class);
        when(safetyGuard.kubeClient.getNamespace()).thenReturn("kates");
        safetyGuard.limits = new FaultLimits();
        safetyGuard.kafkaNamespace = "kafka";
        safetyGuard.kafkaLabel = "strimzi.io/component-type=kafka";
        resource.safetyGuard = safetyGuard;
        FaultSpec dns = FaultSpec.builder("dns")
                .targetNamespace("kube-system")
                .targetLabel("k8s-app=kube-dns")
                .targetAll(true)
                .disruptionType(DisruptionType.POD_DELETE)
                .build();

        for (boolean sequential : new boolean[] {true, false}) {
            DisruptionDtos.CompoundChaosRequest request = request(dns);
            request.sequential = sequential;
            Response response = resource.executeCompound(request);

            assertEquals(400, response.getStatus());
            assertEquals(
                    "No fault was triggered: Step 'faults[0]': targetNamespace kube-system is neither kafka, the Kafka"
                            + " cluster's namespace (kates.chaos.kafka.namespace), nor the Kates API's own, kates; No"
                            + " broker pods found matching label 'strimzi.io/component-type=kafka' in namespace 'kafka'",
                    ((ApiError) response.getEntity()).getMessage());
        }
        assertNoFault();
    }

    @Test
    void aCompoundRunWhileTheLeaseIsHeldIsAConflict() {
        assertTrue(guard.tryAcquire(guard.currentTarget(), "leader-cascade#1"));

        Response response = resource.executeCompound(request(EVERY_BROKER));

        assertEquals(409, response.getStatus());
        assertEquals(
                "Another disruption is running against kafka/krafter, so no fault was triggered",
                ((ApiError) response.getEntity()).getMessage());
        assertNoFault();
        assertEquals(
                "leader-cascade#1", guard.activeToken(guard.currentTarget()).orElseThrow());
    }

    @Test
    void theLeaseIsHeldWhileTheFaultsRunAndReleasedAfter() {
        List<Boolean> busy = new ArrayList<>();
        when(resource.compoundOrchestrator.executeConcurrent(any(), anyInt())).thenAnswer(invocation -> {
            busy.add(guard.isBusy(guard.currentTarget()));
            return new CompoundOutcome(true, List.of());
        });

        assertEquals(200, resource.executeCompound(request(EVERY_BROKER)).getStatus());
        assertEquals(List.of(true), busy);
        assertFalse(guard.isBusy(guard.currentTarget()), "released after a run that answered");

        doThrow(new IllegalStateException("the provider broke"))
                .when(resource.compoundOrchestrator)
                .executeConcurrent(any(), anyInt());

        assertThrows(IllegalStateException.class, () -> resource.executeCompound(request(EVERY_BROKER)));
        assertFalse(guard.isBusy(guard.currentTarget()), "released after a run that threw");
    }

    /**
     * The guard replaces a step's targetBrokerId with the leader of its
     * targetTopic, but a compound fault is never aimed by topic, so it would
     * count the wrong broker.
     */
    @Test
    void eachFaultIsAStepCountedWithoutItsTargetTopic() {
        FaultSpec byTopic = FaultSpec.builder("kill-1")
                .disruptionType(DisruptionType.POD_KILL)
                .targetBrokerId(1)
                .targetTopic("orders")
                .build();

        resource.executeCompound(request(EVERY_BROKER, byTopic));

        ArgumentCaptor<DisruptionPlan> plan = ArgumentCaptor.forClass(DisruptionPlan.class);
        verify(resource.safetyGuard).validatePlan(plan.capture());
        List<DisruptionPlan.DisruptionStep> steps = plan.getValue().getSteps();
        assertEquals(
                List.of("faults[0]", "faults[1]"),
                steps.stream().map(DisruptionPlan.DisruptionStep::name).toList());
        assertEquals("", steps.get(1).faultSpec().targetTopic());
        assertEquals(byTopic.toBuilder().targetTopic("").build(), steps.get(1).faultSpec());
        assertEquals(EVERY_BROKER, steps.get(0).faultSpec());
    }
}
