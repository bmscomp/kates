package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executor;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;
import java.util.function.LongConsumer;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.client.KubernetesClient;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.litmus.*;

/**
 * Chaos provider that creates Litmus ChaosEngine CRs to trigger experiments.
 * Uses Fabric8 Watchers for asynchronous, event-driven polling of results.
 */
@ApplicationScoped
@Named("litmus-crd")
public class LitmusChaosProvider implements ChaosProvider {

    private static final Logger LOG = Logger.getLogger(LitmusChaosProvider.class);

    private static final Map<DisruptionType, String> EXPERIMENT_MAP = Map.ofEntries(
            Map.entry(DisruptionType.POD_KILL, "pod-delete"),
            Map.entry(DisruptionType.CPU_STRESS, "pod-cpu-hog"),
            Map.entry(DisruptionType.MEMORY_STRESS, "pod-memory-hog"),
            Map.entry(DisruptionType.IO_STRESS, "pod-io-stress"),
            Map.entry(DisruptionType.DNS_ERROR, "pod-dns-error"),
            Map.entry(DisruptionType.DISK_FILL, "disk-fill"),
            Map.entry(DisruptionType.NETWORK_PARTITION, "pod-network-partition"),
            Map.entry(DisruptionType.NETWORK_LATENCY, "pod-network-latency"),
            Map.entry(DisruptionType.NODE_DRAIN, "node-drain"),
            // Leader election is triggered by deleting the current partition leader pod
            Map.entry(DisruptionType.LEADER_ELECTION, "pod-delete"));

    /**
     * Faults the kubernetes provider injects whichever backend runs, because
     * pod-delete, the nearest Litmus experiment, does something else. It kills
     * pods without waiting for them to come back, so it does no rolling
     * restart, and it removes no broker: the StrimziPodSet recreates a deleted
     * pod at once. Both go through the Strimzi Cluster Operator instead. Nor
     * does it take a grace period: it deletes with none under FORCE=true,
     * which pods of a StrimziPodSet need (see buildChaosEngine), and with the
     * pod's own otherwise. A POD_DELETE is deleted with gracePeriodSec, so the
     * broker gets the controlled shutdown the fault is meant to test.
     */
    private static final Set<DisruptionType> ON_KUBERNETES_API =
            EnumSet.of(DisruptionType.ROLLING_RESTART, DisruptionType.SCALE_DOWN, DisruptionType.POD_DELETE);

    /** How often the ChaosResult is read until it has a verdict. */
    int resultPollIntervalMs = 5_000;

    @Inject
    KubernetesClient client;

    @Inject
    com.bmscomp.kates.engine.KatesExecutor executor;

    @Inject
    @Named("kubernetes")
    KubernetesChaosProvider kubernetes;

    @Override
    public String name() {
        return "litmus-crd";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        return triggerFault(spec, injectedAt -> {});
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec, LongConsumer onInject) {
        if (ON_KUBERNETES_API.contains(spec.disruptionType())) {
            return kubernetes.triggerFault(spec, onInject);
        }
        if (spec.delayBeforeSec() <= 0) {
            return inject(spec, onInject);
        }
        // Waited out before the pods are picked and the ChaosEngine exists, as
        // the kubernetes provider waits it. Litmus's own RAMP_TIME would wait
        // after the fault as well, past the time Kates waits for the result.
        Executor afterDelay =
                CompletableFuture.delayedExecutor(spec.delayBeforeSec(), TimeUnit.SECONDS, executor.get());
        return CompletableFuture.supplyAsync(() -> inject(spec, onInject), afterDelay)
                .thenCompose(Function.identity());
    }

