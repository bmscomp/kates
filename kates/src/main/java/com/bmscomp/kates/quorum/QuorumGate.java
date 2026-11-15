package com.bmscomp.kates.quorum;

import java.util.Set;
import jakarta.annotation.Priority;
import jakarta.inject.Inject;
import jakarta.ws.rs.Priorities;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.ext.Provider;

import com.bmscomp.kates.api.DrainResource;
import com.bmscomp.kates.engine.NotLeaderException;

/**
 * Only the leader takes a write: on any other member, every request but a read is answered 503
 * with Retry-After, naming the leader. A drain is the exception: the operator sets it on each
 * member, followers included. Runs after authentication, so a caller without a key still gets 401.
 */
@Provider
@Priority(Priorities.USER)
public class QuorumGate implements ContainerRequestFilter {

    private static final Set<String> READS = Set.of("GET", "HEAD", "OPTIONS");

    @Inject
    Quorum quorum;

    @Override
    public void filter(ContainerRequestContext request) {
        if (READS.contains(request.getMethod())
                || request.getUriInfo().getRequestUri().getPath().startsWith("/api/drain")) {
            return;
        }
        if (!quorum.acceptsWrites()) {
            request.abortWith(DrainResource.refused(NotLeaderException.message(quorum.leaderId())));
        }
    }
}
