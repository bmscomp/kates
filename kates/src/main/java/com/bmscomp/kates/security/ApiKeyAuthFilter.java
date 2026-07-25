package com.bmscomp.kates.security;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Set;
import jakarta.annotation.Priority;
import jakarta.ws.rs.Priorities;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.container.PreMatching;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.Provider;

import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@Provider
@PreMatching
@Priority(Priorities.AUTHENTICATION)
public class ApiKeyAuthFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(ApiKeyAuthFilter.class);

    /**
     * Unauthenticated paths. The blanket {@code /q/} prefix was too broad: it
     * exposed every Quarkus management endpoint under that root — including
     * dev-ui and any extension that mounts there — not just the probes and
     * metrics Kubernetes and Prometheus actually need.
     */
    private static final Set<String> PUBLIC_PREFIXES = Set.of("/api/health", "/q/health", "/q/metrics", "/openapi");

    private boolean isSecurityEnabled() {
        return ConfigProvider.getConfig()
                .getOptionalValue("kates.api.security-enabled", Boolean.class)
                .orElse(true);
    }

    private String getApiKey() {
        return ConfigProvider.getConfig()
                .getOptionalValue("kates.api.key", String.class)
                .orElse("");
    }

    @Override
    public void filter(ContainerRequestContext ctx) throws IOException {
        if (!isSecurityEnabled()) {
            return;
        }

        String path = ctx.getUriInfo().getPath();
        if (isPublicPath(path)) {
            return;
        }

        String token = extractToken(ctx);
        if (token == null || token.isBlank()) {
            LOG.warnf("Unauthenticated request to %s from %s", path, ctx.getHeaderString("X-Forwarded-For"));
            ctx.abortWith(errorResponse(
                    Response.Status.UNAUTHORIZED,
                    "Missing API key",
                    "Provide a token via 'Authorization: Bearer <key>' or 'X-API-Key: <key>' header"));
            return;
        }

        String apiKey = getApiKey();
        if (apiKey.isBlank() || !constantTimeEquals(apiKey, token)) {
            LOG.warnf("Invalid API key for request to %s", path);
            ctx.abortWith(
                    errorResponse(Response.Status.FORBIDDEN, "Invalid API key", "The provided API key is not valid"));
        }
    }

    /**
     * Constant-time comparison — a plain equals() short-circuits on the first
     * differing byte, leaking key prefix length via response timing.
     */
    private static boolean constantTimeEquals(String expected, String provided) {
        return MessageDigest.isEqual(
                expected.getBytes(StandardCharsets.UTF_8), provided.getBytes(StandardCharsets.UTF_8));
    }

    private boolean isPublicPath(String path) {
        for (String prefix : PUBLIC_PREFIXES) {
            if (path.startsWith(prefix)) {
                return true;
            }
        }
        return false;
    }

    private String extractToken(ContainerRequestContext ctx) {
        String authHeader = ctx.getHeaderString("Authorization");
        if (authHeader != null && authHeader.startsWith("Bearer ")) {
            return authHeader.substring(7).trim();
        }
        String apiKeyHeader = ctx.getHeaderString("X-API-Key");
        if (apiKeyHeader != null && !apiKeyHeader.isBlank()) {
            return apiKeyHeader.trim();
        }
        return null;
    }

    private Response errorResponse(Response.Status status, String error, String message) {
        return Response.status(status)
                .type(MediaType.APPLICATION_JSON)
                .entity(String.format(
                        "{\"status\":%d,\"error\":\"%s\",\"message\":\"%s\"}", status.getStatusCode(), error, message))
                .build();
    }
}
