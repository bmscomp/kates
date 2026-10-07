package com.bmscomp.kates.security;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.List;
import java.util.Optional;
import jakarta.enterprise.context.ApplicationScoped;

import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Maps a presented API key to the principal that holds it, for REST
 * ({@link ApiKeyIdentityProvider}) and gRPC ({@link GrpcApiKeyInterceptor})
 * alike.
 *
 * <p>The one key there is, {@code kates.api.key}, is the human principal
 * {@value #LEGACY} with every scope, so it keeps the access it always had.
 */
@ApplicationScoped
public class ApiKeys {

    /** The name of the principal that holds {@code kates.api.key}. */
    public static final String LEGACY = "legacy";

    /** Whom a request is when API security is off, as in the dev and test profiles. */
    public static final KatesPrincipal UNSECURED =
            new KatesPrincipal("anonymous", KatesPrincipal.Type.HUMAN, Scopes.ALL, List.of());

    private static final KatesPrincipal LEGACY_PRINCIPAL =
            new KatesPrincipal(LEGACY, KatesPrincipal.Type.HUMAN, Scopes.ALL, List.of());

    @ConfigProperty(name = "kates.api.security-enabled", defaultValue = "true")
    boolean securityEnabled;

    // Optional: SmallRye treats an empty-string property as missing, which
    // would fail plain String injection; SecurityStartupValidator refuses to
    // start with security on and no key.
    @ConfigProperty(name = "kates.api.key")
    Optional<String> legacyKey;

    public boolean securityEnabled() {
        return securityEnabled;
    }

    /** The principal that holds key, or empty when no key matches. */
    public Optional<KatesPrincipal> resolve(String key) {
        String legacy = legacyKey.orElse("");
        if (key == null || key.isBlank() || legacy.isBlank()) {
            return Optional.empty();
        }
        return constantTimeEquals(legacy, key) ? Optional.of(LEGACY_PRINCIPAL) : Optional.empty();
    }

    /**
     * The key a request presents: an {@code Authorization: Bearer} token, or
     * else an {@code X-API-Key} header; null when it presents neither.
     */
    public static String presented(String authorization, String apiKeyHeader) {
        if (authorization != null && authorization.startsWith("Bearer ")) {
            return authorization.substring(7).trim();
        }
        if (apiKeyHeader != null && !apiKeyHeader.isBlank()) {
            return apiKeyHeader.trim();
        }
        return null;
    }

    /**
     * Constant-time comparison: a plain equals() returns at the first byte that
     * differs, so its timing tells a caller how much of a guess was right.
     */
    static boolean constantTimeEquals(String expected, String provided) {
        return MessageDigest.isEqual(
                expected.getBytes(StandardCharsets.UTF_8), provided.getBytes(StandardCharsets.UTF_8));
    }
}
