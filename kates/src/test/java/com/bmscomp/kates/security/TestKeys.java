package com.bmscomp.kates.security;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.HexFormat;

/** Named keys for tests, and a keys file that holds their hashes. */
final class TestKeys {

    static final String LEGACY = "test-secret-key-for-filter-test";
    /** An agent that may only read, on any cluster. */
    static final String AGENT = "kates_agent001_" + "a".repeat(43);
    /** A person who may read and run tests. */
    static final String RUNNER = "kates_runner01_" + "b".repeat(43);
    /** A key in the file that is disabled. */
    static final String DISABLED = "kates_disabled_" + "c".repeat(43);

    private TestKeys() {}

    static String sha256(String key) {
        return HexFormat.of().formatHex(ApiKeys.sha256(key));
    }

    static String file() {
        return """
                keys:
                  - id: agent001
                    name: claude-on-lab
                    type: agent
                    scopes: [read]
                    allowedClusterIds: [lab-cluster]
                    sha256: %s
                  - id: runner01
                    name: perf-team
                    type: human
                    scopes: [read, test:run]
                    sha256: %s
                  - id: disabled
                    name: old-agent
                    type: agent
                    scopes: [read]
                    disabled: true
                    sha256: %s
                """.formatted(sha256(AGENT), sha256(RUNNER), sha256(DISABLED));
    }

    /** Writes text to a new temporary file and returns its path. */
    static Path write(String text) {
        try {
            Path path = Files.createTempFile("kates-api-keys", ".yaml");
            path.toFile().deleteOnExit();
            return Files.writeString(path, text);
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }
}
