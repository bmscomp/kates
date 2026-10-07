package com.bmscomp.kates.security;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.List;
import java.util.Optional;

import org.junit.jupiter.api.Test;

class ApiKeysTest {

    private static ApiKeys keys(String legacy, Path keysFile) {
        ApiKeys keys = new ApiKeys();
        keys.securityEnabled = true;
        keys.legacyKey = Optional.ofNullable(legacy);
        keys.legacyScopes = Optional.empty();
        keys.keysFile = Optional.ofNullable(keysFile).map(Path::toString);
        keys.load(true);
        return keys;
    }

    private static ApiKeys keys(String legacy) {
        return keys(legacy, null);
    }

    @Test
    void theLegacyKeyIsAHumanWithEveryScope() {
        KatesPrincipal p = keys("s3cret").resolve("s3cret").orElseThrow();
        assertEquals(ApiKeys.LEGACY, p.name());
        assertEquals(KatesPrincipal.Type.HUMAN, p.type());
        assertEquals(Scopes.ALL, p.scopes());
        assertTrue(p.allowedClusterIds().isEmpty());
    }

    @Test
    void theLegacyKeysScopesCanBeLowered() {
        ApiKeys keys = keys("s3cret");
        keys.legacyScopes = Optional.of(List.of(Scopes.READ, Scopes.TEST_RUN));
        assertEquals(
                List.of("read", "test:run"),
                keys.resolve("s3cret").orElseThrow().scopes());
    }

    @Test
    void noOtherKeyResolves() {
        assertTrue(keys("s3cret").resolve("s3cre").isEmpty());
        assertTrue(keys("s3cret").resolve("s3cret ").isEmpty());
        assertTrue(keys("s3cret").resolve("").isEmpty());
        assertTrue(keys("s3cret").resolve(null).isEmpty());
        // No key configured: nothing resolves, not even an empty key.
        assertTrue(keys(null).resolve("").isEmpty());
        assertTrue(keys("").resolve("").isEmpty());
    }

    @Test
    void aNamedKeyIsItsEntrysPrincipal() {
        ApiKeys keys = keys(TestKeys.LEGACY, TestKeys.write(TestKeys.file()));
        KatesPrincipal agent = keys.resolve(TestKeys.AGENT).orElseThrow();
        assertEquals("claude-on-lab", agent.name());
        assertEquals(KatesPrincipal.Type.AGENT, agent.type());
        assertEquals(List.of("read"), agent.scopes());
        assertEquals(
                List.of("read", "test:run"),
                keys.resolve(TestKeys.RUNNER).orElseThrow().scopes());
        // The legacy key still works beside them.
        assertEquals(ApiKeys.LEGACY, keys.resolve(TestKeys.LEGACY).orElseThrow().name());
    }

    @Test
    void aNamedKeyMustMatchItsHashAndBeUsable() {
        ApiKeys keys = keys(TestKeys.LEGACY, TestKeys.write(TestKeys.file()));
        // The right id with another secret.
        assertTrue(keys.resolve("kates_agent001_" + "z".repeat(43)).isEmpty());
        // An id no entry has.
        assertTrue(keys.resolve("kates_nobody00_" + "a".repeat(43)).isEmpty());
        assertTrue(keys.resolve(TestKeys.DISABLED).isEmpty());
    }

    @Test
    void anExpiredKeyNoLongerResolves() throws Exception {
        String key = "kates_expiring_" + "d".repeat(43);
        ApiKeys keys = keys(null, TestKeys.write("""
                keys:
                  - id: expiring
                    name: temp
                    type: human
                    scopes: [read]
                    expiresAt: 2026-10-08T00:00:00Z
                    sha256: %s
                """.formatted(TestKeys.sha256(key))));
        keys.clock = () -> Instant.parse("2026-10-07T23:59:59Z");
        assertTrue(keys.resolve(key).isPresent());
        keys.clock = () -> Instant.parse("2026-10-08T00:00:00Z");
        assertTrue(keys.resolve(key).isEmpty());
    }

    @Test
    void aChangedFileIsReadAgainAndABrokenOneKeepsTheKeysBeforeIt() throws Exception {
        Path file = TestKeys.write(TestKeys.file());
        ApiKeys keys = keys(null, file);
        assertTrue(keys.resolve(TestKeys.AGENT).isPresent());

        // The agent's entry removed: revoked at the next read.
        Files.writeString(file, TestKeys.file().replaceFirst("(?s)  - id: agent001.*?(?=  - id: runner01)", ""));
        keys.load(false);
        assertTrue(keys.resolve(TestKeys.AGENT).isEmpty());
        assertTrue(keys.resolve(TestKeys.RUNNER).isPresent());

        // A file that does not parse leaves the keys as they were.
        Files.writeString(file, "keys: [{id: x}]");
        keys.load(false);
        assertTrue(keys.resolve(TestKeys.RUNNER).isPresent());
    }

    @Test
    void aBrokenFileStopsTheStart() {
        Path broken = TestKeys.write("keys: [{id: x}]");
        var e = assertThrows(IllegalStateException.class, () -> keys(null, broken));
        assertTrue(e.getMessage().contains("is not valid, so the Kates API does not start"), e.getMessage());
        Path missing = broken.resolveSibling("no-such-keys-file.yaml");
        assertThrows(IllegalStateException.class, () -> keys(null, missing));
    }

    @Test
    void aKeyIsReadFromBearerFirstThenXApiKey() {
        assertEquals("abc", ApiKeys.presented("Bearer abc ", "xyz"));
        assertEquals("xyz", ApiKeys.presented(null, " xyz "));
        assertEquals("xyz", ApiKeys.presented("Basic abc", "xyz"));
        assertNull(ApiKeys.presented(null, " "));
        assertNull(ApiKeys.presented("Basic abc", null));
    }
}
