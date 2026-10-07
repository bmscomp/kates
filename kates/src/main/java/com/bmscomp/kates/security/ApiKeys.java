package com.bmscomp.kates.security;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.function.Supplier;
import java.util.regex.Matcher;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;

import io.quarkus.runtime.StartupEvent;
import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Maps a presented API key to the principal that holds it, for REST
 * ({@link ApiKeyIdentityProvider}) and gRPC ({@link GrpcApiKeyInterceptor})
 * alike.
 *
 * <p>Two kinds of key:
 *
 * <ul>
 *   <li>named keys, {@code kates_<id>_<secret>}, from the file
 *       {@code kates.api.keys-file} names ({@link ApiKeyFile}). The file is
 *       read at start, and again every 30 seconds, so a key added, disabled
 *       or removed there takes effect without a restart. A file that does not
 *       parse stops the start, and later leaves the keys loaded before it.
 *   <li>{@code kates.api.key}, the human principal {@value #LEGACY}, with
 *       every scope unless {@code kates.api.legacy-key.scopes} lowers them.
 * </ul>
 */
@ApplicationScoped
public class ApiKeys {

    private static final Logger LOG = Logger.getLogger(ApiKeys.class);

    /** The name of the principal that holds {@code kates.api.key}. */
    public static final String LEGACY = "legacy";

    /** Whom a request is when API security is off, as in the dev and test profiles. */
    public static final KatesPrincipal UNSECURED =
            new KatesPrincipal("anonymous", KatesPrincipal.Type.HUMAN, Scopes.ALL, List.of());

    @ConfigProperty(name = "kates.api.security-enabled", defaultValue = "true")
    boolean securityEnabled;

    // Optional: SmallRye treats an empty-string property as missing, which
    // would fail plain String injection; SecurityStartupValidator refuses to
    // start with security on and no key at all.
    @ConfigProperty(name = "kates.api.key")
    Optional<String> legacyKey;

    @ConfigProperty(name = "kates.api.legacy-key.scopes")
    Optional<List<String>> legacyScopes;

    @ConfigProperty(name = "kates.api.keys-file")
    Optional<String> keysFile;

    Supplier<Instant> clock = Instant::now;

    private volatile Map<String, ApiKeyFile.Entry> named = Map.of();
    private String loadedText;

    void onStart(@Observes StartupEvent event) {
        if (!securityEnabled) {
            return;
        }
        for (String scope : legacyScopes.orElse(List.of())) {
            if (!Scopes.ALL.contains(scope)) {
                throw new IllegalStateException(
                        "kates.api.legacy-key.scopes has " + scope + ", which is not one of " + Scopes.ALL);
            }
        }
        load(true);
    }

    @Scheduled(every = "30s", delayed = "30s", concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void reload() {
        if (securityEnabled) {
            load(false);
        }
    }

    /** Reads the keys file, if one is set, when it has changed. */
    synchronized void load(boolean atStart) {
        if (keysFile.isEmpty() || keysFile.get().isBlank()) {
            return;
        }
        String path = keysFile.get();
        String text;
        try {
            text = Files.readString(Path.of(path));
        } catch (IOException e) {
            failLoad(atStart, "cannot read the API keys file " + path + ": " + e.getMessage());
            return;
        }
        if (text.equals(loadedText)) {
            return;
        }
        try {
            named = ApiKeyFile.parse(text);
            loadedText = text;
            LOG.infof("Loaded %d named API keys from %s", named.size(), path);
        } catch (ApiKeyFile.InvalidKeysFile e) {
            failLoad(
                    atStart,
                    "the API keys file " + path + " is not valid, so "
                            + (atStart ? "the Kates API does not start" : "the keys loaded before it stay")
                            + ":\n" + e.getMessage());
        }
    }

    private static void failLoad(boolean atStart, String message) {
        if (atStart) {
            throw new IllegalStateException(message);
        }
        LOG.error(message);
    }

    public boolean securityEnabled() {
        return securityEnabled;
    }

    /** Whether a named keys file is set, which lets the Kates API run without kates.api.key. */
    public boolean hasKeysFile() {
        return keysFile.isPresent() && !keysFile.get().isBlank();
    }

    /** The principal that holds key, or empty when no key matches. */
    public Optional<KatesPrincipal> resolve(String key) {
        if (key == null || key.isBlank()) {
            return Optional.empty();
        }
        Matcher m = ApiKeyFile.KEY.matcher(key);
        if (m.matches()) {
            ApiKeyFile.Entry entry = named.get(m.group(1));
            if (entry != null && MessageDigest.isEqual(sha256(key), entry.sha256())) {
                return usable(m.group(1), entry) ? Optional.of(entry.principal()) : Optional.empty();
            }
        }
        String legacy = legacyKey.orElse("");
        if (legacy.isBlank() || !constantTimeEquals(legacy, key)) {
            return Optional.empty();
        }
        return Optional.of(
                new KatesPrincipal(LEGACY, KatesPrincipal.Type.HUMAN, legacyScopes.orElse(Scopes.ALL), List.of()));
    }

    private boolean usable(String id, ApiKeyFile.Entry entry) {
        String name = entry.principal().name();
        if (entry.disabled()) {
            LOG.warnf("API key %s of %s is disabled", id, name);
            return false;
        }
        if (entry.expiresAt() != null && !clock.get().isBefore(entry.expiresAt())) {
            LOG.warnf("API key %s of %s expired at %s", id, name, entry.expiresAt());
            return false;
        }
        return true;
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

    static byte[] sha256(String key) {
        try {
            return MessageDigest.getInstance("SHA-256").digest(key.getBytes(StandardCharsets.UTF_8));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is missing from this JVM", e);
        }
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
