package com.bmscomp.kates.conformity;

import java.math.BigDecimal;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import java.util.function.Predicate;
import java.util.function.Supplier;
import java.util.regex.Pattern;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.ClientErrorException;
import jakarta.ws.rs.ServiceUnavailableException;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.PersistentVolumeClaim;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.Quantity;
import io.fabric8.kubernetes.api.model.ResourceRequirements;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientException;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import com.bmscomp.kates.api.ApiError;
import com.bmscomp.kates.conformity.ConformityReport.ClusterState;
import com.bmscomp.kates.conformity.ConformityReport.Result;
import com.bmscomp.kates.conformity.ConformityReport.Status;
import com.bmscomp.kates.conformity.ConformityReport.Summary;
import com.bmscomp.kates.conformity.ConformityReport.Verdict;
import com.bmscomp.kates.conformity.ConformityReport.Waived;
import com.bmscomp.kates.conformity.KafkaReference.Role;
import com.bmscomp.kates.conformity.KafkaReference.Spec;
import com.bmscomp.kates.conformity.KafkaReference.Target;
import com.bmscomp.kates.conformity.KafkaReference.Versions;
import com.bmscomp.kates.conformity.KafkaReference.Waiver;
import com.bmscomp.kates.service.ClusterTopologyService;

/**
 * Checks the Strimzi cluster this Kates API watches against a KafkaReference,
 * read-only: Kubernetes get and list calls, nothing else.
 *
 * <p>Each source is read once, when the first rule that needs it runs. A
 * source that cannot be read makes every rule that needs it UNKNOWN, never
 * FAIL. Each read is bounded by the Kubernetes client's request timeout.
 */
@ApplicationScoped
public class ConformityCheck {

    /** The rules this version evaluates; a reference that declares another is refused until it can. */
    private static final Pattern EVALUATED = Pattern.compile("versions\\..+|controllers\\.dedicated"
            + "|(controllers|brokers)\\.(count|(requests|limits)\\.(cpu|memory)|storage\\.(type|size|class))");

    private static final int MAX_SUBJECTS = 50;

    private static final List<Status> ORDER =
            List.of(Status.FAIL, Status.UNKNOWN, Status.WARN, Status.WAIVED, Status.PASS);

    private final KubernetesClient client;
    private final String namespace;
    private final String cluster;

    /** UTC, the zone a waiver's date is read in. */
    Clock clock = Clock.systemUTC();

    @Inject
    public ConformityCheck(
            KubernetesClient client,
            @ConfigProperty(name = "kates.topology.kafka-namespace", defaultValue = "kafka") String namespace,
            @ConfigProperty(name = "kates.topology.kafka-cluster", defaultValue = "krafter") String cluster) {
        this.client = client;
        this.namespace = namespace;
        this.cluster = cluster;
    }

    /**
     * @throws InvalidReferenceException a 400 for a rule this version cannot evaluate yet
     * @throws ClientErrorException a 409 when the reference is for another cluster, or the Kafka is missing
     * @throws ServiceUnavailableException when the Kubernetes API does not answer
     */
    public ConformityReport check(KafkaReference reference) {
        Map<String, String> notYet = KafkaReferenceReader.ruleIds(reference).stream()
                .filter(id -> !EVALUATED.matcher(id).matches())
                .collect(Collectors.toMap(
                        id -> "spec." + id, id -> "not implemented yet", (a, b) -> a, LinkedHashMap::new));
        if (!notYet.isEmpty()) {
            throw new InvalidReferenceException(ApiError.validationFailed(
                    notYet.keySet().stream()
                            .map(path -> path + ": not implemented yet")
                            .collect(Collectors.joining("; ")),
                    notYet));
        }
        return new Run(reference).report();
    }

    /** One check: the reference, the sources it reads, and the results so far. */
    private final class Run {

        private final KafkaReference reference;
        private final Spec spec;
        private final GenericKubernetesResource kafka;
        private final String kafkaSource;
        private final List<Result> results = new ArrayList<>();

        private final Source<List<Pod>> pods = new Source<>(
                "Kubernetes: pods " + namespace + "/strimzi.io/cluster=" + cluster,
                () -> client
                        .pods()
                        .inNamespace(namespace)
                        .withLabel("strimzi.io/cluster", cluster)
                        .withLabel("strimzi.io/kind", "Kafka")
                        .list()
                        .getItems()
                        .stream()
                        .filter(p -> p.getMetadata().getDeletionTimestamp() == null)
                        .toList());

