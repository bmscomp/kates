package com.bmscomp.kates.conformity;

import java.io.IOException;
import java.time.LocalDate;
import java.util.Collection;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.regex.Pattern;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.core.StreamReadFeature;
import com.fasterxml.jackson.core.exc.InputCoercionException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonMappingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.MapperFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.cfg.CoercionAction;
import com.fasterxml.jackson.databind.cfg.CoercionInputShape;
import com.fasterxml.jackson.databind.exc.MismatchedInputException;
import com.fasterxml.jackson.databind.exc.UnrecognizedPropertyException;
import com.fasterxml.jackson.databind.json.JsonMapper;
import com.fasterxml.jackson.databind.type.LogicalType;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.conformity.KafkaReference.Kafka;
import com.bmscomp.kates.conformity.KafkaReference.ListenerProperties;
import com.bmscomp.kates.conformity.KafkaReference.Listeners;
import com.bmscomp.kates.conformity.KafkaReference.Monitoring;
import com.bmscomp.kates.conformity.KafkaReference.RequiredListener;
import com.bmscomp.kates.conformity.KafkaReference.Resources;
import com.bmscomp.kates.conformity.KafkaReference.Role;
import com.bmscomp.kates.conformity.KafkaReference.Spec;
import com.bmscomp.kates.conformity.KafkaReference.Storage;
import com.bmscomp.kates.conformity.KafkaReference.Target;
import com.bmscomp.kates.conformity.KafkaReference.Topics;
import com.bmscomp.kates.conformity.KafkaReference.Versions;
import com.bmscomp.kates.conformity.KafkaReference.Waiver;

/**
 * Reads a KafkaReference body, and refuses one the check could not run as
 * written: a field the model lacks, a value of the wrong kind, a constraint
 * without an operator, a value outside the JSON Schema's limits, a waiver for
 * a rule the reference does not declare.
 *
 * <p>The mapper is its own. Quarkus' shared one ignores unknown fields and
 * turns "3" into 3, so a typo would drop a rule without a word. This one fails
 * on both, and on a duplicate key. Jackson stops at the first such error; the
 * limits after it are all reported, each keyed by its path, as
 * {@code spec.brokers.count} or {@code spec.kafka.config[min.insync.replicas]}.
 */
@ApplicationScoped
public class KafkaReferenceReader {

    public static final String API_VERSION = "conformity.kates.io/v1alpha1";
    public static final String KIND = "KafkaReference";

    /** The JSON Schema's limits, which bound what a check reads and echoes back. */
    public static final int MAX_BYTES = 256 * 1024;

    static final int MAX_ONE_OF = 50;
    static final int MAX_CONFIG_KEYS = 200;
    static final int MAX_REQUIRED_LISTENERS = 50;
    static final int MAX_EXCLUDES = 50;
    static final int MAX_WAIVERS = 200;
    static final int MAX_COUNT = 10_000;

    private static final Pattern DNS_LABEL = Pattern.compile("[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?");
    private static final Pattern CLUSTER_ID = Pattern.compile("[A-Za-z0-9_-]{22}");
    // The schema's pattern, with the prefix bounded as Kubernetes bounds it, so that a long one cannot make it
    // backtrack.
    private static final Pattern LABEL_KEY =
            Pattern.compile("([a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?");
    private static final Pattern CONFIG_KEY = Pattern.compile("[a-z0-9]+([._-][a-z0-9]+)*");
    private static final Pattern LISTENER_NAME = Pattern.compile("[a-z0-9]{1,11}");
    private static final Pattern TOPIC_PREFIX = Pattern.compile("[a-zA-Z0-9._-]{1,249}");

    private static final String DNS_LABEL_RULE = "must be a DNS label: up to 63 lower-case letters, digits and -";

