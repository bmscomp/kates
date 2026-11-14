package com.bmscomp.kates.api;

import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PUT;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.audit.Audited;
import com.bmscomp.kates.disruption.DisruptionConcurrencyGuard;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.security.Scopes;

/**
 * The drain switch. While it is set the Kates API starts no run and no fault,
 * and lets the live ones finish, so that whoever replaces its pod can wait
 * until nothing is lost by it.
 *
 * <p>The switch is kept in memory: a restarted pod is not draining, and its
 * runs are gone anyway. Whoever set it sets it again.
 */
@RolesAllowed(Scopes.READ)
@Path("/api/drain")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Health")
public class DrainResource {

    /** As a full engine answers: the same request can succeed later. */
    private static final int RETRY_AFTER_SECONDS = 60;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    DisruptionConcurrencyGuard faults;

    @Inject
    com.bmscomp.kates.service.AuditService audit;

    @GET
    @Operation(
            summary = "Whether the API is draining",
            description = "draining says whether new runs and faults are refused. liveRuns counts the test runs"
                    + " still going, and faultsInFlight the disruption plans, compound runs and resilience tests"
                    + " between their request and their saved report. drained is true once draining is set, both"
                    + " read zero and no fault holds the disruption lease: the pod can then be replaced without"
                    + " losing a run or leaving a fault in.")
    @APIResponse(responseCode = "200", description = "The drain state")
    public DrainStatus status() {
        // The flag is read before the counts, so a drained answer is never
        // built from counts older than the drain.
        boolean draining = orchestrator.isDraining() && faults.isDraining();
        int liveRuns = orchestrator.liveRuns();
        int faultsInFlight = faults.inFlight();
        // A fault that outlived its request keeps the lease until it is undone.
        boolean leased = faults.isBusy(faults.currentTarget());
        return new DrainStatus(
                draining, liveRuns, faultsInFlight, draining && liveRuns == 0 && faultsInFlight == 0 && !leased);
    }

    @PUT
    @RolesAllowed(Scopes.ADMIN)
    @Audited(action = "UPDATE", type = "drain")
    @Consumes(MediaType.APPLICATION_JSON)
    @Operation(
            summary = "Start or lift a drain",
            description = "With draining true, every request that would start a test run, a disruption, a compound"
                    + " run or a resilience test is answered 503 with Retry-After, schedules stop firing, and"
                    + " live work goes on to its end. A full engine still answers 429, and POST /api/tests/bulk"
                    + " answers 202 with every item failed. A dry run, a cancel and every read still work. With"
                    + " draining false the API starts work again. The switch is not stored: a restarted pod is"
                    + " not draining.")
    @APIResponse(responseCode = "200", description = "The drain state after the change")
    @APIResponse(responseCode = "400", description = "No draining field")
    // synchronized: two flags, and two callers must not leave them apart.
    public synchronized Response set(DrainRequest request) {
        if (request == null || request.draining() == null) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "draining must be true or false"))
                    .build();
        }
        orchestrator.setDraining(request.draining());
        faults.setDraining(request.draining());
        audit.record("UPDATE", "drain", "api/drain", "draining=" + request.draining());
        return Response.ok(status()).build();
    }

    /** The answer to a request that would start work during a drain. */
    public static Response refused(String message) {
        return Response.status(503)
                .header("Retry-After", RETRY_AFTER_SECONDS)
                .entity(ApiError.of(503, "Service Unavailable", message))
                .build();
    }

    public record DrainRequest(Boolean draining) {}

    public record DrainStatus(boolean draining, int liveRuns, int faultsInFlight, boolean drained) {}
}