        private final Source<List<GenericKubernetesResource>> pools = new Source<>(
                "Strimzi: KafkaNodePools " + namespace + "/strimzi.io/cluster=" + cluster,
                () -> client.genericKubernetesResources(ClusterTopologyService.NODE_POOL_CRD)
                        .inNamespace(namespace)
                        .withLabel("strimzi.io/cluster", cluster)
                        .list()
                        .getItems());

        private final Source<Map<String, PersistentVolumeClaim>> claims = new Source<>(
                "Kubernetes: PVCs " + namespace + "/strimzi.io/cluster=" + cluster,
                () -> client
                        .persistentVolumeClaims()
                        .inNamespace(namespace)
                        .withLabel("strimzi.io/cluster", cluster)
                        .list()
                        .getItems()
                        .stream()
                        .collect(Collectors.toMap(c -> c.getMetadata().getName(), Function.identity())));

        private final Source<String> kubernetesVersion = new Source<>(
                "Kubernetes: /version", () -> client.getKubernetesVersion().getGitVersion());

        Run(KafkaReference reference) {
            this.reference = reference;
            this.spec = reference.spec();
            this.kafka = target(spec.target());
            this.kafkaSource = "Strimzi: Kafka " + namespace + "/" + cluster;
        }

        private GenericKubernetesResource target(Target target) {
            String ns = target != null && target.namespace() != null ? target.namespace() : namespace;
            String name = target != null && target.name() != null ? target.name() : cluster;
            if (!ns.equals(namespace) || !name.equals(cluster)) {
                throw new ClientErrorException(
                        "The reference is for Kafka " + ns + "/" + name + "; this Kates API checks " + namespace + "/"
                                + cluster,
                        409);
            }
            GenericKubernetesResource found;
            try {
                found = client.genericKubernetesResources(ClusterTopologyService.KAFKA_CRD)
                        .inNamespace(ns)
                        .withName(name)
                        .get();
            } catch (KubernetesClientException e) {
                throw new ServiceUnavailableException("Kubernetes API not available: " + e.getMessage());
            }
            if (found == null) {
                throw new ClientErrorException("No Kafka " + ns + "/" + name + " in the cluster", 409);
            }
            Object clusterId = at(found, "status", "clusterId");
            if (target != null
                    && target.clusterId() != null
                    && !target.clusterId().equals(clusterId)) {
                throw new ClientErrorException(
                        "The reference pins cluster " + target.clusterId() + "; Kafka " + ns + "/" + name
                                + " is cluster " + clusterId,
                        409);
            }
            return found;
        }

        ConformityReport report() {
            Versions versions = spec.versions();
            if (versions != null) {
                version("versions.kubernetes", versions.kubernetes(), "Kubernetes: /version", kubernetesVersion::get);
                version(
                        "versions.strimzi",
                        versions.strimzi(),
                        kafkaSource + " status.operatorLastSuccessfulVersion",
                        () -> (String) at(kafka, "status", "operatorLastSuccessfulVersion"));
                version("versions.kafka", versions.kafka(), kafkaSource + " status.kafkaVersion", () ->
                        (String) at(kafka, "status", "kafkaVersion"));
            }
            role("controllers", spec.controllers(), "strimzi.io/controller-role", "controller");
            role("brokers", spec.brokers(), "strimzi.io/broker-role", "broker");

            List<Result> waived = waive(results);
            waived.sort(Comparator.comparing((Result r) -> ORDER.indexOf(r.status()))
                    .thenComparing(Result::rule));
            return new ConformityReport(
                    KafkaReferenceReader.API_VERSION,
                    "ConformityReport",
                    reference.metadata().name(),
                    new ConformityReport.Target(
                            "strimzi", namespace, cluster, (String) at(kafka, "status", "clusterId")),
                    Instant.now(clock),
                    clusterState(),
                    verdict(waived),
                    summary(waived),
                    waived);
        }

        private void version(String id, Constraint<Version> constraint, String source, Supplier<String> reported) {
            rule(id, constraint, meets(constraint), source, () -> {
                String text = reported.get();
                if (text == null) {
                    throw new Unreadable(source + " is not set");
                }
                return scalar(
                        Version.parse(text).orElseThrow(() -> new Unreadable("cannot read a version from " + text)));
            });
        }