    private static final List<String> SOURCES = List.of("cr", "brokers");
    private static final List<String> STORAGE_TYPES = List.of("ephemeral", "persistent-claim", "jbod");
    private static final List<String> ANTI_AFFINITY = List.of("required", "preferred", "none");
    private static final List<String> AUTH_TYPES = List.of("none", "tls", "scram-sha-512", "oauth", "custom");
    private static final List<String> AUTHORIZATION_TYPES = List.of("none", "simple", "opa", "keycloak", "custom");
    private static final List<String> LISTENER_TYPES =
            List.of("internal", "cluster-ip", "route", "loadbalancer", "nodeport", "ingress");
    private static final List<String> METRICS_TYPES =
            List.of("none", "jmxPrometheusExporter", "strimziMetricsReporter");

    /** The reason for a value Jackson could not read as its field's type. */
    private static final Map<Class<?>, String> EXPECTED = Map.of(
            Integer.class, "expected a whole number",
            Boolean.class, "expected true or false",
            String.class, "expected text",
            Severity.class, "expected fail or warn",
            LocalDate.class, "expected a date such as 2026-12-31");

    private final ObjectMapper mapper = JsonMapper.builder()
            .enable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
            .disable(DeserializationFeature.ACCEPT_FLOAT_AS_INT)
            .disable(MapperFeature.ALLOW_COERCION_OF_SCALARS)
            // That stops "3" becoming 3, but not 3 becoming "3".
            .withCoercionConfig(
                    LogicalType.Textual,
                    text -> text.setCoercion(CoercionInputShape.Integer, CoercionAction.Fail)
                            .setCoercion(CoercionInputShape.Float, CoercionAction.Fail)
                            .setCoercion(CoercionInputShape.Boolean, CoercionAction.Fail))
            .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
            .defaultLeniency(false)
            .addModule(new JavaTimeModule())
            .build();

    /**
     * The reference in the body.
     *
     * @throws InvalidReferenceException when the check could not run it as written
     */
    public KafkaReference read(byte[] body) {
        if (body.length > MAX_BYTES) {
            throw new InvalidReferenceException(ApiError.of(
                    413,
                    "Payload Too Large",
                    "A reference is at most " + MAX_BYTES + " bytes; this one is " + body.length));
        }
        KafkaReference reference;
        try {
            reference = mapper.readValue(body, KafkaReference.class);
        } catch (JsonMappingException e) {
            throw invalid(Map.of(path(e), reason(e)));
        } catch (JsonProcessingException e) {
            throw invalid(Map.of("body", notJson(e)));
        } catch (IOException e) {
            throw new IllegalStateException("reading a byte array", e);
        }
        if (reference == null) {
            throw invalid(Map.of("body", "expected a map"));
        }
        Map<String, String> errors = new Checks(reference).errors;
        if (!errors.isEmpty()) {
            throw invalid(errors);
        }
        return reference;
    }

    /**
     * The id of each rule the reference declares, in the order it declares
     * them: its path under spec, as {@code brokers.count}, with a key that has
     * a dot in brackets, as {@code kafka.config[min.insync.replicas]}, and a
     * required listener by its name, as {@code kafka.listeners.require[tls]}.
     */
    static Set<String> ruleIds(KafkaReference reference) {
        return new Checks(reference).rules;
    }

    private static InvalidReferenceException invalid(Map<String, String> errors) {
        String message = errors.entrySet().stream()
                .map(e -> e.getKey() + ": " + e.getValue())
                .collect(Collectors.joining("; "));
        return new InvalidReferenceException(ApiError.validationFailed(message, errors));
    }

    private static String path(JsonMappingException e) {
        StringBuilder path = new StringBuilder();
        for (JsonMappingException.Reference ref : e.getPath()) {
            if (ref.getFieldName() != null) {
                path.append(key(ref.getFieldName()));
            } else if (ref.getIndex() >= 0) {
                path.append('[').append(ref.getIndex()).append(']');
            }
        }
        if (path.isEmpty()) {
            return "body";
        }
        return path.charAt(0) == '.' ? path.substring(1) : path.toString();
    }

    /** A key's step in a path: .key, or [key] when the key has a dot. */
    private static String key(String key) {
        return key.contains(".") ? "[" + key + "]" : "." + key;
    }

