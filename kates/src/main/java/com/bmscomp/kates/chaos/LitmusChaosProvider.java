package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
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
            Map.entry(DisruptionType.POD_DELETE, "pod-delete"),
            Map.entry(DisruptionType.CPU_STRESS, "pod-cpu-hog"),
            Map.entry(DisruptionType.MEMORY_STRESS, "pod-memory-hog"),
            Map.entry(DisruptionType.IO_STRESS, "pod-io-stress"),
            Map.entry(DisruptionType.DNS_ERROR, "pod-dns-error"),
            Map.entry(DisruptionType.DISK_FILL, "disk-fill"),
            Map.entry(DisruptionType.NETWORK_PARTITION, "pod-network-partition"),
            Map.entry(DisruptionType.NETWORK_LATENCY, "pod-network-latency"),
            Map.entry(DisruptionType.NODE_DRAIN, "node-drain"),
            // Leader election is triggered by deleting the current partition leader pod
            Map.entry(DisruptionType.LEADER_ELECTION, "pod-delete"),
            Map.entry(DisruptionType.SCALE_DOWN, "pod-delete"),
            Map.entry(DisruptionType.ROLLING_RESTART, "pod-delete"));

    @Inject
    KubernetesClient client;

    @Override
    public String name() {
        return "litmus-crd";
    }

    @Override
    public CompletableFuture<ChaosOutcome> triggerFault(FaultSpec spec) {
        Instant start = Instant.now();
        long startNanos = System.nanoTime();
        String engineName = "kates-" + spec.experimentName() + "-" + System.currentTimeMillis();

        CompletableFuture<ChaosOutcome> future = new CompletableFuture<>();

        try {
            String experimentName = resolveExperiment(spec);

            ChaosEngine engine = buildChaosEngine(spec, engineName, experimentName);
            String resultName = engineName + "-" + experimentName;

            // NOW create the engine
            client.resources(ChaosEngine.class)
                    .inNamespace(spec.targetNamespace())
                    .resource(engine)
                    .create();

            LOG.info("Created ChaosEngine: " + engineName);
            LOG.info("Polling ChaosResult: " + resultName + " in ns=" + spec.targetNamespace());

            // Simple polling loop — more reliable than Fabric8 watchers on CRDs
            int timeoutSec = spec.chaosDurationSec() + 120;
            int pollIntervalMs = 5_000;
            int maxPolls = (timeoutSec * 1000) / pollIntervalMs;

            CompletableFuture.runAsync(() -> {
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

                        if (existing.getStatus() == null
                                || existing.getStatus().experimentStatus == null) {
                            LOG.debugf("Poll %d: ChaosResult exists but no status yet", i + 1);
                            continue;
                        }

                        String v = existing.getStatus().experimentStatus.verdict;
                        LOG.infof("Poll %d: ChaosResult verdict=%s", i + 1, v);

                        if (v != null && !v.equalsIgnoreCase("Awaited")) {
                            var s = existing.getStatus().experimentStatus;
                            if ("Pass".equalsIgnoreCase(v)) {
                                future.complete(ChaosOutcome.success(
                                        engineName, experimentName, start, Instant.now(),
                                        startNanos, s.probeSuccessPercentage, s.failStep, s.phase));
                            } else {
                                future.complete(ChaosOutcome.failure(
                                        engineName, experimentName, start, Instant.now(),
                                        startNanos, "ChaosResult verdict: " + v,
                                        s.probeSuccessPercentage, s.failStep, s.phase));
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
                            engineName, experimentName, start, Instant.now(),
                            startNanos, "Timeout polling for ChaosResult after " + timeoutSec + "s",
                            null, null, null));
                }
            });

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

    private ChaosEngine buildChaosEngine(FaultSpec spec, String engineName, String experimentName) {
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

        if (spec.targetPod() != null && !spec.targetPod().isEmpty()) {
            envVars.add(new ChaosEngineSpec.EnvVar("TARGET_PODS", spec.targetPod()));
        } else if (spec.targetLabel() != null && !spec.targetLabel().isEmpty()) {
            String[] parts = spec.targetLabel().split("=", 2);
            if (parts.length == 2) {
                var pods = client.pods()
                        .inNamespace(spec.targetNamespace())
                        .withLabel(parts[0], parts[1])
                        .list()
                        .getItems();
                if (!pods.isEmpty()) {
                    int index = (int) (Math.random() * pods.size());
                    String podName = pods.get(index).getMetadata().getName();
                    envVars.add(new ChaosEngineSpec.EnvVar("TARGET_PODS", podName));
                }
            }
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
                // that triggers the StrimziPodSet lookup failure.
                case POD_KILL, POD_DELETE, LEADER_ELECTION, SCALE_DOWN, ROLLING_RESTART -> {
                    envVars.add(new ChaosEngineSpec.EnvVar("FORCE", "true"));
                    envVars.add(new ChaosEngineSpec.EnvVar("SEQUENCE", "serial"));
                }
                case CPU_STRESS -> envVars.add(new ChaosEngineSpec.EnvVar("CPU_CORES", String.valueOf(spec.cpuCores())));
                case MEMORY_STRESS -> {
                    envVars.add(new ChaosEngineSpec.EnvVar("MEMORY_CONSUMPTION", String.valueOf(spec.memoryMb())));
                    envVars.add(new ChaosEngineSpec.EnvVar("NUMBER_OF_WORKERS", "1"));
                }
                case IO_STRESS -> {
                    envVars.add(new ChaosEngineSpec.EnvVar("FILESYSTEM_UTILIZATION_PERCENTAGE", String.valueOf(spec.fillPercentage())));
                    envVars.add(new ChaosEngineSpec.EnvVar("NUMBER_OF_WORKERS", String.valueOf(spec.ioWorkers())));
                }
                case DNS_ERROR -> {
                    if (spec.targetTopic() != null && !spec.targetTopic().isEmpty()) {
                        envVars.add(new ChaosEngineSpec.EnvVar("TARGET_HOSTNAMES", spec.targetTopic()));
                    }
                }
                case NETWORK_LATENCY -> envVars.add(new ChaosEngineSpec.EnvVar("NETWORK_LATENCY", String.valueOf(spec.networkLatencyMs())));
                case DISK_FILL -> envVars.add(new ChaosEngineSpec.EnvVar("FILL_PERCENTAGE", String.valueOf(spec.fillPercentage())));
                default -> { }
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
