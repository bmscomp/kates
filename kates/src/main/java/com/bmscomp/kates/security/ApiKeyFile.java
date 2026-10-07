package com.bmscomp.kates.security;

import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.dataformat.yaml.YAMLFactory;

/**
 * The named API keys file ({@code kates.api.keys-file}): one entry per key,
 * holding the SHA-256 of the key and never the key itself.
 *
 * <pre>
 * keys:
 *   - id: 3f9a1c2e                # the &lt;id&gt; of kates_&lt;id&gt;_&lt;secret&gt;
 *     name: claude-on-lab         # the principal, as the audit log names it
 *     type: agent                 # human or agent
 *     scopes: [read]
 *     allowedClusterIds: [...]    # optional; absent means any cluster
 *     expiresAt: 2026-12-31T00:00:00Z   # optional
 *     disabled: false             # optional
 *     sha256: &lt;64 hex characters: the SHA-256 of the whole key&gt;
 * </pre>
 *
 * <p>A key is {@code kates_<id>_<secret>}: the id finds the entry, and the
 * key's SHA-256 must match. Keys are long random strings, so a fast hash is
 * enough; a slow password hash would only slow every request.
 *
 * <p>Until the agent policy exists (plans/mcp-server.md §5.5), an agent's key
 * may carry only {@code read} and {@code read:sensitive}: no load, no fault,
 * no change. {@code chaos:approve}, {@code chaos:run} and {@code admin} are
 * never an agent's.
 */
final class ApiKeyFile {

    /** A key's shape: kates_, an id of 4 to 32 lower-case letters or digits, _, and the secret. */
    static final Pattern KEY = Pattern.compile("^kates_([a-z0-9]{4,32})_(.{16,})$");

    private static final Pattern ID = Pattern.compile("^[a-z0-9]{4,32}$");
    private static final Pattern NAME = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$");
    private static final Pattern SHA256 = Pattern.compile("^[0-9a-f]{64}$");
    private static final Set<String> RESERVED_NAMES = Set.of(ApiKeys.LEGACY, ApiKeys.UNSECURED.name());
    static final Set<String> AGENT_SCOPES = Set.of(Scopes.READ, Scopes.READ_SENSITIVE);

    private static final ObjectMapper YAML =
            new ObjectMapper(new YAMLFactory()).enable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES);

    /** One key of the file. */
    record Entry(KatesPrincipal principal, Instant expiresAt, boolean disabled, byte[] sha256) {}

    /** What the file says is wrong with it, every problem on its own line. */
    static final class InvalidKeysFile extends Exception {
        InvalidKeysFile(String message) {
            super(message);
        }
    }

    // Read by Jackson.
    static final class FileModel {
        public List<EntryModel> keys;
    }

    static final class EntryModel {
        public String id;
        public String name;
        public String type;
        public List<String> scopes;
        public List<String> allowedClusterIds;
        public String expiresAt;
        public boolean disabled;
        public String sha256;
    }

    private ApiKeyFile() {}

    /** The entries of a keys file, by id. */
    static Map<String, Entry> parse(String text) throws InvalidKeysFile {
        FileModel file;
        try {
            file = YAML.readValue(text, FileModel.class);
        } catch (Exception e) {
            throw new InvalidKeysFile("the keys file is not YAML of the expected shape: " + e.getMessage());
        }
        if (file == null || file.keys == null) {
            throw new InvalidKeysFile("the keys file has no 'keys' list");
        }
        List<String> problems = new ArrayList<>();
        Map<String, Entry> entries = new LinkedHashMap<>();
        Set<String> ids = new HashSet<>();
        Set<String> names = new HashSet<>();
        for (int i = 0; i < file.keys.size(); i++) {
            EntryModel m = file.keys.get(i);
            String at = "keys[" + i + "]";
            if (m == null) {
                problems.add(at + " is empty");
                continue;
            }
            int before = problems.size();
            if (m.id == null || !ID.matcher(m.id).matches()) {
                problems.add(at + ".id must be 4 to 32 lower-case letters or digits");
            } else if (!ids.add(m.id)) {
                problems.add(at + ".id " + m.id + " is used twice");
            }
            if (m.name == null || !NAME.matcher(m.name).matches()) {
                problems.add(
                        at + ".name must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit");
            } else if (RESERVED_NAMES.contains(m.name)) {
                problems.add(at + ".name " + m.name + " is reserved");
            } else if (!names.add(m.name)) {
                problems.add(at + ".name " + m.name + " is used twice");
            }
            KatesPrincipal.Type type = null;
            if ("human".equals(m.type)) {
                type = KatesPrincipal.Type.HUMAN;
            } else if ("agent".equals(m.type)) {
                type = KatesPrincipal.Type.AGENT;
            } else {
                problems.add(at + ".type must be human or agent");
            }
            List<String> scopes = m.scopes == null ? List.of() : m.scopes;
            if (scopes.isEmpty()) {
                problems.add(at + ".scopes must name at least one scope");
            }
            for (String scope : scopes) {
                if (!Scopes.ALL.contains(scope)) {
                    problems.add(at + ".scopes has " + scope + ", which is not one of " + Scopes.ALL);
                } else if (type == KatesPrincipal.Type.AGENT && !AGENT_SCOPES.contains(scope)) {
                    problems.add(at + ".scopes has " + scope + ", which an agent's key may not carry: until the"
                            + " agent policy exists, an agent may only " + Scopes.READ + " and "
                            + Scopes.READ_SENSITIVE);
                }
            }
            if (new HashSet<>(scopes).size() != scopes.size()) {
                problems.add(at + ".scopes names a scope twice");
            }
            List<String> clusters = m.allowedClusterIds == null ? List.of() : m.allowedClusterIds;
            for (String cluster : clusters) {
                if (cluster == null || cluster.isBlank() || !cluster.chars().allMatch(c -> c > 0x20 && c < 0x7f)) {
                    problems.add(at + ".allowedClusterIds has a value that is not a clusterId");
                }
            }
            Instant expiresAt = null;
            if (m.expiresAt != null) {
                try {
                    expiresAt = Instant.parse(m.expiresAt);
                } catch (DateTimeParseException e) {
                    problems.add(at + ".expiresAt must be an ISO-8601 instant, such as 2026-12-31T00:00:00Z");
                }
            }
            if (m.sha256 == null || !SHA256.matcher(m.sha256).matches()) {
                problems.add(at + ".sha256 must be the key's SHA-256 as 64 lower-case hex characters");
            }
            if (problems.size() == before) {
                KatesPrincipal principal = new KatesPrincipal(m.name, type, scopes, clusters);
                entries.put(
                        m.id,
                        new Entry(
                                principal, expiresAt, m.disabled, HexFormat.of().parseHex(m.sha256)));
            }
        }
        if (!problems.isEmpty()) {
            throw new InvalidKeysFile(String.join("\n", problems));
        }
        return entries;
    }
}