    private static String reason(JsonMappingException e) {
        if (e instanceof Refusal) {
            return e.getOriginalMessage();
        }
        if (e instanceof UnrecognizedPropertyException) {
            return "unknown field";
        }
        if (e.getCause() instanceof InputCoercionException) {
            return "number out of range";
        }
        if (e.getCause() instanceof JsonProcessingException cause) {
            return notJson(cause);
        }
        if (e instanceof MismatchedInputException mismatch && mismatch.getTargetType() != null) {
            Class<?> target = mismatch.getTargetType();
            if (target.isRecord() || Map.class.isAssignableFrom(target)) {
                return "expected a map";
            }
            if (Collection.class.isAssignableFrom(target)) {
                return "expected a list";
            }
            return EXPECTED.getOrDefault(target, "invalid value");
        }
        return "invalid value";
    }

    private static String notJson(JsonProcessingException e) {
        return "not valid JSON: " + e.getOriginalMessage()
                + (e.getLocation() != null
                        ? " (line " + e.getLocation().getLineNr() + ", column "
                                + e.getLocation().getColumnNr() + ")"
                        : "");
    }

    /**
     * One walk over a reference Jackson has read: the reason for each value
     * outside the schema's limits, by its path, and each rule declared, by id.
     */
    private static final class Checks {

        final Map<String, String> errors = new LinkedHashMap<>();
        final Set<String> rules = new LinkedHashSet<>();

        Checks(KafkaReference reference) {
            if (!API_VERSION.equals(reference.apiVersion())) {
                error("apiVersion", "must be " + API_VERSION);
            }
            if (!KIND.equals(reference.kind())) {
                error("kind", "must be " + KIND);
            }
            if (present("metadata", reference.metadata())) {
                if (present("metadata.name", reference.metadata().name())) {
                    matches("metadata.name", reference.metadata().name(), DNS_LABEL, DNS_LABEL_RULE);
                }
                length("metadata.description", reference.metadata().description(), 0, 500);
            }
            if (present("spec", reference.spec())) {
                spec(reference.spec());
            }
        }

        private void spec(Spec spec) {
            Target target = spec.target();
            if (target != null) {
                if (target.platform() != null && !target.platform().equals("strimzi")) {
                    error("spec.target.platform", "must be strimzi, the only platform in v1alpha1");
                }
                matches("spec.target.namespace", target.namespace(), DNS_LABEL, DNS_LABEL_RULE);
                matches("spec.target.name", target.name(), DNS_LABEL, DNS_LABEL_RULE);
                matches(
                        "spec.target.clusterId",
                        target.clusterId(),
                        CLUSTER_ID,
                        "must be a Kafka cluster id: 22 letters, digits, - or _");
            }
            matches(
                    "spec.zoneLabel",
                    spec.zoneLabel(),
                    LABEL_KEY,
                    "must be a Kubernetes label key, such as topology.kubernetes.io/zone");
            Versions versions = spec.versions();
            if (versions != null) {
                declare("versions.kubernetes", versions.kubernetes());
                declare("versions.strimzi", versions.strimzi());
                declare("versions.kafka", versions.kafka());
            }
            role("controllers", spec.controllers());
            role("brokers", spec.brokers());
            kafka(spec.kafka());
            topics(spec.topics());
            Monitoring monitoring = spec.monitoring();
            if (monitoring != null) {
                choice("monitoring.metrics", monitoring.metrics(), METRICS_TYPES);
                declare("monitoring.kafkaExporter", monitoring.kafkaExporter());
                declare("monitoring.podMonitor", monitoring.podMonitor());
            }
            if (rules.isEmpty()) {
                error("spec", "declares no rule");
            }
            waivers(spec.waivers());
        }

