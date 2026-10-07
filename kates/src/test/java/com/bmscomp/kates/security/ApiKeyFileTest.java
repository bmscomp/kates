package com.bmscomp.kates.security;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;
import java.util.List;

import org.junit.jupiter.api.Test;

class ApiKeyFileTest {

    private static final String HASH = "0".repeat(64);

    private static String entry(String fields) {
        return "keys:\n  - " + fields.strip().replace("\n", "\n    ") + "\n";
    }

    private static String problems(String text) {
        return assertThrows(ApiKeyFile.InvalidKeysFile.class, () -> ApiKeyFile.parse(text))
                .getMessage();
    }

    @Test
    void entriesParseByIdWithTheirPrincipals() throws Exception {
        var entries = ApiKeyFile.parse(TestKeys.file());
        assertEquals(List.of("agent001", "runner01", "disabled"), List.copyOf(entries.keySet()));
        var agent = entries.get("agent001").principal();
        assertEquals("claude-on-lab", agent.name());
        assertEquals(KatesPrincipal.Type.AGENT, agent.type());
        assertEquals(List.of("read"), agent.scopes());
        assertEquals(List.of("lab-cluster"), agent.allowedClusterIds());
        assertTrue(entries.get("disabled").disabled());
        assertEquals(List.of(), entries.get("runner01").principal().allowedClusterIds());
    }

    @Test
    void expiryIsAnInstant() throws Exception {
        var entries = ApiKeyFile.parse(entry("""
                id: abcd1234
                name: temp
                type: human
                scopes: [read]
                expiresAt: 2026-12-31T00:00:00Z
                sha256: %s
                """.formatted(HASH)));
        assertEquals(
                Instant.parse("2026-12-31T00:00:00Z"), entries.get("abcd1234").expiresAt());
    }

    @Test
    void anAgentMayOnlyRead() {
        String message = problems(entry("""
                id: abcd1234
                name: bot
                type: agent
                scopes: [read, test:run, admin]
                sha256: %s
                """.formatted(HASH)));
        assertTrue(message.contains("keys[0].scopes has test:run, which an agent's key may not carry"), message);
        assertTrue(message.contains("keys[0].scopes has admin, which an agent's key may not carry"), message);
    }

    @Test
    void everyProblemIsNamed() {
        String message = problems("""
                keys:
                  - id: AB
                    name: legacy
                    type: robot
                    scopes: [read, read, write]
                    allowedClusterIds: [" "]
                    expiresAt: tomorrow
                    sha256: xyz
                  - id: abcd1234
                    name: a
                    type: human
                    scopes: []
                    sha256: %s
                  - id: abcd1234
                    name: a
                    type: human
                    scopes: [read]
                    sha256: %s
                """.formatted(HASH, HASH));
        for (String want : List.of(
                "keys[0].id must be 4 to 32 lower-case letters or digits",
                "keys[0].name legacy is reserved",
                "keys[0].type must be human or agent",
                "keys[0].scopes has write, which is not one of",
                "keys[0].scopes names a scope twice",
                "keys[0].allowedClusterIds has a value that is not a clusterId",
                "keys[0].expiresAt must be an ISO-8601 instant",
                "keys[0].sha256 must be the key's SHA-256",
                "keys[1].scopes must name at least one scope",
                "keys[2].id abcd1234 is used twice",
                "keys[2].name a is used twice")) {
            assertTrue(message.contains(want), "lacks " + want + ":\n" + message);
        }
    }

    @Test
    void aMisspeltFieldIsRefusedRatherThanIgnored() {
        // "scope" for "scopes" would otherwise leave a key with no scopes, or,
        // for "disable", a key the operator meant to switch off.
        assertTrue(problems(entry("""
                id: abcd1234
                name: a
                type: human
                scopes: [read]
                disable: true
                sha256: %s
                """.formatted(HASH))).contains("not YAML of the expected shape"));
        assertTrue(problems("nothing: here").contains("not YAML of the expected shape"));
        assertTrue(problems("keys:").contains("no 'keys' list"));
    }

    @Test
    void theKeyShapeNamesItsId() {
        var m = ApiKeyFile.KEY.matcher(TestKeys.AGENT);
        assertTrue(m.matches());
        assertEquals("agent001", m.group(1));
        assertFalse(ApiKeyFile.KEY.matcher("kates_agent001_short").matches());
        assertFalse(ApiKeyFile.KEY.matcher("not-a-named-key").matches());
    }
}