    /**
     * Creates the ChaosEngine and polls its ChaosResult. The start is taken
     * here, after any delay, so the outcome times the fault, not the wait, and
     * it is the moment {@code onInject} gets.
     */
    private CompletableFuture<ChaosOutcome> inject(FaultSpec spec, LongConsumer onInject) {
        Instant start = Instant.now();
        long startNanos = System.nanoTime();
        String engineName = "kates-" + spec.experimentName() + "-" + System.currentTimeMillis();

        CompletableFuture<ChaosOutcome> future = new CompletableFuture<>();

        try {
            String experimentName = resolveExperiment(spec);

            ChaosEngine engine = buildChaosEngine(spec, engineName, experimentName);
            String resultName = engineName + "-" + experimentName;

            // Once the pods are picked, before the engine that injects the fault exists.
            onInject.accept(startNanos);

            // NOW create the engine
            client.resources(ChaosEngine.class)
                    .inNamespace(spec.targetNamespace())
                    .resource(engine)
                    .create();

            LOG.info("Created ChaosEngine: " + engineName);
            LOG.info("Polling ChaosResult: " + resultName + " in ns=" + spec.targetNamespace());

            // Simple polling loop — more reliable than Fabric8 watchers on CRDs
            int timeoutSec = spec.chaosDurationSec() + 120;
            int pollIntervalMs = resultPollIntervalMs;
            int maxPolls = (timeoutSec * 1000) / pollIntervalMs;

            // Runs on the shared bounded executor, NOT the common ForkJoinPool.
            // This loop sleeps 5s per iteration for up to (chaosDuration + 120)s;
            // on the common pool (parallelism = cores - 1, shared with every
            // parallel stream in the JVM) a couple of concurrent experiments
            // could occupy it entirely while doing nothing but sleeping.
            CompletableFuture.runAsync(
                    () -> {
                        for (int i = 0; i < maxPolls && !future.isDone(); i++) {
                            try {
                                Thread.sleep(pollIntervalMs);
                                if (future.isDone()) break;

                                var existing = client.resources(ChaosResult.class)
                                        .inNamespace(spec.targetNamespace())
                                        .withName(resultName)
                                        .get();

                                if (existing == null) {
                                    LOG.debugf("Poll %d: ChaosResult '%s' not found yet", i + 1, resultName);
                                    continue;
                                }

                                if (existing.getStatus() == null || existing.getStatus().experimentStatus == null) {
                                    LOG.debugf("Poll %d: ChaosResult exists but no status yet", i + 1);
                                    continue;
                                }

                                String v = existing.getStatus().experimentStatus.verdict;
                                LOG.infof("Poll %d: ChaosResult verdict=%s", i + 1, v);

                                if (v != null && !v.equalsIgnoreCase("Awaited")) {
                                    var s = existing.getStatus().experimentStatus;
                                    if ("Pass".equalsIgnoreCase(v)) {
                                        future.complete(ChaosOutcome.success(
                                                engineName,
                                                experimentName,
                                                start,
                                                Instant.now(),
                                                startNanos,
                                                s.probeSuccessPercentage,
                                                s.failStep,
                                                s.phase));
                                    } else {
                                        future.complete(ChaosOutcome.failure(
                                                engineName,
                                                experimentName,
                                                start,
                                                Instant.now(),
                                                startNanos,
                                                "ChaosResult verdict: " + v,
                                                s.probeSuccessPercentage,
                                                s.failStep,
                                                s.phase));
                                    }
                                    return;
                                }
                            } catch (InterruptedException e) {
                                Thread.currentThread().interrupt();
                                break;
                            } catch (Exception e) {
                                LOG.warnf("Poll error: %s", e.getMessage());
                            }
                        }

                        // Timeout — no result received
                        if (!future.isDone()) {
                            future.complete(ChaosOutcome.failure(
                                    engineName,
                                    experimentName,
                                    start,
                                    Instant.now(),
                                    startNanos,
                                    "Timeout polling for ChaosResult after " + timeoutSec + "s",
                                    null,
                                    null,
                                    null));
                        }
                    },
                    executor.get());

        } catch (Exception e) {
            LOG.error("Litmus fault injection failed", e);
            future.complete(ChaosOutcome.failure(
                    engineName,
                    spec.experimentName(),
                    start,
                    Instant.now(),
                    startNanos,
                    e.getMessage(),
                    null,
                    null,
                    null));
        }

        return future;
    }