        private void role(String role, Role spec) {
            if (spec == null) {
                return;
            }
            count(role + ".count", spec.count());
            if (role.equals("controllers")) {
                declare(role + ".dedicated", spec.dedicated());
            } else if (spec.dedicated() != null) {
                error("spec." + role + ".dedicated", "unknown field: only controllers take dedicated");
            }
            resources(role + ".requests", spec.requests());
            resources(role + ".limits", spec.limits());
            Storage storage = spec.storage();
            if (storage != null) {
                choice(role + ".storage.type", storage.type(), STORAGE_TYPES);
                declare(role + ".storage.size", storage.size());
                declare(role + ".storage.class", storage.storageClass());
                if (storage.storageClass() != null
                        && storage.storageClass().values().anyMatch(v -> v.isEmpty() || v.length() > 253)) {
                    error("spec." + role + ".storage.class", "must be 1 to 253 characters");
                }
            }
            count(role + ".zones", spec.zones());
            count(role + ".skew", spec.skew());
            count(role + ".perNode", spec.perNode());
            choice(role + ".antiAffinity", spec.antiAffinity(), ANTI_AFFINITY);
        }

        private void resources(String id, Resources resources) {
            if (resources != null) {
                declare(id + ".cpu", resources.cpu());
                declare(id + ".memory", resources.memory());
            }
        }

        private void kafka(Kafka kafka) {
            if (kafka == null) {
                return;
            }
            if (kafka.source() != null && !SOURCES.contains(kafka.source())) {
                error("spec.kafka.source", "must be one of " + String.join(", ", SOURCES));
            }
            declare("kafka.rackAwareness", kafka.rackAwareness());
            choice("kafka.authorization", kafka.authorization(), AUTHORIZATION_TYPES);
            Listeners listeners = kafka.listeners();
            if (listeners != null) {
                required(listeners.require());
                ListenerProperties every = listeners.every();
                if (every != null) {
                    choice("kafka.listeners.every.type", every.type(), LISTENER_TYPES);
                    declare("kafka.listeners.every.tls", every.tls());
                    choice("kafka.listeners.every.auth", every.auth(), AUTH_TYPES);
                }
            }
            Map<String, Constraint<JsonNode>> config = kafka.config();
            if (config != null && config.size() > MAX_CONFIG_KEYS) {
                error("spec.kafka.config", "has more than " + MAX_CONFIG_KEYS + " keys");
            } else if (config != null) {
                config.forEach((name, constraint) -> {
                    String id = "kafka.config" + key(name);
                    if (name.length() > 128 || !CONFIG_KEY.matcher(name).matches()) {
                        error("spec." + id, "must be a broker config name, such as min.insync.replicas");
                    } else if (present("spec." + id, constraint)) {
                        declare(id, constraint);
                    }
                });
            }
        }

        private void topics(Topics topics) {
            if (topics == null) {
                return;
            }
            count("topics.replicationFactor", topics.replicationFactor());
            count("topics.minInsyncReplicas", topics.minInsyncReplicas());
            if (topics.replicationFactor() == null && topics.minInsyncReplicas() == null) {
                error("spec.topics", "declares no rule: replicationFactor or minInsyncReplicas");
            }
            List<String> exclude = topics.exclude();
            if (exclude != null && exclude.size() > MAX_EXCLUDES) {
                error("spec.topics.exclude", "lists more than " + MAX_EXCLUDES + " prefixes");
                return;
            }
            for (int i = 0; exclude != null && i < exclude.size(); i++) {
                String path = "spec.topics.exclude[" + i + "]";
                if (present(path, exclude.get(i))) {
                    matches(
                            path,
                            exclude.get(i),
                            TOPIC_PREFIX,
                            "must be the start of a topic name: up to 249 letters, digits, ., _ and -");
                }
            }
        }