        private void role(String role, Role spec, String roleLabel, String poolRole) {
            if (spec == null) {
                return;
            }
            resources(role + ".requests", spec.requests(), roleLabel, ResourceRequirements::getRequests);
            resources(role + ".limits", spec.limits(), roleLabel, ResourceRequirements::getLimits);
            storage(role, spec.storage(), roleLabel, poolRole);
            if (spec.count() != null) {
                rule(role + ".count", spec.count(), meets(spec.count()), pods.name, () -> {
                    List<Pod> mine = rolePods(roleLabel);
                    long ready = mine.stream().filter(ConformityCheck::ready).count();
                    return new Observation<>(Map.of("", mine.size()), ready + " of " + mine.size() + " Ready");
                });
            }
            if (spec.dedicated() != null) {
                rule("controllers.dedicated", spec.dedicated(), meets(spec.dedicated()), pools.name, () -> {
                    List<String> combined = pools.get().stream()
                            .filter(pool -> at(pool, "spec", "roles") instanceof List<?> roles
                                    && roles.contains("broker")
                                    && roles.contains("controller"))
                            .map(pool -> pool.getMetadata().getName())
                            .toList();
                    return new Observation<>(
                            Map.of("", combined.isEmpty()),
                            combined.isEmpty() ? null : "pools with both roles: " + String.join(", ", combined));
                });
            }
        }

        /** Requests or limits of each pod's kafka container; one the container does not set is unset. */
        private void resources(
                String id,
                KafkaReference.Resources resources,
                String roleLabel,
                Function<ResourceRequirements, Map<String, Quantity>> pick) {
            if (resources == null) {
                return;
            }
            Map<String, Constraint<Quantity>> byName = new LinkedHashMap<>();
            byName.put("cpu", resources.cpu());
            byName.put("memory", resources.memory());
            byName.forEach((name, constraint) -> rule(
                    id + "." + name,
                    constraint,
                    meets(constraint),
                    pods.name + ", container kafka",
                    () -> perPod(roleLabel, pod -> kafkaResource(pod, pick, name))));
        }

        private static Quantity kafkaResource(
                Pod pod, Function<ResourceRequirements, Map<String, Quantity>> pick, String name) {
            for (var container : pod.getSpec().getContainers()) {
                if ("kafka".equals(container.getName()) && container.getResources() != null) {
                    Map<String, Quantity> values = pick.apply(container.getResources());
                    return values == null ? null : values.get(name);
                }
            }
            return null;
        }

        private void storage(String role, KafkaReference.Storage storage, String roleLabel, String poolRole) {
            if (storage == null) {
                return;
            }
            rule(role + ".storage.type", storage.type(), meets(storage.type()), pools.name, () -> {
                Map<String, String> types = new LinkedHashMap<>();
                for (GenericKubernetesResource pool : pools.get()) {
                    if (at(pool, "spec", "roles") instanceof List<?> roles && roles.contains(poolRole)) {
                        types.put(pool.getMetadata().getName(), (String) at(pool, "spec", "storage", "type"));
                    }
                }
                return new Observation<>(types, null);
            });
            rule(role + ".storage.size", storage.size(), meets(storage.size()), claims.name, () -> {
                Map<String, PersistentVolumeClaim> byName = claims.get();
                return perPod(
                        roleLabel,
                        pod -> boundCapacity(
                                claimNames(pod).stream().map(byName::get).toList()));
            });
            rule(role + ".storage.class", storage.storageClass(), meets(storage.storageClass()), claims.name, () -> {
                Map<String, PersistentVolumeClaim> byName = claims.get();
                Map<String, String> classes = new LinkedHashMap<>();
                for (Pod pod : rolePods(roleLabel)) {
                    for (String claim : claimNames(pod)) {
                        PersistentVolumeClaim pvc = byName.get(claim);
                        classes.put(claim, pvc == null ? null : pvc.getSpec().getStorageClassName());
                    }
                }
                return new Observation<>(classes, classes.isEmpty() ? "no PVCs: ephemeral storage" : null);
            });
        }

        /** The value of each pod of a role, by pod name; null for a pod without one. */
        private <T> Observation<T> perPod(String roleLabel, Function<Pod, T> value) {
            Map<String, T> values = new LinkedHashMap<>();
            for (Pod pod : rolePods(roleLabel)) {
                values.put(pod.getMetadata().getName(), value.apply(pod));
            }
            return new Observation<>(values, null);
        }

