package com.bmscomp.kates.api;

import java.net.URI;
import jakarta.annotation.Priority;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.container.PreMatching;
import jakarta.ws.rs.core.UriBuilder;
import jakarta.ws.rs.ext.Provider;

/**
 * Introduces a stable {@code /api/v1} version prefix without touching any
 * resource class. Requests to {@code /api/v1/...} are rewritten to
 * {@code /api/...} before JAX-RS matching, so the existing endpoints are
 * reachable under both paths.
 *
 * This is intentionally non-breaking: the unversioned {@code /api/...} paths
 * keep working, so the Go CLI, the Kubernetes probes ({@code /api/health}),
 * the API-key filter's public prefixes, and all existing tests are unaffected.
 * New/external consumers can adopt {@code /api/v1} and get a versioned contract.
 *
 * Runs at the highest precedence so the rewrite happens before authentication
 * evaluates the path.
 */
@Provider
@PreMatching
@Priority(1)
public class ApiVersionAliasFilter implements ContainerRequestFilter {

    private static final String VERSION_PREFIX = "/api/v1/";
    private static final String TARGET_PREFIX = "/api/";

    @Override
    public void filter(ContainerRequestContext ctx) {
        String path = ctx.getUriInfo().getPath();
        // getPath() is relative to the app base and has no leading slash.
        String normalized = path.startsWith("/") ? path : "/" + path;

        if (normalized.equals("/api/v1") || normalized.equals("/api/v1/")) {
            // Bare version root — nothing to route to; leave it to 404 normally.
            return;
        }
        if (normalized.startsWith(VERSION_PREFIX)) {
            String rewritten = TARGET_PREFIX + normalized.substring(VERSION_PREFIX.length());
            URI newUri = UriBuilder.fromUri(ctx.getUriInfo().getRequestUri())
                    .replacePath(rewritten)
                    .build();
            ctx.setRequestUri(newUri);
        }
    }
}