        /** Each required listener is one rule, named by the listener. */
        private void required(List<RequiredListener> require) {
            if (require != null && require.size() > MAX_REQUIRED_LISTENERS) {
                error("spec.kafka.listeners.require", "lists more than " + MAX_REQUIRED_LISTENERS + " listeners");
                return;
            }
            for (int i = 0; require != null && i < require.size(); i++) {
                String path = "spec.kafka.listeners.require[" + i + "]";
                RequiredListener listener = require.get(i);
                if (!present(path, listener)) {
                    continue;
                }
                if (present(path + ".name", listener.name())
                        && matches(
                                path + ".name",
                                listener.name(),
                                LISTENER_NAME,
                                "must be a Strimzi listener name: up to 11 lower-case letters and digits")
                        && !rules.add("kafka.listeners.require[" + listener.name() + "]")) {
                    error(path + ".name", "names a listener listed before it");
                }
                Map<String, Constraint<?>> properties = new LinkedHashMap<>();
                properties.put("type", listener.type());
                properties.put("port", listener.port());
                properties.put("tls", listener.tls());
                properties.put("auth", listener.auth());
                properties.values().removeIf(Objects::isNull);
                if (properties.isEmpty()) {
                    error(path, "lists no property to check: type, port, tls or auth");
                }
                properties.forEach((property, constraint) -> {
                    if (constraint.severity() == Severity.WARN) {
                        error(path + "." + property, "set severity on the listener, not on its properties");
                    }
                });
                allowed(path + ".type", listener.type(), LISTENER_TYPES);
                range(path + ".port", listener.port(), 1, 65_535);
                allowed(path + ".auth", listener.auth(), AUTH_TYPES);
            }
        }

        /**
         * A waiver names a rule the reference declares, once. A date already
         * past is accepted: the waiver has expired, and its rule counts again.
         */
        private void waivers(List<Waiver> waivers) {
            if (waivers != null && waivers.size() > MAX_WAIVERS) {
                error("spec.waivers", "lists more than " + MAX_WAIVERS + " waivers");
                return;
            }
            Map<String, Integer> waived = new HashMap<>();
            for (int i = 0; waivers != null && i < waivers.size(); i++) {
                String path = "spec.waivers[" + i + "]";
                Waiver waiver = waivers.get(i);
                if (!present(path, waiver)) {
                    continue;
                }
                if (present(path + ".rule", waiver.rule()) && length(path + ".rule", waiver.rule(), 1, 200)) {
                    Integer first = waived.putIfAbsent(waiver.rule(), i);
                    if (first != null) {
                        error(path + ".rule", "waives the rule spec.waivers[" + first + "] waives");
                    } else if (!rules.contains(waiver.rule())) {
                        error(path + ".rule", "names no rule this reference declares");
                    }
                }
                if (present(path + ".reason", waiver.reason())) {
                    length(path + ".reason", waiver.reason(), 1, 500);
                }
                if (present(path + ".by", waiver.by())) {
                    length(path + ".by", waiver.by(), 1, 100);
                }
                present(path + ".until", waiver.until());
            }
        }

        private void declare(String id, Constraint<?> constraint) {
            if (constraint != null) {
                rules.add(id);
            }
        }

        private void count(String id, Constraint<Integer> constraint) {
            declare(id, constraint);
            range("spec." + id, constraint, 0, MAX_COUNT);
        }

        private void choice(String id, Constraint<String> constraint, List<String> values) {
            declare(id, constraint);
            allowed("spec." + id, constraint, values);
        }

        private void range(String path, Constraint<Integer> constraint, int min, int max) {
            if (constraint != null && constraint.values().anyMatch(v -> v < min || v > max)) {
                error(path, "must be from " + min + " to " + max);
            }
        }

        private void allowed(String path, Constraint<String> constraint, List<String> values) {
            if (constraint != null && constraint.values().anyMatch(v -> !values.contains(v))) {
                error(path, "must be one of " + String.join(", ", values));
            }
        }

        private boolean present(String path, Object value) {
            if (value == null) {
                error(path, "required");
            }
            return value != null;
        }

        private boolean matches(String path, String value, Pattern pattern, String rule) {
            boolean matches = value == null || pattern.matcher(value).matches();
            if (!matches) {
                error(path, rule);
            }
            return matches;
        }

        private boolean length(String path, String value, int min, int max) {
            boolean within = value == null || (value.length() >= min && value.length() <= max);
            if (!within) {
                error(path, "must be " + min + " to " + max + " characters");
            }
            return within;
        }

        private void error(String path, String reason) {
            errors.putIfAbsent(path, reason);
        }
    }
}