    private String resolveExperiment(FaultSpec spec) {
        if (spec.disruptionType() != null) {
            String mapped = EXPERIMENT_MAP.get(spec.disruptionType());

            // Try to dynamically discover installed ChaosExperiments
            try {
                if (mapped != null) {
                    var exps = client.resources(ChaosExperiment.class)
                            .inNamespace(spec.targetNamespace())
                            .list()
                            .getItems();

                    for (ChaosExperiment exp : exps) {
                        if (exp.getMetadata().getName().equals(mapped)) {
                            return mapped;
                        }
                    }
                }
            } catch (Exception e) {
                LOG.debug("Failed to list ChaosExperiments dynamically", e);
            }

            if (mapped != null) {
                return mapped;
            }
        }
        return spec.experimentName();
    }

    ChaosEngine buildChaosEngine(FaultSpec spec, String engineName, String experimentName) {
        ChaosEngine engine = new ChaosEngine();
        engine.getMetadata().setName(engineName);
        engine.getMetadata().setNamespace(spec.targetNamespace());
        engine.getMetadata().setLabels(Map.of("managed-by", "kates"));

        ChaosEngineSpec engineSpec = new ChaosEngineSpec();
        engineSpec.engineState = "active";
        engineSpec.chaosServiceAccount = "litmus-admin";
        engineSpec.annotationCheck = "false";

        // For Strimzi 0.30+, there is no StatefulSet. StrimziPodSet is a custom controller
        // that Litmus chaos-runner doesn't natively support for target resolution.
        // We omit appinfo completely to run in "infrastructure" mode and rely on TARGET_PODS.
        // engineSpec.appinfo = appinfo;

        ChaosEngineSpec.Experiment experiment = new ChaosEngineSpec.Experiment();
        experiment.name = experimentName;

        ChaosEngineSpec.Components components = new ChaosEngineSpec.Components();
        List<ChaosEngineSpec.EnvVar> envVars = new ArrayList<>();
        envVars.add(new ChaosEngineSpec.EnvVar("TOTAL_CHAOS_DURATION", String.valueOf(spec.chaosDurationSec())));

        // Resolved here, not by Litmus, so targetBrokerId and targetAll mean the
        // same thing as on the kubernetes backend. node-drain takes their node.
        boolean hasTarget = (spec.targetPod() != null && !spec.targetPod().isEmpty())
                || (spec.targetLabel() != null && !spec.targetLabel().isBlank());
        if (hasTarget && !EXPERIMENT_MAP.get(DisruptionType.NODE_DRAIN).equals(experimentName)) {
            envVars.add(new ChaosEngineSpec.EnvVar("TARGET_PODS", String.join(",", PodTargets.resolve(client, spec))));
        }

        if (spec.envOverrides() != null) {
            for (Map.Entry<String, String> e : spec.envOverrides().entrySet()) {
                envVars.add(new ChaosEngineSpec.EnvVar(e.getKey(), e.getValue()));
            }
        }

        if (spec.disruptionType() != null) {
            switch (spec.disruptionType()) {
                // Strimzi uses StrimziPodSet (a custom CRD controller) instead of StatefulSet.
                // Litmus go-runner does not recognise StrimziPodSet as a workload type, so
                // after deleting the target pod it fails during recovery verification with:
                //   "TARGET_SELECTION_ERROR: no pod found for specified target {kind: strimzipodset}"
                // Setting FORCE=true performs an immediate delete (skip graceful termination)
                // and SEQUENCE=serial avoids the parallel-mode workload-based pod status check
                // that triggers the StrimziPodSet lookup failure. A POD_DELETE, which must not
                // be forced, goes to the kubernetes provider instead (ON_KUBERNETES_API).
                case POD_KILL, LEADER_ELECTION -> {
                    envVars.add(new ChaosEngineSpec.EnvVar("FORCE", "true"));
                    envVars.add(new ChaosEngineSpec.EnvVar("SEQUENCE", "serial"));
                }
                case CPU_STRESS ->
                    envVars.add(new ChaosEngineSpec.EnvVar("CPU_CORES", String.valueOf(spec.cpuCores())));
                case MEMORY_STRESS -> {
                    envVars.add(new ChaosEngineSpec.EnvVar("MEMORY_CONSUMPTION", String.valueOf(spec.memoryMb())));
                    envVars.add(new ChaosEngineSpec.EnvVar("NUMBER_OF_WORKERS", "1"));
                }
                case IO_STRESS -> {
                    envVars.add(new ChaosEngineSpec.EnvVar(
                            "FILESYSTEM_UTILIZATION_PERCENTAGE", String.valueOf(spec.fillPercentage())));
                    envVars.add(new ChaosEngineSpec.EnvVar("NUMBER_OF_WORKERS", String.valueOf(spec.ioWorkers())));
                }
                case DNS_ERROR -> {
                    if (spec.targetTopic() != null && !spec.targetTopic().isEmpty()) {
                        envVars.add(new ChaosEngineSpec.EnvVar("TARGET_HOSTNAMES", spec.targetTopic()));
                    }
                }
                case NETWORK_LATENCY ->
                    envVars.add(new ChaosEngineSpec.EnvVar("NETWORK_LATENCY", String.valueOf(spec.networkLatencyMs())));
                case DISK_FILL ->
                    envVars.add(new ChaosEngineSpec.EnvVar("FILL_PERCENTAGE", String.valueOf(spec.fillPercentage())));
                default -> {}
            }
        }

        // By experiment, not by type, so a step without a type that names
        // node-drain gets its node too. An override that picks the node is
        // left to do so: node-drain reads NODE_LABEL only when TARGET_NODE is
        // empty, so a TARGET_NODE from Kates would overrule it. Either way,
        // Litmus's own pods run on a node the drain doesn't take.
        if (EXPERIMENT_MAP.get(DisruptionType.NODE_DRAIN).equals(experimentName)) {
            java.util.function.Predicate<io.fabric8.kubernetes.api.model.Node> drained;
            if (overridesTheNode(spec)) {
                drained = drainedByOverride(spec);
            } else {
                String node = targetNode(spec);
                envVars.add(new ChaosEngineSpec.EnvVar("TARGET_NODE", node));
                drained = n -> node.equals(n.getMetadata().getName());
            }
            if (drained != null) {
                Map<String, String> away = litmusPodsAwayFrom(drained);
                components.nodeSelector = away;
                engineSpec.components = new ChaosEngineSpec.EngineComponents();
                engineSpec.components.runner = new ChaosEngineSpec.Runner();
                engineSpec.components.runner.nodeSelector = away;
            }
        }

        components.env = envVars;
        ChaosEngineSpec.ExperimentSpec expSpec = new ChaosEngineSpec.ExperimentSpec();
        expSpec.components = components;

        if (spec.probes() != null && !spec.probes().isEmpty()) {
            List<ChaosEngineSpec.Probe> litmusProbes = new ArrayList<>();
            for (ProbeSpec p : spec.probes()) {
                ChaosEngineSpec.Probe lp = new ChaosEngineSpec.Probe();
                lp.name = p.name();
                lp.type = p.type();

                if ("cmdProbe".equals(p.type()) && p.command() != null) {
                    ChaosEngineSpec.CmdProbe cmd = new ChaosEngineSpec.CmdProbe();
                    cmd.inputs = new ChaosEngineSpec.CmdProbeInputs();
                    cmd.inputs.command = p.command();
                    cmd.inputs.comparator = new ChaosEngineSpec.Comparator();
                    if (p.expectedOutput() != null) {
                        cmd.inputs.comparator.value = p.expectedOutput();
                    }
                    lp.cmdProbe = cmd;
                }

                lp.runProperties = new ChaosEngineSpec.RunProperties();
                litmusProbes.add(lp);
            }
            expSpec.probe = litmusProbes;
        }

        experiment.spec = expSpec;

        engineSpec.experiments = List.of(experiment);
        engine.setSpec(engineSpec);

        return engine;
    }

