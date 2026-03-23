package com.bmscomp.kates.security;

import java.io.IOException;
import java.util.Set;

import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.container.PreMatching;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.Provider;

import jakarta.annotation.Priority;
import jakarta.ws.rs.Priorities;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@Provider
@PreMatching
@Priority(Priorities.AUTHENTICATION)
public class ApiKeyAuthFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(ApiKeyAuthFilter.class);

    private static final Set<String> PUBLIC_PREFIXES = Set.of(
            "/api/health",
            "/q/",
            "/openapi");

    @ConfigProperty(name = "kates.api.security-enabled", defaultValue = "true")
    boolean securityEnabled;

    @ConfigProperty(name = "kates.api.key", defaultValue = "")
    String apiKey;

    @Override
    public void filter(ContainerRequestContext ctx) throws IOException {
        if (!securityEnabled) {
            return;
        }

        String path = ctx.getUriInfo().getPath();
        if (isPublicPath(path)) {
            return;
        }

        String token = extractToken(ctx);
        if (token == null || token.isBlank()) {
            LOG.warnf("Unauthenticated request to %s from %s", path, ctx.getHeaderString("X-Forwarded-For"));
            ctx.abortWith(errorResponse(Response.Status.UNAUTHORIZED, "Missing API key",
                    "Provide a token via 'Authorization: Bearer <key>' or 'X-API-Key: <key>' header"));
            return;
        }

        if (apiKey.isBlank() || !apiKey.equals(token)) {
            LOG.warnf("Invalid API key for request to %s", path);
            ctx.abortWith(
                    errorResponse(Response.Status.FORBIDDEN, "Invalid API key", "The provided API key is not valid"));
        }
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
                .entity(String.format("{\"status\":%d,\"error\":\"%s\",\"message\":\"%s\"}", status.getStatusCode(),
                        error, message))
                .build();
    }
}