        private List<Pod> rolePods(String roleLabel) {
            return pods.get().stream()
                    .filter(p -> "true".equals(p.getMetadata().getLabels().get(roleLabel)))
                    .toList();
        }

        /**
         * Evaluates one rule: it holds when every subject's value meets the
         * constraint, and a subject with no value never does.
         */
        private <T> void rule(
                String id,
                Constraint<T> constraint,
                Predicate<T> meets,
                String source,
                Supplier<Observation<T>> observe) {
            if (constraint == null) {
                return;
            }
            Observation<T> observation;
            try {
                observation = observe.get();
            } catch (Unreadable e) {
                results.add(new Result(
                        id,
                        Status.UNKNOWN,
                        constraint.severity(),
                        expected(constraint),
                        null,
                        null,
                        e.getMessage(),
                        source,
                        null,
                        null));
                return;
            }
            Map<String, T> values = observation.values();
            List<String> failing = values.entrySet().stream()
                    .filter(e -> e.getValue() == null || !meets.test(e.getValue()))
                    .map(Map.Entry::getKey)
                    .toList();
            Status status = failing.isEmpty()
                    ? Status.PASS
                    : constraint.severity() == Severity.WARN ? Status.WARN : Status.FAIL;
            Collection<String> shown = failing.isEmpty() ? values.keySet() : failing;
            List<Object> observed = shown.stream()
                    .map(values::get)
                    .map(ConformityCheck::render)
                    .distinct()
                    .toList();
            List<String> subjects = failing.stream()
                    .filter(s -> !s.isEmpty())
                    .limit(MAX_SUBJECTS)
                    .toList();
            results.add(new Result(
                    id,
                    status,
                    constraint.severity(),
                    expected(constraint),
                    observed.isEmpty() ? null : observed.size() == 1 ? observed.get(0) : observed,
                    subjects.isEmpty() ? null : subjects,
                    observation.detail(),
                    source,
                    null,
                    null));
        }

        /** An active waiver turns a rule that is not PASS into WAIVED; the report keeps what it was. */
        private List<Result> waive(List<Result> results) {
            Map<String, Waiver> byRule = spec.waivers() == null
                    ? Map.of()
                    : spec.waivers().stream().collect(Collectors.toMap(Waiver::rule, Function.identity()));
            LocalDate today = LocalDate.now(clock);
            List<Result> out = new ArrayList<>();
            for (Result r : results) {
                Waiver w = byRule.get(r.rule());
                if (w == null) {
                    out.add(r);
                } else if (today.isAfter(w.until())) {
                    out.add(with(r, r.status(), null, "waiver expired on " + w.until()));
                } else if (r.status() == Status.PASS) {
                    out.add(with(r, r.status(), null, "waiver no longer needed"));
                } else {
                    out.add(with(r, Status.WAIVED, new Waived(r.status(), w.reason(), w.by(), w.until()), null));
                }
            }
            return out;
        }

        private ClusterState clusterState() {
            Integer notReady;
            try {
                notReady = (int) pods.get().stream().filter(p -> !ready(p)).count();
            } catch (Unreadable e) {
                notReady = null;
            }
            Object observed = at(kafka, "status", "observedGeneration");
            return new ClusterState(
                    readyCondition(at(kafka, "status", "conditions")),
                    kafka.getMetadata().getGeneration(),
                    observed instanceof Number n ? n.longValue() : null,
                    notReady);
        }
    }

    private static Result with(Result r, Status status, Waived waiver, String note) {
        return new Result(
                r.rule(),
                status,
                r.severity(),
                r.expected(),
                r.observed(),
                r.subjects(),
                r.detail(),
                r.source(),
                waiver,
                note);
    }

    private static Verdict verdict(List<Result> results) {
        if (results.stream().anyMatch(r -> r.status() == Status.FAIL)) {
            return Verdict.FAIL;
        }
        if (results.stream().anyMatch(r -> r.status() == Status.UNKNOWN && r.severity() == Severity.FAIL)) {
            return Verdict.UNKNOWN;
        }
        return Verdict.PASS;
    }

    private static Summary summary(List<Result> results) {
        Map<Status, Long> counts =
                results.stream().collect(Collectors.groupingBy(Result::status, Collectors.counting()));
        return new Summary(
                counts.getOrDefault(Status.PASS, 0L).intValue(),
                counts.getOrDefault(Status.FAIL, 0L).intValue(),
                counts.getOrDefault(Status.WARN, 0L).intValue(),
                counts.getOrDefault(Status.WAIVED, 0L).intValue(),
                counts.getOrDefault(Status.UNKNOWN, 0L).intValue());
    }