    /** Whether envOverrides picks the node: TARGET_NODE names it, NODE_LABEL has Litmus pick one. */
    private static boolean overridesTheNode(FaultSpec spec) {
        return spec.envOverrides() != null
                && (spec.envOverrides().containsKey("TARGET_NODE")
                        || spec.envOverrides().containsKey("NODE_LABEL"));
    }

    /**
     * The node a drain drains: the one the pods it picks run on
     * ({@link PodTargets}), so it hits the pod its targetPod, targetBrokerId
     * or selector picks, as every other fault does. node-drain, given no
     * TARGET_NODE, drains the node of a random pod in any namespace, the
     * control plane's among them, and Kates used to give it none. It drains
     * one node, so the pods a targetAll drain picks must all run on the same
     * one.
     *
     * @throws IllegalStateException when a pod is gone or not on a node yet,
     *     or the pods run on more than one node; the fault fails before the
     *     ChaosEngine exists, so nothing is drained
     */
    private String targetNode(FaultSpec spec) {
        List<String> pods = PodTargets.resolve(client, spec);
        Set<String> nodes = new TreeSet<>();
        for (String name : pods) {
            var pod = client.pods()
                    .inNamespace(spec.targetNamespace())
                    .withName(name)
                    .get();
            if (pod == null) {
                throw new IllegalStateException("NODE_DRAIN: pod " + name + " was not found in namespace '"
                        + spec.targetNamespace() + "', so there is no node to drain");
            }
            String node = pod.getSpec() != null ? pod.getSpec().getNodeName() : null;
            if (node == null || node.isBlank()) {
                throw new IllegalStateException(
                        "NODE_DRAIN: pod " + name + " is not on a node yet, so there is no node to drain");
            }
            nodes.add(node);
        }
        if (nodes.size() > 1) {
            throw new IllegalStateException("NODE_DRAIN: the pods targetAll picks run on " + nodes.size() + " nodes ("
                    + String.join(", ", nodes) + "), and node-drain drains one. Narrow targetLabel to the pods of"
                    + " one node, or name the node in envOverrides.TARGET_NODE");
        }
        String node = nodes.iterator().next();
        LOG.info("NODE_DRAIN: TARGET_NODE " + node + ", the node of " + String.join(", ", pods));
        return node;
    }

