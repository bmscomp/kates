package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.function.LongConsumer;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.api.model.EphemeralContainer;
import io.fabric8.kubernetes.api.model.EphemeralContainerBuilder;
import io.fabric8.kubernetes.api.model.OwnerReference;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.api.model.networking.v1.*;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientException;
import io.fabric8.kubernetes.client.readiness.Readiness;
import org.eclipse.microprofile.faulttolerance.Retry;
import org.eclipse.microprofile.faulttolerance.Timeout;
import org.jboss.logging.Logger;

/**
 * Chaos provider using direct Kubernetes API calls.
 * Supports pod deletion, network policy injection, Strimzi rolling updates,
 * KafkaNodePool scaling and StatefulSet manipulation without requiring
 * external chaos infrastructure like Litmus.
 */
@ApplicationScoped
@Named("kubernetes")
public class KubernetesChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(KubernetesChaosProvider.class);

    /**
     * Annotation stamped on a KafkaNodePool or StatefulSet the first time
     * SCALE_DOWN reduces it, recording the ORIGINAL replica count. Rollback
     * restores from this value — reading {@code spec.replicas} at rollback time
     * only ever sees the already reduced count, so without the snapshot the
     * restore is a silent no-op.
     */
    public static final String ORIGINAL_REPLICAS_ANNOTATION = "kates.io/original-replicas";

    /**
     * Epoch millis of the scale-down that wrote {@link #ORIGINAL_REPLICAS_ANNOTATION}.
     * Startup orphan recovery uses this to tell a fault abandoned by a dead pod
     * from one another replica is still running.
     */
    public static final String SCALED_DOWN_AT_ANNOTATION = "kates.io/scaled-down-at";

    /**
     * Pod annotation the Strimzi Cluster Operator acts on at its next
     * reconciliation: it restarts the pod through its rolling update. The new
     * pod does not carry it.
     */
    public static final String MANUAL_ROLLING_UPDATE_ANNOTATION = "strimzi.io/manual-rolling-update";

    private static final String STRIMZI_POD_SET = "StrimziPodSet";

    /** How often ROLLING_RESTART checks whether the roll has finished. */
    long rollPollIntervalMs = 5_000;

    /** A roll that did not finish within {@code chaosDurationSec}. */
    public static class IncompleteRollException extends RuntimeException {
        public IncompleteRollException(String message) {
            super(message);
        }
    }

    /** How often SCALE_DOWN checks whether the Cluster Operator has removed the brokers. */
    long scalePollIntervalMs = 5_000;

    /**
     * A scale-down the Strimzi Cluster Operator held back or did not finish
     * within {@code chaosDurationSec}; the node pools have their replicas back.
     */
    public static class IncompleteScaleDownException extends RuntimeException {
        public IncompleteScaleDownException(String message) {
            super(message);
        }
    }

    @Inject
    KubernetesClient client;

    @Inject
    KubernetesChaosProvider self;

    @Inject
    com.bmscomp.kates.engine.KatesExecutor executor;

    @Override
    public String name() {
        return "kubernetes";
    }

    /**
     * Injects the fault, once. Nothing here is retried, as a whole or in part:
     * every step changes the cluster, and a call the API server carried out
     * but whose answer the client lost looks like a failure. Retried, the
     * injection ran again on what the first attempt had changed: another pod
     * picked at random and killed, a StatefulSet scaled down from the count the
     * first attempt left. A failure fails the fault instead. Only removing a
     * partition is retried ({@link #cleanup}), and its failure does not inject
     * the partition again.
     *
     * <p>No circuit breaker either. One breaker served every fault in the
     * application, so a plan's failed faults refused the next plan's with an
     * error that said nothing about it, and a few faults per plan are no load
     * to shed from the API server.
     */
    private void applyDisruption(FaultSpec spec, String engineName) throws Exception {
        switch (spec.disruptionType()) {
            case POD_KILL -> executePodKill(spec);
            case POD_DELETE -> executePodDelete(spec);
            case NETWORK_PARTITION -> executeNetworkPartition(spec, engineName);
            case ROLLING_RESTART -> executeRollingRestart(spec);
            case SCALE_DOWN -> executeScaleDown(spec);
            case LEADER_ELECTION -> executePodKill(spec);
            case CPU_STRESS -> executeCpuStress(spec);
            case IO_STRESS -> executeIoStress(spec);
            default ->
                throw new UnsupportedOperationException(
                        "DisruptionType " + spec.disruptionType() + " not supported by kubernetes provider");
        }
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return triggerFault(spec, injectedAt -> {});
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec, LongConsumer onInject) {
        return CompletableFuture.supplyAsync(
                () -> {
                    Instant start = Instant.now();
                    long startNanos = System.nanoTime();
                    String engineName = spec.experimentName() + "-" + System.currentTimeMillis();

                    try {
                        if (spec.disruptionType() == null) {
                            throw new IllegalArgumentException("No disruptionType set — use the builder");
                        }
                        if (spec.delayBeforeSec() > 0) {
                            Thread.sleep(spec.delayBeforeSec() * 1000L);
                            // Taken again once the delay is over, so the outcome
                            // times the fault and not the wait before it.
                            start = Instant.now();
                            startNanos = System.nanoTime();
                        }
                        // The fault goes in from here, once its delay is over.
                        onInject.accept(startNanos);
                        applyDisruption(spec, engineName);
                        return ChaosOutcome.success(
                                engineName, spec.experimentName(), start, Instant.now(), startNanos, null, null, null);

                    } catch (InterruptedException e) {
                        Thread.currentThread().interrupt();
                        return ChaosOutcome.failure(
                                engineName,
                                spec.experimentName(),
                                start,
                                Instant.now(),
                                startNanos,
                                "Interrupted",
                                null,
                                null,
                                null);
                    } catch (Exception e) {
                        LOG.error("Fault injection failed", e);
                        return ChaosOutcome.failure(
                                engineName,
                                spec.experimentName(),
                                start,
                                Instant.now(),
                                startNanos,
                                e.getMessage(),
                                null,
                                null,
                                null);
                    }
                },
                executor.get());
    }

    private void executePodKill(FaultSpec spec) {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("POD_KILL: force-deleting " + podName);
            client.pods()
                    .inNamespace(spec.targetNamespace())
                    .withName(podName)
                    .withGracePeriod(0)
                    .delete();
        }
    }

    private void executePodDelete(FaultSpec spec) {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("POD_DELETE: gracefully deleting " + podName + " (grace=" + spec.gracePeriodSec() + "s)");
            client.pods()
                    .inNamespace(spec.targetNamespace())
                    .withName(podName)
                    .withGracePeriod(spec.gracePeriodSec())
                    .delete();
        }
    }

    /**
     * Isolates the target pods for {@code chaosDurationSec}, then removes the
     * partition, whatever the duration, and also when isolating a later pod
     * failed or the wait was interrupted: a NetworkPolicy left behind cuts the
     * pod off until rollback, which a failed fault does not get.
     */
    private void executeNetworkPartition(FaultSpec spec, String engineName) throws InterruptedException {
        Exception failure = null;
        try {
            for (String podName : PodTargets.resolve(client, spec)) {
                isolatePod(spec, podName);
            }
            // The chaos limits refuse a partition without a duration; one that
            // gets here anyway is a blip.
            Thread.sleep(Math.max(0, spec.chaosDurationSec()) * 1000L);
        } catch (RuntimeException | InterruptedException e) {
            failure = e;
            throw e;
        } finally {
            removePartition(spec.targetNamespace(), engineName, failure);
        }
    }

    /**
     * Removes every Kates NetworkPolicy with {@link #cleanup}, which its own
     * {@code @Retry} retries. When that fails as well, the fault fails saying
     * the pods may still be cut off, and the failure of the partition itself,
     * if any, goes along as suppressed. A failed fault gets no rollback, so the
     * message says how to remove them.
     */
    private void removePartition(String namespace, String engineName, Exception failure) {
        try {
            self.cleanup(engineName);
        } catch (RuntimeException e) {
            IllegalStateException notRemoved = new IllegalStateException(
                    "NETWORK_PARTITION: could not remove the NetworkPolicies, so the pods may still be cut off."
                            + " Remove them with kubectl delete networkpolicy -n " + namespace
                            + " -l managed-by=kates. " + e.getMessage(),
                    e);
            if (failure != null) {
                notRemoved.addSuppressed(failure);
            }
            if (failure instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw notRemoved;
        }
    }

    private void isolatePod(FaultSpec spec, String podName) {
        String policyName = "kates-netpol-" + podName;

        LOG.info("NETWORK_PARTITION: applying deny NetworkPolicy for " + podName);

        var pod = client.pods()
                .inNamespace(spec.targetNamespace())
                .withName(podName)
                .get();
        if (pod == null) {
            throw new IllegalStateException("Pod not found: " + podName);
        }

        Map<String, String> podLabels = pod.getMetadata().getLabels();

        NetworkPolicy policy = new NetworkPolicyBuilder()
                .withNewMetadata()
                .withName(policyName)
                .withNamespace(spec.targetNamespace())
                .addToLabels("managed-by", "kates")
                .endMetadata()
                .withNewSpec()
                .withNewPodSelector()
                .addToMatchLabels(podLabels)
                .endPodSelector()
                .withPolicyTypes("Ingress", "Egress")
                .endSpec()
                .build();

        client.network()
                .networkPolicies()
                .inNamespace(spec.targetNamespace())
                .resource(policy)
                .create();
    }

    /**
     * Restarts every pod {@code targetLabel} matches (or just {@code targetPod})
     * one at a time, then waits for the roll to finish so the observation
     * window starts after it.
     *
     * <p>Strimzi runs Kafka pods from StrimziPodSets, not StatefulSets. Their
     * pods are annotated for the Cluster Operator, which rolls them at its next
     * reconciliation with its own checks: one pod at a time, each ready again
     * before the next, and none whose restart would leave a partition under
     * min.insync.replicas. The pods are annotated rather than their
     * StrimziPodSets because selectors such as {@code zone=alpha} match pod
     * labels only. Pods of a StatefulSet (Kafka not run by Strimzi) are rolled
     * by restarting it.
     */
    private void executeRollingRestart(FaultSpec spec) throws InterruptedException {
        String namespace = spec.targetNamespace();
        Map<String, String> uids = new LinkedHashMap<>();
        Set<String> statefulSets = new LinkedHashSet<>();

        for (String podName : PodTargets.resolve(client, spec)) {
            Pod pod = client.pods().inNamespace(namespace).withName(podName).get();
            if (pod == null) {
                throw new IllegalStateException("Pod not found: " + podName);
            }
            OwnerReference owner = pod.getMetadata().getOwnerReferences().stream()
                    .filter(o -> STRIMZI_POD_SET.equals(o.getKind()) || "StatefulSet".equals(o.getKind()))
                    .findFirst()
                    .orElse(null);
            if (owner == null) {
                LOG.info("ROLLING_RESTART: skipping " + podName + ", not run by a StrimziPodSet or a StatefulSet");
                continue;
            }
            if (STRIMZI_POD_SET.equals(owner.getKind())) {
                LOG.info("ROLLING_RESTART: annotating " + podName + " for the Strimzi Cluster Operator to roll");
                client.pods()
                        .inNamespace(namespace)
                        .withName(podName)
                        .edit(p -> new PodBuilder(p)
                                .editMetadata()
                                .addToAnnotations(MANUAL_ROLLING_UPDATE_ANNOTATION, "true")
                                .endMetadata()
                                .build());
            } else {
                statefulSets.add(owner.getName());
            }
            uids.put(podName, pod.getMetadata().getUid());
        }

        if (uids.isEmpty()) {
            throw new IllegalStateException("ROLLING_RESTART: no pod matching '" + spec.targetLabel()
                    + "' in namespace '" + namespace + "' is run by a StrimziPodSet or a StatefulSet");
        }
        for (String name : statefulSets) {
            LOG.info("ROLLING_RESTART: restarting StatefulSet " + name);
            client.apps()
                    .statefulSets()
                    .inNamespace(namespace)
                    .withName(name)
                    .rolling()
                    .restart();
        }

        awaitRoll(spec, uids);
    }

    /**
     * Waits up to {@code chaosDurationSec} for every pod in {@code uids} to be
     * replaced (same name, new UID) and ready. The Cluster Operator starts the
     * roll at its next reconciliation, every two minutes by default, so the
     * budget has to cover that as well as the restarts. {@code chaosDurationSec}
     * 0 does not wait.
     *
     * <p>On timeout the annotation is taken off the pods not yet rolled, so the
     * operator does not restart them later, in the middle of another step.
     */
    private void awaitRoll(FaultSpec spec, Map<String, String> uids) throws InterruptedException {
        String namespace = spec.targetNamespace();
        if (spec.chaosDurationSec() <= 0) {
            LOG.info("ROLLING_RESTART: chaosDurationSec is 0, not waiting for " + uids.keySet() + " to roll");
            return;
        }

        long deadline = System.nanoTime() + spec.chaosDurationSec() * 1_000_000_000L;
        Set<String> pending = new LinkedHashSet<>(uids.keySet());
        while (true) {
            pending.removeIf(name -> isReplacedAndReady(namespace, name, uids.get(name)));
            long remainingMs = (deadline - System.nanoTime()) / 1_000_000L;
            if (pending.isEmpty() || remainingMs <= 0) {
                break;
            }
            Thread.sleep(Math.min(rollPollIntervalMs, remainingMs));
        }

        if (pending.isEmpty()) {
            LOG.info("ROLLING_RESTART: rolled " + uids.keySet());
            return;
        }
        for (String name : pending) {
            try {
                removeManualRollingUpdate(namespace, name);
            } catch (KubernetesClientException e) {
                LOG.warn("ROLLING_RESTART: could not remove the annotation from " + name, e);
            }
        }
        throw new IncompleteRollException("ROLLING_RESTART: " + (uids.size() - pending.size()) + " of " + uids.size()
                + " pods rolled within chaosDurationSec=" + spec.chaosDurationSec() + "s; not rolled: " + pending
                + ". The Cluster Operator rolls at its next reconciliation and holds back a pod whose restart"
                + " would leave a partition under min.insync.replicas; its log says which.");
    }

    private boolean isReplacedAndReady(String namespace, String podName, String oldUid) {
        try {
            Pod pod = client.pods().inNamespace(namespace).withName(podName).get();
            return pod != null
                    && !Objects.equals(oldUid, pod.getMetadata().getUid())
                    && pod.getMetadata().getDeletionTimestamp() == null
                    && Readiness.isPodReady(pod);
        } catch (KubernetesClientException e) {
            LOG.debug("ROLLING_RESTART: could not read " + podName + ", checking again", e);
            return false;
        }
    }

    /** Takes the annotation off a pod not yet rolled; a replaced pod has none. */
    private void removeManualRollingUpdate(String namespace, String podName) {
        var resource = client.pods().inNamespace(namespace).withName(podName);
        Pod pod = resource.get();
        if (pod != null
                && pod.getMetadata().getAnnotations() != null
                && pod.getMetadata().getAnnotations().containsKey(MANUAL_ROLLING_UPDATE_ANNOTATION)) {
            resource.edit(p -> new PodBuilder(p)
                    .editMetadata()
                    .removeFromAnnotations(MANUAL_ROLLING_UPDATE_ANNOTATION)
                    .endMetadata()
                    .build());
        }
    }

    /**
     * Removes one broker from each workload the step selects
     * ({@link ScaleDownTargets}): a KafkaNodePool loses one replica through the
     * Strimzi Cluster Operator ({@link NodePoolScaleDown}), a StatefulSet (Kafka
     * not run by Strimzi) is scaled down by one, but never below one replica.
     * Each records its original replica count for rollback.
     */
    private void executeScaleDown(FaultSpec spec) throws InterruptedException {
        ScaleDownTargets.Targets targets = ScaleDownTargets.resolve(client, spec);
        targets.skipped().forEach(reason -> LOG.info("SCALE_DOWN: skipping, " + reason));
        if (targets.isEmpty()) {
            throw new IllegalStateException(
                    "SCALE_DOWN: nothing to scale down, " + String.join("; ", targets.skipped()));
        }

        if (!targets.pools().isEmpty()) {
            new NodePoolScaleDown(client, scalePollIntervalMs).run(spec, targets.pools());
        }
        int scaled = 0;
        for (String name : targets.statefulSets()) {
            if (scaleDownStatefulSet(spec.targetNamespace(), name)) {
                scaled++;
            }
        }
        if (targets.pools().isEmpty() && scaled == 0) {
            throw new IllegalStateException("SCALE_DOWN: nothing to scale down, StatefulSet(s) "
                    + targets.statefulSets() + " have one replica, and SCALE_DOWN leaves the last one");
        }
    }

    private boolean scaleDownStatefulSet(String namespace, String name) {
        StatefulSet ss = client.apps()
                .statefulSets()
                .inNamespace(namespace)
                .withName(name)
                .get();
        if (ss == null) {
            throw new IllegalStateException("StatefulSet not found: " + name);
        }
        // Read once, and the scale below sets an absolute count from it. A
        // request the client sends again sets the same count, and nothing reads
        // the count again, so a scale whose answer was lost removes one replica.
        int current = ss.getSpec().getReplicas() != null ? ss.getSpec().getReplicas() : 1;
        if (current <= 1) {
            LOG.info("SCALE_DOWN: StatefulSet " + name + " has one replica, leaving it");
            return false;
        }
        // Snapshot the ORIGINAL replica count once, before reducing.
        // A second SCALE_DOWN step must NOT overwrite it with the
        // already-reduced value, so only stamp when absent.
        Map<String, String> annotations = ss.getMetadata().getAnnotations();
        boolean alreadySnapshotted = annotations != null && annotations.containsKey(ORIGINAL_REPLICAS_ANNOTATION);
        if (!alreadySnapshotted) {
            String scaledDownAt = String.valueOf(System.currentTimeMillis());
            client.apps()
                    .statefulSets()
                    .inNamespace(namespace)
                    .withName(name)
                    .edit(s -> new StatefulSetBuilder(s)
                            .editMetadata()
                            .addToAnnotations(ORIGINAL_REPLICAS_ANNOTATION, String.valueOf(current))
                            .addToAnnotations(SCALED_DOWN_AT_ANNOTATION, scaledDownAt)
                            .endMetadata()
                            .build());
        }
        LOG.info("SCALE_DOWN: StatefulSet " + name + " from " + current + " → " + (current - 1));
        client.apps().statefulSets().inNamespace(namespace).withName(name).scale(current - 1);
        return true;
    }

    private void executeCpuStress(FaultSpec spec) {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("CPU_STRESS: injecting stress-ng ephemeral container into " + podName);
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    "chaos-cpu-stress",
                    "polinux/stress",
                    "stress",
                    "--cpu",
                    String.valueOf(spec.cpuCores()),
                    "--timeout",
                    spec.chaosDurationSec() + "s");
        }
    }

    private void executeIoStress(FaultSpec spec) {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("IO_STRESS: injecting stress-ng ephemeral container into " + podName);
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    "chaos-io-stress",
                    "polinux/stress",
                    "stress",
                    "--io",
                    String.valueOf(spec.ioWorkers()),
                    "--timeout",
                    spec.chaosDurationSec() + "s");
        }
    }

    private void injectEphemeralContainer(
            String namespace, String podName, String containerName, String image, String... command) {
        var podResource = client.pods().inNamespace(namespace).withName(podName);
        var pod = podResource.get();
        if (pod == null) {
            throw new IllegalStateException("Pod not found: " + podName);
        }

        EphemeralContainer ec = new EphemeralContainerBuilder()
                .withName(containerName)
                .withImage(image)
                .withCommand(command)
                .build();

        pod.getSpec().getEphemeralContainers().add(ec);

        // Ephemeral containers can only be added through the pods/ephemeralcontainers
        // subresource (k8s 1.25+); the API server rejects a pod update that changes them.
        client.pods().inNamespace(namespace).resource(pod).ephemeralContainers().replace();
    }

    @Override
    public ChaosStatus pollStatus(String engineName) {
        return ChaosStatus.COMPLETED;
    }

    /**
     * Removes every NetworkPolicy Kates made, in every namespace. The one write
     * here that is retried: it deletes by label, so doing it again removes
     * nothing more, and a policy left in place keeps a pod cut off.
     */
    @Retry(maxRetries = 3, delay = 2000)
    @Timeout(15000)
    @Override
    public void cleanup(String engineName) {
        try {
            client.network()
                    .networkPolicies()
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .delete();
            LOG.info("Cleaned up Kates-managed NetworkPolicies");
        } catch (Exception e) {
            LOG.warn("Cleanup failed", e);
            throw e; // throw to trigger retry
        }
    }

    @Timeout(5000)
    @Override
    public boolean isAvailable() {
        try {
            client.pods().inNamespace("default").list();
            return true;
        } catch (Exception e) {
            return false;
        }
    }
}