    /** The constraint in its map form, with each value as the reference writes it. */
    static Map<String, Object> expected(Constraint<?> constraint) {
        Map<String, Object> expected = new LinkedHashMap<>();
        if (constraint.equalTo() != null) {
            expected.put("equals", render(constraint.equalTo()));
        }
        if (constraint.min() != null) {
            expected.put("min", render(constraint.min()));
        }
        if (constraint.max() != null) {
            expected.put("max", render(constraint.max()));
        }
        if (constraint.oneOf() != null) {
            expected.put(
                    "oneOf",
                    constraint.oneOf().stream().map(ConformityCheck::render).toList());
        }
        return expected;
    }

    private static Object render(Object value) {
        if (value == null) {
            return "unset";
        }
        return value instanceof Quantity || value instanceof Version ? value.toString() : value;
    }

    private static <T extends Comparable<? super T>> Predicate<T> meets(Constraint<T> constraint) {
        return value -> constraint.holds(value, Comparator.naturalOrder());
    }

    private static List<String> claimNames(Pod pod) {
        return pod.getSpec().getVolumes().stream()
                .filter(v -> v.getPersistentVolumeClaim() != null)
                .map(v -> v.getPersistentVolumeClaim().getClaimName())
                .toList();
    }

    /** What the bound claims hold, added up: a claim that is missing or not Bound adds nothing. */
    static Quantity boundCapacity(List<PersistentVolumeClaim> claims) {
        BigDecimal bytes = BigDecimal.ZERO;
        String format = null;
        for (PersistentVolumeClaim pvc : claims) {
            if (pvc == null
                    || pvc.getStatus() == null
                    || !"Bound".equals(pvc.getStatus().getPhase())) {
                continue;
            }
            Quantity capacity = pvc.getStatus().getCapacity() == null
                    ? null
                    : pvc.getStatus().getCapacity().get("storage");
            if (capacity != null) {
                bytes = bytes.add(capacity.getNumericalAmount());
                format = format == null ? capacity.getFormat() : format;
            }
        }
        return format == null || format.isEmpty()
                ? new Quantity(bytes.toPlainString())
                : Quantity.fromNumericalAmount(bytes, format);
    }

    private static <T> Observation<T> scalar(T value) {
        return new Observation<>(Map.of("", value), null);
    }

    static boolean ready(Pod pod) {
        return pod.getStatus() != null
                && pod.getStatus().getConditions() != null
                && pod.getStatus().getConditions().stream()
                        .anyMatch(c -> "Ready".equals(c.getType()) && "True".equals(c.getStatus()));
    }

    private static Boolean readyCondition(Object conditions) {
        if (!(conditions instanceof List<?> list)) {
            return null;
        }
        return list.stream()
                .anyMatch(c ->
                        c instanceof Map<?, ?> m && "Ready".equals(m.get("type")) && "True".equals(m.get("status")));
    }

    /** A value under a custom resource's spec or status, or null. */
    static Object at(GenericKubernetesResource resource, String... path) {
        Object node = resource.getAdditionalProperties();
        for (String key : path) {
            if (!(node instanceof Map<?, ?> map)) {
                return null;
            }
            node = map.get(key);
        }
        return node;
    }

    /** The values a rule compares, by subject ("" for a rule about the cluster as a whole). */
    private record Observation<T>(Map<String, T> values, String detail) {}

    /** A source that could not be read: the rules that need it are UNKNOWN. */
    private static final class Unreadable extends RuntimeException {
        Unreadable(String message) {
            super(message, null, false, false);
        }
    }

    /** Read once, on first use; a failure is kept and reported to each rule that asks. */
    private static final class Source<T> {
        private final String name;
        private final Supplier<T> load;
        private boolean loaded;
        private T value;
        private RuntimeException error;

        Source(String name, Supplier<T> load) {
            this.name = name;
            this.load = load;
        }

        T get() {
            if (!loaded) {
                loaded = true;
                try {
                    value = load.get();
                } catch (RuntimeException e) {
                    error = e;
                }
            }
            if (error != null) {
                throw new Unreadable(name + ": " + error.getMessage());
            }
            return value;
        }
    }
}