    /**
     * The nodes an override may have node-drain drain: the one TARGET_NODE
     * names, or else those NODE_LABEL matches. Null when both are empty, and
     * node-drain then drains the node of a random pod in any namespace, which
     * no node is safe from.
     */
    private static java.util.function.Predicate<io.fabric8.kubernetes.api.model.Node> drainedByOverride(
            FaultSpec spec) {
        String targetNode = spec.envOverrides().get("TARGET_NODE");
        if (targetNode != null && !targetNode.isBlank()) {
            return n -> targetNode.equals(n.getMetadata().getName());
        }
        String nodeLabel = spec.envOverrides().get("NODE_LABEL");
        if (nodeLabel != null && !nodeLabel.isBlank()) {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(nodeLabel);
            return n -> selector.matches(
                    n.getMetadata().getLabels() != null ? n.getMetadata().getLabels() : Map.of());
        }
        return null;
    }

    /** The node label Litmus's pods are pinned by, which the kubelet sets on every node. */
    private static final String HOSTNAME_LABEL = "kubernetes.io/hostname";

    /**
     * Where Litmus's runner and experiment pods run during a drain: on a node
     * it doesn't drain. node-drain evicts every pod on its node, Litmus's own
     * among them, and an evicted experiment uncordons the node and stops, so
     * the fault ends early and Kates waits out the ChaosResult. Litmus asks
     * for the node to be cordoned first instead. A ChaosEngine gives both pods
     * a nodeSelector but no affinity, so they go to one other node by its
     * hostname label: the first by name that is Ready and schedulable and has
     * no NoSchedule or NoExecute taint, since they tolerate none.
     *
     * @throws IllegalStateException when no node qualifies; the fault fails
     *     before the ChaosEngine exists, so nothing is drained
     */
    private Map<String, String> litmusPodsAwayFrom(
            java.util.function.Predicate<io.fabric8.kubernetes.api.model.Node> drained) {
        Map<String, String> away = client.nodes().list().getItems().stream()
                .filter(n -> !drained.test(n) && canRunLitmusPods(n))
                .sorted(Comparator.comparing(n -> n.getMetadata().getName()))
                .map(n -> Map.of(HOSTNAME_LABEL, n.getMetadata().getLabels().get(HOSTNAME_LABEL)))
                .findFirst()
                .orElseThrow(() -> new IllegalStateException("NODE_DRAIN: no node it doesn't drain can run Litmus's"
                        + " runner and experiment pods (Ready, schedulable, without a NoSchedule or NoExecute taint,"
                        + " labelled " + HOSTNAME_LABEL + "), so the drain would evict them"));
        LOG.info("NODE_DRAIN: Litmus's pods run on " + away.get(HOSTNAME_LABEL));
        return away;
    }

