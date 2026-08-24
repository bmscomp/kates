package com.bmscomp.kates.disruption;

import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.jboss.logging.Logger;

/**
 * The single way a disruption plan gets started.
 *
 * <p>Every caller needs the same four things — reject while another plan holds
 * the cluster, validate against the safety guard, persist a RUNNING placeholder
 * so an immediate GET resolves, then run the plan in the background — and each
 * endpoint that grew its own copy got a different subset. The playbook endpoint
 * in particular ran plans with no safety validation at all, and held the request
 * thread for the full duration of the plan (minutes), which is longer than most
 * load balancers keep a connection open.
 */
@ApplicationScoped
public class DisruptionLauncher {

    private static final Logger LOG = Logger.getLogger(DisruptionLauncher.class);

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionSafetyGuard safetyGuard;

    @Inject
    DisruptionConcurrencyGuard concurrencyGuard;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    ObjectMapper objectMapper;

    @Inject
    com.bmscomp.kates.engine.KatesExecutor executor;

    public enum Status {
        /** Running in the background; poll {@code GET /api/disruptions/{id}}. */
        ACCEPTED,
        /** Refused by the safety guard before anything was touched. */
        REJECTED,
        /** Another plan is already running against this cluster. */
        CONFLICT
    }

    /**
     * @param id the report id, absent only for CONFLICT (nothing was recorded)
     * @param messages validation warnings for ACCEPTED, the reasons for REJECTED
     *     or CONFLICT
     */
    public record LaunchResult(Status status, String id, List<String> messages) {

        public boolean accepted() {
            return status == Status.ACCEPTED;
        }
    }

    public LaunchResult launch(DisruptionPlan plan) {
        String target = concurrencyGuard.currentTarget();
        if (concurrencyGuard.isBusy(target)) {
            return new LaunchResult(
                    Status.CONFLICT,
                    null,
                    List.of("A disruption plan is already running against " + target
                            + ". Wait for it to finish before starting another."));
        }

        String id = UUID.randomUUID().toString().substring(0, 8);

        // Validate up front so an unsafe plan fails fast rather than turning into
        // an accepted run the caller has to poll to discover was rejected. The
        // orchestrator re-validates before touching the cluster.
        DisruptionSafetyGuard.ValidationResult validation = safetyGuard.validatePlan(plan);
        if (!validation.safe()) {
            List<String> combined = new ArrayList<>(validation.warnings());
            combined.addAll(validation.errors().stream().map(e -> "ERROR: " + e).toList());

            DisruptionReport rejected = new DisruptionReport();
            rejected.setPlanName(plan.getName());
            rejected.setStatus("REJECTED");
            rejected.setValidationWarnings(combined);
            DisruptionPersistence.persistReport(id, rejected, repository, objectMapper);
            return new LaunchResult(Status.REJECTED, id, combined);
        }

        // Persist a RUNNING placeholder BEFORE returning so an immediate GET on
        // the returned id resolves instead of 404-ing, and so a pod that dies
        // mid-plan leaves a row the startup reconciler can find.
        DisruptionReport pending = new DisruptionReport();
        pending.setPlanName(plan.getName());
        pending.setStatus("RUNNING");
        pending.setValidationWarnings(validation.warnings());
        DisruptionPersistence.persistReport(id, pending, repository, objectMapper);

        LOG.info("Accepted disruption plan '" + plan.getName() + "' with ID: " + id);
        executor.get().execute(() -> runPlan(id, plan));

        return new LaunchResult(Status.ACCEPTED, id, validation.warnings());
    }

    private void runPlan(String id, DisruptionPlan plan) {
        try {
            DisruptionReport report = orchestrator.execute(plan);
            DisruptionPersistence.persistReport(id, report, repository, objectMapper);
        } catch (Exception e) {
            LOG.error("Disruption plan failed: " + id, e);
            DisruptionReport failed = new DisruptionReport();
            failed.setPlanName(plan.getName());
            failed.setStatus("FAILED");
            failed.setValidationWarnings(List.of("Execution error: " + e.getMessage()));
            DisruptionPersistence.persistReport(id, failed, repository, objectMapper);
        }
    }
}
