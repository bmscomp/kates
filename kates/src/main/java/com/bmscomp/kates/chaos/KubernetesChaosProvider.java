package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.function.LongConsumer;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.api.model.EphemeralContainer;
import io.fabric8.kubernetes.api.model.EphemeralContainerBuilder;
import io.fabric8.kubernetes.api.model.Node;
import io.fabric8.kubernetes.api.model.NodeBuilder;
import io.fabric8.kubernetes.api.model.OwnerReference;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.api.model.VolumeMount;
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

    // Built without config, as the tests build it, a Quorum is alone and always leads.
    @Inject
    com.bmscomp.kates.quorum.Quorum quorum = new com.bmscomp.kates.quorum.Quorum();

    @org.eclipse.microprofile.config.inject.ConfigProperty(
            name = "kates.engine.max-duration-ms",
            defaultValue = "7200000")
    long maxDurationMs = 7_200_000;

    /**
     * The marks a fault written now carries: this leader's term, and a deadline at the engine's run
     * bound. shortcut: a fault the live leader leaves after a crashed plan thread lives up to that
     * bound (2 h); upgrade to the plan's own bound if that is seen.
     */
    Map<String, String> marks(long term) {
        return FaultMarks.of(term, Instant.now().plusMillis(maxDurationMs));
    }

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
    private void applyDisruption(FaultSpec spec, String engineName, Map<String, String> marks) throws Exception {
        switch (spec.disruptionType()) {
            case POD_KILL -> executePodKill(spec);
            case POD_DELETE -> executePodDelete(spec);
            case NETWORK_PARTITION -> executeNetworkPartition(spec, engineName, marks);
            case ROLLING_RESTART -> executeRollingRestart(spec);
            case SCALE_DOWN -> executeScaleDown(spec, marks);
            case LEADER_ELECTION -> executePodKill(spec);
            case CPU_STRESS -> executeCpuStress(spec, engineName);
            case IO_STRESS -> executeIoStress(spec, engineName);
            case MEMORY_STRESS -> executeMemoryStress(spec, engineName);
            case DISK_FILL -> executeDiskFill(spec, engineName);
            case NODE_DRAIN -> executeNodeDrain(spec, marks);
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
                        // Confirmed after the delay, right before any write: a member that stopped
                        // leading since the plan started writes nothing (J4 F1).
                        Map<String, String> marks = marks(quorum.requireWrites());
                        // The fault goes in from here, once its delay is over.
                        onInject.accept(startNanos);
                        applyDisruption(spec, engineName, marks);
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
    private void executeNetworkPartition(FaultSpec spec, String engineName, Map<String, String> marks)
            throws InterruptedException {
        Exception failure = null;
        try {
            for (String podName : PodTargets.resolve(client, spec)) {
                isolatePod(spec, podName, marks);
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

    private void isolatePod(FaultSpec spec, String podName, Map<String, String> marks) {
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
                .addToAnnotations(marks)
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
    private void executeScaleDown(FaultSpec spec, Map<String, String> marks) throws InterruptedException {
        ScaleDownTargets.Targets targets = ScaleDownTargets.resolve(client, spec);
        targets.skipped().forEach(reason -> LOG.info("SCALE_DOWN: skipping, " + reason));
        if (targets.isEmpty()) {
            throw new IllegalStateException(
                    "SCALE_DOWN: nothing to scale down, " + String.join("; ", targets.skipped()));
        }

        if (!targets.pools().isEmpty()) {
            new NodePoolScaleDown(client, scalePollIntervalMs, marks).run(spec, targets.pools());
        }
        int scaled = 0;
        for (String name : targets.statefulSets()) {
            if (scaleDownStatefulSet(spec.targetNamespace(), name, marks)) {
                scaled++;
            }
        }
        if (targets.pools().isEmpty() && scaled == 0) {
            throw new IllegalStateException("SCALE_DOWN: nothing to scale down, StatefulSet(s) "
                    + targets.statefulSets() + " have one replica, and SCALE_DOWN leaves the last one");
        }
    }

    private boolean scaleDownStatefulSet(String namespace, String name, Map<String, String> marks) {
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
        // The marks are written every time: a snapshot an earlier plan left carries an older term,
        // which the reconciler would take for a dead leader's fault and undo under this one.
        String scaledDownAt = String.valueOf(System.currentTimeMillis());
        client.apps().statefulSets().inNamespace(namespace).withName(name).edit(s -> {
            var metadata = new StatefulSetBuilder(s).editMetadata().addToAnnotations(marks);
            if (!alreadySnapshotted) {
                metadata.addToAnnotations(ORIGINAL_REPLICAS_ANNOTATION, String.valueOf(current))
                        .addToAnnotations(SCALED_DOWN_AT_ANNOTATION, scaledDownAt);
            }
            return metadata.endMetadata().build();
        });
        LOG.info("SCALE_DOWN: StatefulSet " + name + " from " + current + " → " + (current - 1));
        client.apps().statefulSets().inNamespace(namespace).withName(name).scale(current - 1);
        return true;
    }

    /**
     * Adds a stress container to each target pod, then waits out
     * {@code chaosDurationSec}, as the partition does: the stress runs that
     * long, and a fault reported done before then ended its plan, and released
     * its lease, while the pods were still under it.
     */
    private void executeCpuStress(FaultSpec spec, String engineName) throws InterruptedException {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("CPU_STRESS: injecting stress-ng ephemeral container into " + podName);
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    stressContainerName("chaos-cpu-stress", engineName),
                    stressImage,
                    "stress",
                    "--cpu",
                    String.valueOf(spec.cpuCores()),
                    "--timeout",
                    spec.chaosDurationSec() + "s");
        }
        Thread.sleep(Math.max(0, spec.chaosDurationSec()) * 1000L);
    }

    /** As {@link #executeCpuStress}, with IO workers. */
    private void executeIoStress(FaultSpec spec, String engineName) throws InterruptedException {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("IO_STRESS: injecting stress-ng ephemeral container into " + podName);
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    stressContainerName("chaos-io-stress", engineName),
                    stressImage,
                    "stress",
                    "--io",
                    String.valueOf(spec.ioWorkers()),
                    "--timeout",
                    spec.chaosDurationSec() + "s");
        }
        Thread.sleep(Math.max(0, spec.chaosDurationSec()) * 1000L);
    }

    /**
     * As {@link #executeCpuStress}, with one worker that holds {@code memoryMb}.
     * The container is the pod's, not the broker container's, so it presses on
     * the pod's and the node's memory, not the broker container's own limit.
     */
    private void executeMemoryStress(FaultSpec spec, String engineName) throws InterruptedException {
        for (String podName : PodTargets.resolve(client, spec)) {
            LOG.info("MEMORY_STRESS: injecting stress ephemeral container into " + podName);
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    stressContainerName("chaos-memory-stress", engineName),
                    stressImage,
                    "stress",
                    "--vm",
                    "1",
                    "--vm-bytes",
                    spec.memoryMb() + "M",
                    "--vm-keep",
                    "--timeout",
                    spec.chaosDurationSec() + "s");
        }
        Thread.sleep(Math.max(0, spec.chaosDurationSec()) * 1000L);
    }

    /** Where the DISK_FILL container mounts the broker's data volume. */
    static final String FILL_MOUNT = "/kates-fill";

    /**
     * Fills each target pod's data volume to {@code fillPercentage} for
     * {@code chaosDurationSec}, from an ephemeral container that mounts it and
     * writes one file, as the broker's own user, into the first directory there
     * it may write: the volume's root, or the broker's log directory, where
     * Kafka ignores a plain file. The file goes when the container exits,
     * at the end of the duration or when the pod stops; a pod killed outright
     * leaves it, named kates-disk-fill-*, on the volume.
     */
    private void executeDiskFill(FaultSpec spec, String engineName) throws InterruptedException {
        String id = engineName.substring(engineName.lastIndexOf('-') + 1);
        String containerName = "chaos-disk-fill-" + id;
        List<String> pods = PodTargets.resolve(client, spec);
        for (String podName : pods) {
            Pod pod = client.pods()
                    .inNamespace(spec.targetNamespace())
                    .withName(podName)
                    .get();
            if (pod == null) {
                throw new IllegalStateException("Pod not found: " + podName);
            }
            String volume = dataVolume(pod);
            LOG.info("DISK_FILL: filling volume " + volume + " of " + podName + " to " + spec.fillPercentage() + "%");
            injectEphemeralContainer(
                    spec.targetNamespace(),
                    podName,
                    containerName,
                    stressImage,
                    new io.fabric8.kubernetes.api.model.VolumeMountBuilder()
                            .withName(volume)
                            .withMountPath(FILL_MOUNT)
                            .build(),
                    "sh",
                    "-c",
                    fillScript(spec.fillPercentage(), spec.chaosDurationSec(), id));
        }
        Thread.sleep(Math.max(0, spec.chaosDurationSec()) * 1000L);
        for (String podName : pods) {
            Integer exit = ephemeralExitCode(spec.targetNamespace(), podName, containerName);
            if (exit != null && exit != 0) {
                throw new IllegalStateException("DISK_FILL: the fill container in " + podName + " exited " + exit
                        + ", so the volume was not filled; kubectl logs " + podName + " -c " + containerName
                        + " says why");
            }
        }
    }

    /**
     * The shell the DISK_FILL container runs (busybox sh): it writes up to
     * {@code percent} of the volume, holds it until {@code durationSec} has
     * passed since it started, and removes the file on exit or SIGTERM. dd and
     * sleep run in the background, waited on, because sh runs a trap only once
     * a foreground command ends: a SIGTERM during a long dd otherwise came to
     * nothing, the kubelet killed the container, and the file stayed.
     */
    static String fillScript(int percent, int durationSec, String id) {
        return "end=$(($(date +%s)+" + durationSec + ")); d=; for c in " + FILL_MOUNT + " " + FILL_MOUNT + "/*/; do"
                + " if [ -d \"$c\" ] && [ -w \"$c\" ]; then d=$c; break; fi; done;"
                + " [ -n \"$d\" ] || { echo 'no directory on the volume this user may write' >&2; exit 1; };"
                + " f=${d%/}/kates-disk-fill-" + id + "; p=;"
                + " trap '[ -n \"$p\" ] && kill $p 2>/dev/null; rm -f \"$f\"' EXIT; trap 'exit 0' TERM INT;"
                + " set -- $(df -Pk " + FILL_MOUNT + " | tail -n 1); want=$((($2*" + percent + "/100-$3)/1024));"
                + " if [ \"$want\" -gt 0 ]; then dd if=/dev/zero of=\"$f\" bs=1048576 count=\"$want\" 2>/dev/null &"
                + " p=$!; wait $p; p=; fi; left=$((end-$(date +%s)));"
                + " if [ \"$left\" -gt 0 ]; then sleep \"$left\" & p=$!; wait $p; p=; fi; exit 0";
    }

    /**
     * The volume DISK_FILL fills: the first one the pod's first container
     * mounts that a PersistentVolumeClaim backs, else the first it mounts
     * named data*, as Strimzi names a broker's (data-0 for JBOD and
     * ephemeral storage).
     *
     * @throws IllegalStateException when the pod has neither
     */
    static String dataVolume(Pod pod) {
        var mounted = pod.getSpec().getContainers().getFirst().getVolumeMounts().stream()
                .map(VolumeMount::getName)
                .toList();
        var volumes = pod.getSpec().getVolumes().stream()
                .filter(v -> mounted.contains(v.getName()))
                .toList();
        return volumes.stream()
                .filter(v -> v.getPersistentVolumeClaim() != null)
                .findFirst()
                .or(() -> volumes.stream()
                        .filter(v -> v.getName().startsWith("data"))
                        .findFirst())
                .map(io.fabric8.kubernetes.api.model.Volume::getName)
                .orElseThrow(() -> new IllegalStateException(
                        "DISK_FILL: pod " + pod.getMetadata().getName()
                                + " mounts no PersistentVolumeClaim and no volume named data*, so there is no data volume"
                                + " to fill"));
    }

    /** The exit code of a terminated ephemeral container, or null while it runs or when it can't be read. */
    private Integer ephemeralExitCode(String namespace, String podName, String containerName) {
        try {
            Pod pod = client.pods().inNamespace(namespace).withName(podName).get();
            if (pod == null || pod.getStatus() == null || pod.getStatus().getEphemeralContainerStatuses() == null) {
                return null;
            }
            return pod.getStatus().getEphemeralContainerStatuses().stream()
                    .filter(s -> containerName.equals(s.getName()))
                    .map(s -> s.getState() != null && s.getState().getTerminated() != null
                            ? s.getState().getTerminated().getExitCode()
                            : null)
                    .filter(Objects::nonNull)
                    .findFirst()
                    .orElse(null);
        } catch (KubernetesClientException e) {
            LOG.debug("DISK_FILL: could not read " + podName + "'s containers", e);
            return null;
        }
    }

    /** Label on a node a NODE_DRAIN cordoned, which orphan recovery looks for. */
    public static final String CORDONED_LABEL = "kates.io/cordoned";

    /** Epoch millis of the cordon, which orphan recovery compares with its start. */
    public static final String CORDONED_AT_ANNOTATION = "kates.io/cordoned-at";

    /** How often NODE_DRAIN tries again to evict the pods a PodDisruptionBudget held back. */
    long drainPollIntervalMs = 5_000;

    /** The node the Kates API runs on, which a drain never takes; null in tests. */
    @Inject
    KatesNode katesNode;

    /**
     * Drains a node as kubectl drain does, for {@code chaosDurationSec}: cordons
     * it, evicts its pods through the Eviction API, which honours their
     * PodDisruptionBudgets, and tries again every few seconds for those a
     * budget holds back; then uncordons it, whatever happened, as the partition
     * removes its policy. DaemonSet and static pods stay. The node is picked
     * as on litmus-crd ({@link DrainTargets}), never the Kates API's. A node
     * already cordoned is refused: uncordoning it at the end would undo
     * someone else's cordon.
     *
     * @throws IllegalStateException when a pod was still not evicted when the
     *     duration ended; the node is uncordoned all the same
     */
    private void executeNodeDrain(FaultSpec spec, Map<String, String> marks) throws InterruptedException {
        Optional<String> kates = katesNode != null ? katesNode.name() : Optional.empty();
        String node = DrainTargets.node(client, spec, kates);
        Node current = client.nodes().withName(node).get();
        if (current == null) {
            throw new IllegalStateException("NODE_DRAIN: node " + node + " was not found");
        }
        if (current.getSpec() != null && Boolean.TRUE.equals(current.getSpec().getUnschedulable())) {
            throw new IllegalStateException("NODE_DRAIN: node " + node + " is already cordoned, and Kates leaves a"
                    + " cordon it did not make alone");
        }
        long deadline = System.nanoTime() + Math.max(0, spec.chaosDurationSec()) * 1_000_000_000L;
        LOG.info("NODE_DRAIN: cordoning " + node);
        client.nodes()
                .withName(node)
                .edit(n -> new NodeBuilder(n)
                        .editMetadata()
                        .addToLabels(CORDONED_LABEL, "true")
                        .addToAnnotations(marks)
                        .addToAnnotations(CORDONED_AT_ANNOTATION, String.valueOf(System.currentTimeMillis()))
                        .endMetadata()
                        .editOrNewSpec()
                        .withUnschedulable(true)
                        .endSpec()
                        .build());
        Exception failure = null;
        try {
            Set<String> pending = evictAll(node, deadline);
            long remainingMs = (deadline - System.nanoTime()) / 1_000_000L;
            if (remainingMs > 0) {
                Thread.sleep(remainingMs);
            }
            if (!pending.isEmpty()) {
                throw new IllegalStateException("NODE_DRAIN: " + pending.size() + " pod(s) on " + node
                        + " were not evicted within chaosDurationSec=" + spec.chaosDurationSec() + "s: " + pending
                        + ". A PodDisruptionBudget refused their eviction");
            }
        } catch (RuntimeException | InterruptedException e) {
            failure = e;
            throw e;
        } finally {
            removeCordon(node, failure);
        }
    }

    /**
     * Evicts every pod on {@code node} a drain evicts, trying again those a
     * PodDisruptionBudget holds back until {@code deadline}; the namespace/name
     * of those still not evicted then.
     */
    private Set<String> evictAll(String node, long deadline) throws InterruptedException {
        Map<String, Pod> pending = new LinkedHashMap<>();
        // The field selector spares the API server; the filter is what decides.
        for (Pod pod : client.pods()
                .inAnyNamespace()
                .withField("spec.nodeName", node)
                .list()
                .getItems()) {
            if (node.equals(pod.getSpec().getNodeName()) && isEvictable(pod)) {
                pending.put(
                        pod.getMetadata().getNamespace() + "/"
                                + pod.getMetadata().getName(),
                        pod);
            }
        }
        LOG.info("NODE_DRAIN: evicting " + pending.keySet() + " from " + node);
        while (true) {
            pending.values().removeIf(this::evict);
            long remainingMs = (deadline - System.nanoTime()) / 1_000_000L;
            if (pending.isEmpty() || remainingMs <= 0) {
                return pending.keySet();
            }
            Thread.sleep(Math.min(drainPollIntervalMs, remainingMs));
        }
    }

    /** As kubectl drain: not a DaemonSet's pod, a static pod, one already going, or one that has ended. */
    static boolean isEvictable(Pod pod) {
        var meta = pod.getMetadata();
        if (meta.getDeletionTimestamp() != null
                || (meta.getAnnotations() != null
                        && meta.getAnnotations().containsKey("kubernetes.io/config.mirror"))) {
            return false;
        }
        if (meta.getOwnerReferences() != null
                && meta.getOwnerReferences().stream().anyMatch(o -> "DaemonSet".equals(o.getKind()))) {
            return false;
        }
        String phase = pod.getStatus() != null ? pod.getStatus().getPhase() : null;
        return !"Succeeded".equals(phase) && !"Failed".equals(phase);
    }

    /** Whether the pod is evicted or gone; false while a PodDisruptionBudget holds it back. */
    private boolean evict(Pod pod) {
        String namespace = pod.getMetadata().getNamespace();
        String name = pod.getMetadata().getName();
        try {
            boolean evicted =
                    client.pods().inNamespace(namespace).withName(name).evict();
            if (evicted) {
                LOG.info("NODE_DRAIN: evicted " + namespace + "/" + name);
            }
            return evicted;
        } catch (KubernetesClientException e) {
            if (e.getCode() == 404) {
                return true;
            }
            throw e;
        }
    }

    /**
     * Uncordons the node, trying three times: a node left cordoned takes no new
     * pod until someone uncordons it. When that fails, the fault fails saying
     * how, with its own failure, if any, suppressed; orphan recovery uncordons
     * it once its deadline has passed.
     */
    private void removeCordon(String node, Exception failure) {
        KubernetesClientException last = null;
        for (int attempt = 0; attempt < 3; attempt++) {
            try {
                uncordon(client, node);
                LOG.info("NODE_DRAIN: uncordoned " + node);
                return;
            } catch (KubernetesClientException e) {
                last = e;
                if (attempt == 2) {
                    break;
                }
                try {
                    Thread.sleep(2_000);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }
        IllegalStateException notRemoved = new IllegalStateException(
                "NODE_DRAIN: could not uncordon node " + node
                        + ", so it takes no new pod. Uncordon it with kubectl uncordon " + node + ". "
                        + (last != null ? last.getMessage() : "interrupted"),
                last);
        if (failure != null) {
            notRemoved.addSuppressed(failure);
        }
        if (failure instanceof InterruptedException) {
            Thread.currentThread().interrupt();
        }
        throw notRemoved;
    }

    /** Uncordons a node a NODE_DRAIN cordoned and takes Kates's marks off it; also orphan recovery's undo. */
    public static void uncordon(KubernetesClient client, String node) {
        client.nodes()
                .withName(node)
                .edit(n -> new NodeBuilder(n)
                        .editMetadata()
                        .removeFromLabels(CORDONED_LABEL)
                        .removeFromAnnotations(CORDONED_AT_ANNOTATION)
                        .removeFromAnnotations(FaultMarks.TERM)
                        .removeFromAnnotations(FaultMarks.DEADLINE)
                        .endMetadata()
                        .editOrNewSpec()
                        .withUnschedulable(null)
                        .endSpec()
                        .build());
    }

    /**
     * An ephemeral container is never removed, and a pod refuses a second one
     * of the same name, so each fault names its own: the prefix and the engine
     * id's timestamp. Not the whole engine id, whose experiment name is free
     * text and a container name a DNS label.
     */
    static String stressContainerName(String prefix, String engineName) {
        return prefix + "-" + engineName.substring(engineName.lastIndexOf('-') + 1);
    }

    static final String DEFAULT_STRESS_IMAGE = "docker.io/polinux/stress:1.0.4";

    /**
     * The image CPU_STRESS and IO_STRESS run as an ephemeral container. Written
     * twice, like FaultLimits: in the annotation for the application, and as
     * the field's value for a provider made with new, as tests make it.
     */
    @org.eclipse.microprofile.config.inject.ConfigProperty(
            name = "kates.chaos.stress-image",
            defaultValue = DEFAULT_STRESS_IMAGE)
    String stressImage = DEFAULT_STRESS_IMAGE;

    private void injectEphemeralContainer(
            String namespace, String podName, String containerName, String image, String... command) {
        injectEphemeralContainer(namespace, podName, containerName, image, null, command);
    }

    private void injectEphemeralContainer(
            String namespace,
            String podName,
            String containerName,
            String image,
            VolumeMount mount,
            String... command) {
        var podResource = client.pods().inNamespace(namespace).withName(podName);
        var pod = podResource.get();
        if (pod == null) {
            throw new IllegalStateException("Pod not found: " + podName);
        }

        // stress needs no privileges: run as the target's own non-root user and group, which its
        // namespace already admits (OpenShift's restricted-v2 allows only the namespace's uid range
        // and writes it on each container, not the pod), else as nobody, which polinux/stress is
        // not by default. Either way the container passes Pod Security "restricted" there.
        var podSc = pod.getSpec().getSecurityContext();
        Long uid = podSc == null || podSc.getRunAsUser() == null || podSc.getRunAsUser() == 0
                ? null
                : podSc.getRunAsUser();
        Long gid = podSc == null ? null : podSc.getRunAsGroup();
        for (var container : pod.getSpec().getContainers()) {
            var sc = container.getSecurityContext();
            if (sc == null) {
                continue;
            }
            if (uid == null && sc.getRunAsUser() != null && sc.getRunAsUser() != 0) {
                uid = sc.getRunAsUser();
            }
            if (gid == null && sc.getRunAsGroup() != null) {
                gid = sc.getRunAsGroup();
            }
        }

        EphemeralContainer ec = new EphemeralContainerBuilder()
                .withName(containerName)
                .withImage(image)
                .withCommand(command)
                .withVolumeMounts(mount == null ? List.of() : List.of(mount))
                .withNewSecurityContext()
                .withRunAsNonRoot(true)
                .withRunAsUser(uid == null ? 65534L : uid)
                .withRunAsGroup(gid == null ? 65534L : gid)
                .withAllowPrivilegeEscalation(false)
                .withNewCapabilities()
                .withDrop("ALL")
                .endCapabilities()
                .withNewSeccompProfile()
                .withType("RuntimeDefault")
                .endSeccompProfile()
                .endSecurityContext()
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