    private static boolean canRunLitmusPods(io.fabric8.kubernetes.api.model.Node node) {
        Map<String, String> labels = node.getMetadata().getLabels();
        if (labels == null
                || labels.get(HOSTNAME_LABEL) == null
                || labels.get(HOSTNAME_LABEL).isBlank()) {
            return false;
        }
        var spec = node.getSpec();
        if (spec != null && Boolean.TRUE.equals(spec.getUnschedulable())) {
            return false;
        }
        if (spec != null
                && spec.getTaints() != null
                && spec.getTaints().stream()
                        .anyMatch(t -> "NoSchedule".equals(t.getEffect()) || "NoExecute".equals(t.getEffect()))) {
            return false;
        }
        return node.getStatus() != null
                && node.getStatus().getConditions() != null
                && node.getStatus().getConditions().stream()
                        .anyMatch(c -> "Ready".equals(c.getType()) && "True".equals(c.getStatus()));
    }

    @Override
    @SuppressWarnings("null")
    public ChaosStatus pollStatus(String engineName) {
        try {
            var engineOpt = client
                    .resources(ChaosEngine.class)
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .list()
                    .getItems()
                    .stream()
                    .filter(e -> e.getMetadata().getName().equals(engineName))
                    .findFirst();

            if (engineOpt.isEmpty()) return ChaosStatus.NOT_FOUND;

            ChaosEngine engine = engineOpt.get();
            ChaosEngineStatus status = engine.getStatus();
            if (status == null) return ChaosStatus.PENDING;

            String engineStatus = status.engineStatus != null ? status.engineStatus : "";
            return switch (engineStatus.toLowerCase()) {
                case "completed" -> ChaosStatus.COMPLETED;
                case "stopped" -> ChaosStatus.COMPLETED;
                default -> ChaosStatus.RUNNING;
            };
        } catch (Exception e) {
            return ChaosStatus.NOT_FOUND;
        }
    }

    @Override
    public void cleanup(String engineName) {
        try {
            client.resources(ChaosEngine.class)
                    .inAnyNamespace()
                    .withLabel("managed-by", "kates")
                    .delete();
            LOG.info("Cleaned up Kates-managed ChaosEngines");
        } catch (Exception e) {
            LOG.warn("ChaosEngine cleanup failed", e);
        }
    }

    @Override
    public boolean isAvailable() {
        try {
            client.resources(ChaosEngine.class).inAnyNamespace().list();
            return true;
        } catch (Exception e) {
            LOG.warn("Litmus availability check failed", e);
            return false;
        }
    }
}
