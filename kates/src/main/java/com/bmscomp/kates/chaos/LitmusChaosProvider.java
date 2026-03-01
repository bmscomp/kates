package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.*;
import java.util.concurrent.CompletableFuture;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.inject.Named;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.Watch;
import io.fabric8.kubernetes.client.Watcher;
import io.fabric8.kubernetes.client.WatcherException;
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

    private static final Map<DisruptionType, String> EXPERIMENT_MAP = Map.of(
            DisruptionType.POD_KILL, "pod-delete",
            DisruptionType.POD_DELETE, "pod-delete",
            DisruptionType.CPU_STRESS, "pod-cpu-hog",
            DisruptionType.DISK_FILL, "disk-fill",
            DisruptionType.NETWORK_PARTITION, "pod-network-partition",
            DisruptionType.NETWORK_LATENCY, "pod-network-latency");

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

            // Create engine using strongly typed POJO
            client.resources(ChaosEngine.class)
                    .inNamespace(spec.targetNamespace())
                    .resource(engine)
                    .create();

            LOG.info("Created ChaosEngine: " + engineName);

            String resultName = engineName + "-" + experimentName;

            Watch watch = client.resources(ChaosResult.class)
                    .inNamespace(spec.targetNamespace())
                    .withName(resultName)
                    .watch(new Watcher<>() {
                        @Override
                        @SuppressWarnings("null")
                        public void eventReceived(Action action, ChaosResult result) {
                            if (result == null
                                    || result.getStatus() == null
                                    || result.getStatus().experimentStatus == null) {
                                return;
                            }

                            ChaosResultStatus.ExperimentStatus status = result.getStatus().experimentStatus;
                            String verdict = status.verdict;

                            if (verdict != null && !verdict.equalsIgnoreCase("Awaited")) {
                                String failStep = status.failStep;
                                String probSuccess = status.probeSuccessPercentage;

                                String details = "";
                                if (failStep != null && !failStep.isEmpty()) details += "FailStep: " + failStep;
                                if (probSuccess != null && !probSuccess.isEmpty()) {
                                    details += (details.isEmpty() ? "" : ", ") + "ProbeSuccess: " + probSuccess + "%";
                                }

                                if ("Pass".equalsIgnoreCase(verdict)) {
                                    future.complete(ChaosOutcome.success(
                                            engineName,
                                            experimentName,
                                            start,
                                            Instant.now(),
                                            startNanos,
                                            probSuccess,
                                            failStep,
                                            status.phase));
                                } else {
                                    future.complete(ChaosOutcome.failure(
                                            engineName,
                                            experimentName,
                                            start,
                                            Instant.now(),
                                            startNanos,
                                            "ChaosResult verdict: " + verdict
                                                    + (details.isEmpty() ? "" : " (" + details + ")"),
                                            probSuccess,
                                            failStep,
                                            status.phase));
                                }
                            }
                        }

                        @Override
                        public void onClose(WatcherException cause) {
                            if (!future.isDone()) {
                                if (cause != null) {
                                    LOG.warn("Watcher closed with error", cause);
                                }
                                future.completeExceptionally(
                                        cause != null
                                                ? cause
                                                : new RuntimeException(
                                                        "Watcher closed unexpectedly without providing a verdict"));
                            }
                        }
                    });

            // Clean up the watcher when the future completes (success, failure, or timeout)
            future.whenComplete((res, err) -> watch.close());

            // Fallback timeout execution since we removed Thread.sleep
            CompletableFuture.delayedExecutor(spec.chaosDurationSec() + 120, java.util.concurrent.TimeUnit.SECONDS)
                    .execute(() -> {
                        if (!future.isDone()) {
                            future.complete(ChaosOutcome.failure(
                                    engineName,
                                    experimentName,
                                    start,
                                    Instant.now(),
                                    startNanos,
                                    "Timeout polling for ChaosResult via Watcher",
                                    null,
                                    null,
                                    null));
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

        ChaosEngineSpec.AppInfo appinfo = new ChaosEngineSpec.AppInfo();
        appinfo.appns = spec.targetNamespace();
        appinfo.applabel = spec.targetLabel();
        appinfo.appkind = "statefulset";
        engineSpec.appinfo = appinfo;

        ChaosEngineSpec.Experiment experiment = new ChaosEngineSpec.Experiment();
        experiment.name = experimentName;

        ChaosEngineSpec.Components components = new ChaosEngineSpec.Components();
        List<ChaosEngineSpec.EnvVar> envVars = new ArrayList<>();
        envVars.add(new ChaosEngineSpec.EnvVar("TOTAL_CHAOS_DURATION", String.valueOf(spec.chaosDurationSec())));

        if (spec.targetPod() != null && !spec.targetPod().isEmpty()) {
            envVars.add(new ChaosEngineSpec.EnvVar("TARGET_PODS", spec.targetPod()));
        }

        for (Map.Entry<String, String> e : spec.envOverrides().entrySet()) {
            envVars.add(new ChaosEngineSpec.EnvVar(e.getKey(), e.getValue()));
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
