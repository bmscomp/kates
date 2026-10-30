package com.bmscomp.kates.compliance;

import java.math.BigDecimal;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.TreeMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.function.Function;
import java.util.function.Predicate;
import java.util.function.Supplier;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.ClientErrorException;
import jakarta.ws.rs.ServiceUnavailableException;

import io.fabric8.kubernetes.api.model.Affinity;
import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.LabelSelector;
import io.fabric8.kubernetes.api.model.LabelSelectorRequirement;
import io.fabric8.kubernetes.api.model.PersistentVolumeClaim;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodAffinityTerm;
import io.fabric8.kubernetes.api.model.PodAntiAffinity;
import io.fabric8.kubernetes.api.model.Quantity;
import io.fabric8.kubernetes.api.model.ResourceRequirements;
import io.fabric8.kubernetes.api.model.discovery.v1.Endpoint;
import io.fabric8.kubernetes.api.model.discovery.v1.EndpointSlice;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientException;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import org.apache.kafka.clients.admin.Config;
import org.apache.kafka.clients.admin.ConfigEntry;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.ListTopicsOptions;
import org.apache.kafka.clients.admin.TopicDescription;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.config.ConfigResource;
import org.apache.kafka.common.config.TopicConfig;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import com.bmscomp.kates.compliance.ComplianceReport.ClusterState;
import com.bmscomp.kates.compliance.ComplianceReport.Result;
import com.bmscomp.kates.compliance.ComplianceReport.Status;
import com.bmscomp.kates.compliance.ComplianceReport.Summary;
import com.bmscomp.kates.compliance.ComplianceReport.Verdict;
import com.bmscomp.kates.compliance.ComplianceReport.Waived;
import com.bmscomp.kates.compliance.KafkaReference.Role;
import com.bmscomp.kates.compliance.KafkaReference.Spec;
import com.bmscomp.kates.compliance.KafkaReference.Target;
import com.bmscomp.kates.compliance.KafkaReference.Versions;
import com.bmscomp.kates.compliance.KafkaReference.Waiver;
import com.bmscomp.kates.service.ClusterTopologyService;
import com.bmscomp.kates.service.KafkaAdminService;

/**
 * Checks the Strimzi cluster this Kates API watches against a KafkaReference,
 * read-only: Kubernetes get and list calls, with kafka.source brokers the
 * Kafka Admin API's describeCluster and describeConfigs, and with topics its
 * listTopics, describeTopics and describeConfigs.
 *
 * <p>Each source is read once, when the first rule that needs it runs. A
 * source that cannot be read makes every rule that needs it UNKNOWN, never
 * FAIL. Each read is bounded by the Kubernetes client's request timeout.
 */
@ApplicationScoped
public class ComplianceCheck {

    /** The rules this version evaluates; a reference that declares another is refused until it can. */
    static final CustomResourceDefinitionContext POD_MONITOR_CRD = new CustomResourceDefinitionContext.Builder()
            .withGroup("monitoring.coreos.com")
            .withVersion("v1")
            .withPlural("podmonitors")
            .withScope("Namespaced")
            .build();

    private static final String DEFAULT_ZONE_LABEL = "topology.kubernetes.io/zone";
    private static final String HOSTNAME = "kubernetes.io/hostname";

    private static final int MAX_SUBJECTS = 50;

    private static final List<Status> ORDER =
            List.of(Status.FAIL, Status.UNKNOWN, Status.WARN, Status.WAIVED, Status.PASS);

    private final KubernetesClient client;
    private final KafkaAdminService admin;
    private final String namespace;
    private final String cluster;

    /** UTC, the zone a waiver's date is read in. */
    Clock clock = Clock.systemUTC();

    @Inject
    public ComplianceCheck(
            KubernetesClient client,
            KafkaAdminService admin,
            @ConfigProperty(name = "kates.topology.kafka-namespace", defaultValue = "kafka") String namespace,
            @ConfigProperty(name = "kates.topology.kafka-cluster", defaultValue = "krafter") String cluster) {
        this.client = client;
        this.admin = admin;
        this.namespace = namespace;
        this.cluster = cluster;
    }

    /**
     * @throws ClientErrorException a 409 when the reference is for another cluster, or the Kafka is missing
     * @throws ServiceUnavailableException when the Kubernetes API does not answer, or refuses the Kafka CR's get
     */
    public ComplianceReport check(KafkaReference reference) {
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

        /**
         * Each node's labels. A namespace-scoped role, such as the kates chart's
         * with rbac.scope=namespace, may not list nodes, which are cluster-scoped;
         * then the default zone label comes from the EndpointSlices of Strimzi's
         * headless Service instead, and another one from the brokers' racks.
         */
        private final Source<NodeLabels> nodeLabels = new Source<>("Kubernetes: nodes", () -> {
            try {
                return new NodeLabels(
                        client.nodes().list().getItems().stream()
                                .collect(Collectors.toMap(
                                        n -> n.getMetadata().getName(),
                                        n -> n.getMetadata().getLabels() == null
                                                ? Map.of()
                                                : n.getMetadata().getLabels())),
                        null,
                        false);
            } catch (KubernetesClientException e) {
                if (e.getCode() != 403) {
                    throw e;
                }
            }
            if (DEFAULT_ZONE_LABEL.equals(zoneLabel())) {
                return endpointZones();
            }
            return brokerRacks();
        });

        /** With kafka.source brokers only: the brokers Kafka reports, and their effective configs. */
        private final Source<Collection<Node>> brokers =
                new Source<>("Kafka Admin: describeCluster", this::describeBrokers);

        private final Source<BrokerConfigs> brokerConfigs = new Source<>("Kafka Admin: describeConfigs", () -> {
            List<ConfigResource> resources = brokers.get().stream()
                    .map(n -> new ConfigResource(ConfigResource.Type.BROKER, n.idString()))
                    .toList();
            Map<String, Config> configs = new TreeMap<>();
            List<String> unread = new ArrayList<>();
            admin.sharedAdminClient().describeConfigs(resources).values().forEach((resource, config) -> {
                try {
                    configs.put(resource.name(), await(config));
                } catch (IllegalStateException e) {
                    unread.add("broker " + resource.name() + ": " + e.getMessage());
                }
            });
            unread.sort(null);
            return new BrokerConfigs(configs, unread);
        });

        /** With topics only: every topic, internal ones included, but those an exclude prefix names. */
        private final Source<List<String>> topicNames =
                new Source<>(
                        "Kafka Admin: listTopics",
                        () -> await(admin.sharedAdminClient()
                                        .listTopics(new ListTopicsOptions().listInternal(true))
                                        .names())
                                .stream()
                                .filter(this::checked)
                                .sorted()
                                .toList());

        private final Source<Map<String, TopicDescription>> topicDescriptions = new Source<>(
                "Kafka Admin: describeTopics",
                () -> await(admin.sharedAdminClient()
                        .describeTopics(topicNames.get())
                        .allTopicNames()));

        private final Source<Map<String, Config>> topicConfigs =
                new Source<>("Kafka Admin: describeConfigs, topics", () -> {
                    List<ConfigResource> resources = topicNames.get().stream()
                            .map(name -> new ConfigResource(ConfigResource.Type.TOPIC, name))
                            .toList();
                    Map<String, Config> configs = new TreeMap<>();
                    await(admin.sharedAdminClient().describeConfigs(resources).all())
                            .forEach((resource, config) -> configs.put(resource.name(), config));
                    return configs;
                });

        /**
         * Every namespace's PodMonitors, or with a namespace-scoped role only
         * the Kafka namespace's. Null items when the PodMonitor CRD is not
         * installed: then there is none, which is the answer.
         */
        private final Source<Monitors> podMonitors = new Source<>("Kubernetes: PodMonitors in any namespace", () -> {
            try {
                return new Monitors(
                        client.genericKubernetesResources(POD_MONITOR_CRD)
                                .inAnyNamespace()
                                .list()
                                .getItems(),
                        false);
            } catch (KubernetesClientException e) {
                if (e.getCode() == 404) {
                    return new Monitors(null, false);
                }
                if (e.getCode() != 403) {
                    throw e;
                }
            }
            try {
                return new Monitors(
                        client.genericKubernetesResources(POD_MONITOR_CRD)
                                .inNamespace(namespace)
                                .list()
                                .getItems(),
                        true);
            } catch (KubernetesClientException e) {
                if (e.getCode() == 404) {
                    return new Monitors(null, true);
                }
                throw e;
            }
        });

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
                if (e.getCode() == 403) {
                    throw new ServiceUnavailableException("The Kates API's service account may not get"
                            + " kafkas.kafka.strimzi.io in " + ns + ": bind it a Role there that grants it,"
                            + " such as the kates chart's with rbac.scope=namespace");
                }
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

        ComplianceReport report() {
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
            kafka(spec.kafka());
            topics(spec.topics());
            monitoring(spec.monitoring());

            List<Result> waived = waive(results);
            waived.sort(Comparator.comparing((Result r) -> ORDER.indexOf(r.status()))
                    .thenComparing(Result::rule));
            return new ComplianceReport(
                    KafkaReferenceReader.API_VERSION,
                    "ConformityReport",
                    reference.metadata().name(),
                    new ComplianceReport.Target(
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
            placement(role, spec, roleLabel);
            if (spec.count() != null) {
                rule(role + ".count", spec.count(), meets(spec.count()), pods.name, () -> {
                    List<Pod> mine = rolePods(roleLabel);
                    long ready = mine.stream().filter(ComplianceCheck::ready).count();
                    return new Observation<>(Map.of("", mine.size()), ready + " of " + mine.size() + " Ready");
                });
            }
            if (spec.dedicated() != null) {
                rule("controllers.dedicated", spec.dedicated(), meets(spec.dedicated()), pools.name, () -> {
                    List<String> combined = new ArrayList<>();
                    List<String> idle = new ArrayList<>();
                    for (GenericKubernetesResource pool : pools.get()) {
                        if (at(pool, "spec", "roles") instanceof List<?> roles
                                && roles.contains("broker")
                                && roles.contains("controller")) {
                            (idle(pool) ? idle : combined)
                                    .add(pool.getMetadata().getName());
                        }
                    }
                    return new Observation<>(
                            Map.of("", combined.isEmpty()),
                            detail(
                                    combined.isEmpty() ? null : "pools with both roles: " + String.join(", ", combined),
                                    notCounted(idle)));
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
                List<String> idle = new ArrayList<>();
                for (GenericKubernetesResource pool : pools.get()) {
                    if (at(pool, "spec", "roles") instanceof List<?> roles && roles.contains(poolRole)) {
                        if (idle(pool)) {
                            idle.add(pool.getMetadata().getName());
                        } else {
                            types.put(pool.getMetadata().getName(), (String) at(pool, "spec", "storage", "type"));
                        }
                    }
                }
                return new Observation<>(
                        types,
                        detail(types.isEmpty() ? "no pool has the " + poolRole + " role" : null, notCounted(idle)));
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

        /**
         * Where the pods of a role run. A pod that is not scheduled, or runs on
         * a node without the zone label, fails zones and skew whatever the
         * count: the spread cannot be shown.
         */
        private void placement(String role, Role spec, String roleLabel) {
            String zoneLabel = zoneLabel();
            Supplier<String> source = () -> pods.name + ", " + zonesFrom("nodes' " + zoneLabel);
            rule(
                    role + ".zones",
                    spec.zones(),
                    meets(spec.zones()),
                    source,
                    () -> zoned(roleLabel, zoneLabel, Map::size));
            rule(
                    role + ".skew",
                    spec.skew(),
                    meets(spec.skew()),
                    source,
                    () -> zoned(roleLabel, zoneLabel, perZone -> {
                        var counts = perZone.values().stream()
                                .mapToInt(Integer::intValue)
                                .summaryStatistics();
                        return perZone.isEmpty() ? 0 : counts.getMax() - counts.getMin();
                    }));
            rule(role + ".perNode", spec.perNode(), meets(spec.perNode()), pods.name, () -> {
                Map<String, Integer> perNode = new LinkedHashMap<>();
                for (Pod pod : rolePods(roleLabel)) {
                    if (pod.getSpec().getNodeName() != null) {
                        perNode.merge(pod.getSpec().getNodeName(), 1, Integer::sum);
                    }
                }
                return new Observation<>(perNode, null, true);
            });
            rule(
                    role + ".antiAffinity",
                    spec.antiAffinity(),
                    meets(spec.antiAffinity()),
                    pods.name + " affinity",
                    () -> perPod(roleLabel, ComplianceCheck::antiAffinity));
        }

        /**
         * The pods of a role by zone, measured: under "" the measure, and each
         * pod that has no zone with a null, so that the rule fails and names it.
         */
        private Observation<Integer> zoned(
                String roleLabel, String zoneLabel, Function<Map<String, Integer>, Integer> measure) {
            NodeLabels nodes = nodeLabels.get();
            Map<String, Integer> perZone = new TreeMap<>();
            Map<String, Integer> values = new LinkedHashMap<>();
            for (Pod pod : rolePods(roleLabel)) {
                String node = pod.getSpec().getNodeName();
                String zone = node == null ? null : nodes.of(node).get(zoneLabel);
                if (zone == null) {
                    values.put(pod.getMetadata().getName(), null);
                } else {
                    perZone.merge(zone, 1, Integer::sum);
                }
            }
            values.put("", measure.apply(perZone));
            String detail = perZone.entrySet().stream()
                    .map(e -> e.getKey() + ": " + e.getValue())
                    .collect(Collectors.joining(", "));
            if (values.size() > 1) {
                detail = (detail.isEmpty() ? "" : detail + "; ") + (values.size() - 1)
                        + " not scheduled on a node with " + zoneLabel;
            }
            if (nodes.from() != null) {
                detail = (detail.isEmpty() ? "" : detail + "; ") + "zones from " + nodes.from();
            }
            return new Observation<>(values, detail.isEmpty() ? null : detail);
        }

        /**
         * The default zone label of each node a Kafka pod runs on, as the
         * EndpointSlice controller copies it onto the endpoints of Strimzi's
         * headless Service, which lists every Kafka pod, Ready or not. A pod
         * with no address yet has no endpoint, so it counts as not in a zone
         * unless another Kafka pod shares its node.
         */
        private NodeLabels endpointZones() {
            String service = cluster + "-kafka-brokers";
            List<EndpointSlice> slices;
            try {
                slices = client.discovery()
                        .v1()
                        .endpointSlices()
                        .inNamespace(namespace)
                        .withLabel("kubernetes.io/service-name", service)
                        .list()
                        .getItems();
            } catch (KubernetesClientException e) {
                if (e.getCode() == 403) {
                    throw new IllegalStateException("Forbidden: the Kates API's service account may list neither"
                            + " nodes, which are cluster-scoped, nor EndpointSlices in " + namespace);
                }
                throw e;
            }
            Map<String, Map<String, String>> byNode = new HashMap<>();
            for (EndpointSlice slice : slices) {
                for (Endpoint endpoint : slice.getEndpoints() == null ? List.<Endpoint>of() : slice.getEndpoints()) {
                    if (endpoint.getNodeName() != null) {
                        Map<String, String> labels =
                                byNode.computeIfAbsent(endpoint.getNodeName(), n -> new HashMap<>());
                        if (endpoint.getZone() != null) {
                            labels.put(DEFAULT_ZONE_LABEL, endpoint.getZone());
                        }
                    }
                }
            }
            if (byNode.isEmpty()) {
                throw new IllegalStateException("Forbidden: the Kates API's service account may not list nodes,"
                        + " which are cluster-scoped, and no EndpointSlice of " + namespace + "/" + service
                        + " names a node");
            }
            return new NodeLabels(byNode, "the EndpointSlices of " + namespace + "/" + service, false);
        }

        /**
         * The zone label of each node a broker runs on, as Strimzi copies it
         * into the broker's rack when it is spec.kafka.rack.topologyKey. A
         * node that runs no broker Kafka reports is not covered: its zone is
         * not known, rather than missing.
         */
        private NodeLabels brokerRacks() {
            if (!zoneLabel().equals(at(kafka, "spec", "kafka", "rack", "topologyKey"))) {
                throw new IllegalStateException("Forbidden: the Kates API's service account may not list nodes,"
                        + " which are cluster-scoped, and only nodes carry " + zoneLabel()
                        + ", which is not the Kafka CR's spec.kafka.rack.topologyKey");
            }
            Map<String, String> rackById = new HashMap<>();
            for (Node broker : brokers.get()) {
                rackById.put(broker.idString(), broker.rack());
            }
            Map<String, Map<String, String>> byNode = new HashMap<>();
            for (Pod pod : pods.get()) {
                String name = pod.getMetadata().getName();
                String id = name.substring(name.lastIndexOf('-') + 1);
                String node = pod.getSpec().getNodeName();
                if (node != null && rackById.containsKey(id)) {
                    Map<String, String> labels = byNode.computeIfAbsent(node, n -> new HashMap<>());
                    String rack = rackById.get(id);
                    if (rack != null && !rack.isEmpty()) {
                        labels.put(zoneLabel(), rack);
                    }
                }
            }
            if (byNode.isEmpty()) {
                throw new IllegalStateException("Forbidden: the Kates API's service account may not list nodes,"
                        + " which are cluster-scoped, and no broker Kafka reports runs in a Kafka pod");
            }
            return new NodeLabels(byNode, brokers.name + " racks", true);
        }

        /** Where the zones come from: the nodes' labels, or what stood in for the node list once read. */
        private String zonesFrom(String nodesSource) {
            NodeLabels nodes = nodeLabels.peek();
            return nodes != null && nodes.from() != null ? nodes.from() : nodesSource;
        }

        private String zoneLabel() {
            return spec.zoneLabel() != null ? spec.zoneLabel() : DEFAULT_ZONE_LABEL;
        }

        /** The Kafka rules, read from the Kafka CR. */
        /** Monitoring: read from the Kafka CR, and from the PodMonitors in any namespace. */
        private void monitoring(KafkaReference.Monitoring rules) {
            if (rules == null) {
                return;
            }
            String crKafka = kafkaSource + " spec";
            rule(
                    "monitoring.metrics",
                    rules.metrics(),
                    meets(rules.metrics()),
                    crKafka + ".kafka.metricsConfig",
                    () -> {
                        Object type = at(kafka, "spec", "kafka", "metricsConfig", "type");
                        return scalar(type == null ? "none" : type.toString());
                    });
            rule(
                    "monitoring.kafkaExporter",
                    rules.kafkaExporter(),
                    meets(rules.kafkaExporter()),
                    crKafka + ".kafkaExporter",
                    () -> scalar(at(kafka, "spec", "kafkaExporter") != null));
            rule("monitoring.podMonitor", rules.podMonitor(), meets(rules.podMonitor()), podMonitors.name, () -> {
                Monitors monitors = podMonitors.get();
                if (monitors.items() == null) {
                    return new Observation<>(Map.of("", false), "the PodMonitor CRD is not installed");
                }
                List<Pod> brokerPods = rolePods("strimzi.io/broker-role");
                if (brokerPods.isEmpty()) {
                    return new Observation<>(Map.of("", false), "no broker pod to select");
                }
                List<String> selecting = monitors.items().stream()
                        .filter(m -> watches(m, namespace)
                                && brokerPods.stream()
                                        .allMatch(p -> matches(
                                                selector(at(m, "spec", "selector")),
                                                p.getMetadata().getLabels())))
                        .map(m -> m.getMetadata().getNamespace() + "/"
                                + m.getMetadata().getName())
                        .toList();
                if (selecting.isEmpty() && monitors.ownNamespaceOnly()) {
                    // One in a namespace it may not list could still select them.
                    throw new Unreadable("Forbidden: the Kates API's service account may not list PodMonitors"
                            + " outside " + namespace + ", and none in " + namespace + " selects every broker pod");
                }
                return new Observation<>(
                        Map.of("", !selecting.isEmpty()),
                        selecting.isEmpty() ? "no PodMonitor selects every broker pod" : String.join(", ", selecting));
            });
        }

        private LabelSelector selector(Object spec) {
            return spec instanceof Map<?, ?> map
                    ? client.getKubernetesSerialization().convertValue(map, LabelSelector.class)
                    : null;
        }

        private void kafka(KafkaReference.Kafka rules) {
            if (rules == null) {
                return;
            }
            String crKafka = kafkaSource + " spec.kafka";
            boolean fromBrokers = "brokers".equals(rules.source());
            rule(
                    "kafka.authorization",
                    rules.authorization(),
                    meets(rules.authorization()),
                    crKafka + ".authorization",
                    () -> {
                        Object type = at(kafka, "spec", "kafka", "authorization", "type");
                        return scalar(type == null ? "none" : type.toString());
                    });
            rule(
                    "kafka.rackAwareness",
                    rules.rackAwareness(),
                    meets(rules.rackAwareness()),
                    () -> crKafka + ".rack, " + (fromBrokers ? brokers.name + " racks" : zonesFrom("nodes' labels")),
                    () -> rackAwareness(fromBrokers));
            if (rules.listeners() != null) {
                listeners(rules.listeners(), crKafka + ".listeners");
            }
            if (rules.config() != null) {
                Object set = at(kafka, "spec", "kafka", "config");
                Map<?, ?> config = set instanceof Map<?, ?> m ? m : Map.of();
                rules.config().forEach((key, constraint) -> {
                    Predicate<String> meets = value -> Constraint.configHolds(constraint, value);
                    rule(
                            "kafka.config" + (key.contains(".") ? "[" + key + "]" : "." + key),
                            constraint,
                            meets,
                            fromBrokers ? brokerConfigs.name + ", every broker" : crKafka + ".config",
                            () -> fromBrokers ? effectiveConfig(key, meets) : declaredConfig(config, key));
                });
            }
        }

        /** Each topic's own durability, read through the Kafka Admin API whatever kafka.source says. */
        private void topics(KafkaReference.Topics rules) {
            if (rules == null) {
                return;
            }
            rule(
                    "topics.replicationFactor",
                    rules.replicationFactor(),
                    meets(rules.replicationFactor()),
                    topicNames.name + ", describeTopics",
                    () -> perTopic(
                            topicDescriptions.get(), ComplianceCheck::replicationFactor, rules.replicationFactor()));
            rule(
                    "topics.minInsyncReplicas",
                    rules.minInsyncReplicas(),
                    meets(rules.minInsyncReplicas()),
                    topicNames.name + ", describeConfigs",
                    () -> perTopic(topicConfigs.get(), ComplianceCheck::minInsyncReplicas, rules.minInsyncReplicas()));
        }

        private boolean checked(String topic) {
            List<String> exclude = spec.topics().exclude();
            return exclude == null || exclude.stream().noneMatch(topic::startsWith);
        }

        private Observation<String> declaredConfig(Map<?, ?> config, String key) {
            Object value = config.get(key);
            return new Observation<>(
                    Collections.singletonMap("", value == null ? null : value.toString()),
                    value == null ? "not set in the Kafka CR" : null);
        }

        /**
         * The value each broker reports, defaults included; a sensitive one is
         * never read. A broker that could not be read, or reports no value
         * (log.retention.ms, which Kafka derives from log.retention.hours),
         * hides no gap the others show, and when they show none, the rule is
         * UNKNOWN.
         */
        private Observation<String> effectiveConfig(String key, Predicate<String> meets) {
            BrokerConfigs read = brokerConfigs.get();
            List<String> unread = new ArrayList<>(unreported());
            unread.addAll(read.unread());
            Map<String, String> values = new LinkedHashMap<>();
            List<String> valueless = new ArrayList<>();
            boolean reported = false;
            for (Map.Entry<String, Config> broker : read.configs().entrySet()) {
                ConfigEntry entry = broker.getValue().get(key);
                if (entry != null && entry.isSensitive()) {
                    throw new Unreadable(key + " is sensitive: Kafka does not return its value");
                }
                reported |= entry != null;
                if (entry != null && entry.value() == null) {
                    valueless.add("broker " + broker.getKey());
                } else {
                    values.put("broker " + broker.getKey(), entry == null ? null : entry.value());
                }
            }
            if (!valueless.isEmpty()) {
                unread.add(String.join(", ", valueless) + " report no value: Kafka derives " + key
                        + " from another setting");
            }
            if (!unread.isEmpty() && values.values().stream().allMatch(v -> v != null && meets.test(v))) {
                throw new Unreadable(String.join("; ", unread));
            }
            if (!reported) {
                unread.add(0, "brokers do not report " + key);
            }
            return new Observation<>(values, unread.isEmpty() ? null : String.join("; ", unread));
        }

        /** The brokers Kafka reports, once the cluster the Admin client reaches is the Kafka CR's. */
        private Collection<Node> describeBrokers() {
            DescribeClusterResult described = admin.sharedAdminClient().describeCluster();
            String reached = await(described.clusterId());
            Object clusterId = at(kafka, "status", "clusterId");
            if (clusterId != null && !clusterId.equals(reached)) {
                throw new IllegalStateException(
                        "the Kafka Admin client (kates.kafka.bootstrap-servers) reaches cluster " + reached + "; Kafka "
                                + namespace + "/" + cluster + " is cluster " + clusterId);
            }
            return await(described.nodes());
        }

        /** The broker pools' status.nodeIds that Kafka does not report: fenced, or not started. */
        private List<String> unreported() {
            List<String> reported = brokers.get().stream().map(Node::idString).toList();
            List<String> missing = new ArrayList<>();
            for (GenericKubernetesResource pool : pools.get()) {
                if (at(pool, "spec", "roles") instanceof List<?> roles
                        && roles.contains("broker")
                        && at(pool, "status", "nodeIds") instanceof List<?> ids) {
                    for (Object id : ids) {
                        if (!reported.contains(id.toString())) {
                            missing.add("broker " + id + ": not reported by Kafka");
                        }
                    }
                }
            }
            return missing;
        }

        /**
         * The rack key is the zone label, and then, from the Kafka CR, every
         * broker's node carries that label (where Strimzi takes each rack
         * from) or, from the brokers, every broker reports a rack.
         */
        private Observation<Boolean> rackAwareness(boolean fromBrokers) {
            String zoneLabel = zoneLabel();
            Object key = at(kafka, "spec", "kafka", "rack", "topologyKey");
            if (!zoneLabel.equals(key)) {
                return new Observation<>(
                        Map.of("", false),
                        key == null
                                ? "spec.kafka.rack is not set"
                                : "spec.kafka.rack.topologyKey is " + key + ", not " + zoneLabel);
            }
            List<String> without;
            if (fromBrokers) {
                without = brokers.get().stream()
                        .filter(n -> n.rack() == null || n.rack().isEmpty())
                        .map(n -> "broker " + n.idString())
                        .toList();
                List<String> unread = unreported();
                if (without.isEmpty() && !unread.isEmpty()) {
                    throw new Unreadable(String.join("; ", unread));
                }
            } else {
                NodeLabels nodes = nodeLabels.get();
                without = rolePods("strimzi.io/broker-role").stream()
                        .filter(p -> p.getSpec().getNodeName() == null
                                || !nodes.of(p.getSpec().getNodeName()).containsKey(zoneLabel))
                        .map(p -> p.getMetadata().getName())
                        .toList();
            }
            return new Observation<>(
                    Map.of("", without.isEmpty()),
                    without.isEmpty()
                            ? null
                            : (fromBrokers
                                            ? "brokers that report no rack: "
                                            : "brokers on no node with " + zoneLabel + ": ")
                                    + String.join(", ", without));
        }

        private void listeners(KafkaReference.Listeners rules, String source) {
            Map<String, Map<String, Object>> listeners = new LinkedHashMap<>();
            if (at(kafka, "spec", "kafka", "listeners") instanceof List<?> list) {
                for (Object item : list) {
                    if (item instanceof Map<?, ?> l && l.get("name") != null) {
                        Map<String, Object> properties = new LinkedHashMap<>();
                        properties.put(
                                "type",
                                l.get("type") == null ? null : l.get("type").toString());
                        properties.put("port", l.get("port") instanceof Number n ? n.intValue() : null);
                        properties.put("tls", Boolean.TRUE.equals(l.get("tls")));
                        Object auth = l.get("authentication") instanceof Map<?, ?> a ? a.get("type") : null;
                        properties.put("auth", auth == null ? "none" : auth.toString());
                        listeners.put(l.get("name").toString(), properties);
                    }
                }
            }
            if (rules.require() != null) {
                for (KafkaReference.RequiredListener required : rules.require()) {
                    requiredListener(required, listeners.get(required.name()), source);
                }
            }
            KafkaReference.ListenerProperties every = rules.every();
            if (every != null) {
                rule(
                        "kafka.listeners.every.type",
                        every.type(),
                        meets(every.type()),
                        source,
                        () -> perListener(listeners, l -> (String) l.get("type")));
                rule(
                        "kafka.listeners.every.tls",
                        every.tls(),
                        meets(every.tls()),
                        source,
                        () -> perListener(listeners, l -> (Boolean) l.get("tls")));
                rule(
                        "kafka.listeners.every.auth",
                        every.auth(),
                        meets(every.auth()),
                        source,
                        () -> perListener(listeners, l -> (String) l.get("auth")));
            }
        }

        /** One rule: the listener exists under its name with every property listed. */
        private void requiredListener(
                KafkaReference.RequiredListener required, Map<String, Object> found, String source) {
            Map<String, Object> expected = new LinkedHashMap<>();
            List<String> differ = new ArrayList<>();
            property("type", required.type(), found, expected, differ);
            property("port", required.port(), found, expected, differ);
            property("tls", required.tls(), found, expected, differ);
            property("auth", required.auth(), found, expected, differ);
            boolean holds = found != null && differ.isEmpty();
            results.add(new Result(
                    "kafka.listeners.require[" + required.name() + "]",
                    holds ? Status.PASS : required.severity() == Severity.WARN ? Status.WARN : Status.FAIL,
                    required.severity(),
                    expected,
                    found == null ? "missing" : found,
                    null,
                    found == null
                            ? "no listener named " + required.name()
                            : differ.isEmpty() ? null : String.join("; ", differ),
                    source,
                    null,
                    null));
        }

        /** The value of each pod of a role, by pod name; null for a pod without one. */
        private <T> Observation<T> perPod(String roleLabel, Function<Pod, T> value) {
            Map<String, T> values = new LinkedHashMap<>();
            for (Pod pod : rolePods(roleLabel)) {
                values.put(pod.getMetadata().getName(), value.apply(pod));
            }
            return new Observation<>(values, values.isEmpty() ? "no pod has " + roleLabel + "=true" : null);
        }

        private List<Pod> rolePods(String roleLabel) {
            return pods.get().stream()
                    .filter(p -> "true".equals(p.getMetadata().getLabels().get(roleLabel)))
                    .toList();
        }

        /**
         * Evaluates one rule: it holds when every subject's value meets the
         * constraint, and a subject with no value never does. With no subject
         * at all it does not hold, unless the observation says that holds.
         */
        private <T> void rule(
                String id,
                Constraint<?> constraint,
                Predicate<T> meets,
                String source,
                Supplier<Observation<T>> observe) {
            rule(id, constraint, meets, () -> source, observe);
        }

        /** A rule whose source is known once it has read: the zones' source depends on what it may list. */
        private <T> void rule(
                String id,
                Constraint<?> constraint,
                Predicate<T> meets,
                Supplier<String> source,
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
                        source.get(),
                        null,
                        null));
                return;
            }
            Map<String, T> values = observation.values();
            List<String> failing = values.entrySet().stream()
                    .filter(e -> e.getValue() == null || !meets.test(e.getValue()))
                    .map(Map.Entry::getKey)
                    .toList();
            boolean holds = failing.isEmpty() && (!values.isEmpty() || observation.emptyHolds());
            Status status = holds ? Status.PASS : constraint.severity() == Severity.WARN ? Status.WARN : Status.FAIL;
            Collection<String> shown = failing.isEmpty() ? values.keySet() : failing;
            List<Object> observed = shown.stream()
                    .map(values::get)
                    .map(ComplianceCheck::render)
                    .distinct()
                    .toList();
            List<String> named = failing.stream().filter(s -> !s.isEmpty()).toList();
            List<String> subjects = named.stream().limit(MAX_SUBJECTS).toList();
            results.add(new Result(
                    id,
                    status,
                    constraint.severity(),
                    expected(constraint),
                    observed.isEmpty() ? null : observed.size() == 1 ? observed.get(0) : observed,
                    subjects.isEmpty() ? null : subjects,
                    detail(
                            observation.detail(),
                            named.size() > MAX_SUBJECTS
                                    ? named.size() + " fall short; the first " + MAX_SUBJECTS + " are listed"
                                    : null),
                    source.get(),
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
                    constraint.oneOf().stream().map(ComplianceCheck::render).toList());
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

    /**
     * required when a required anti-affinity term on kubernetes.io/hostname
     * repels the pod's own labels, preferred when only a preferred term does,
     * otherwise none.
     */
    static String antiAffinity(Pod pod) {
        Affinity affinity = pod.getSpec().getAffinity();
        PodAntiAffinity anti = affinity == null ? null : affinity.getPodAntiAffinity();
        if (anti == null) {
            return "none";
        }
        Map<String, String> own = pod.getMetadata().getLabels();
        if (anti.getRequiredDuringSchedulingIgnoredDuringExecution() != null
                && anti.getRequiredDuringSchedulingIgnoredDuringExecution().stream()
                        .anyMatch(t -> repels(t, own))) {
            return "required";
        }
        if (anti.getPreferredDuringSchedulingIgnoredDuringExecution() != null
                && anti.getPreferredDuringSchedulingIgnoredDuringExecution().stream()
                        .anyMatch(t -> repels(t.getPodAffinityTerm(), own))) {
            return "preferred";
        }
        return "none";
    }

    private static boolean repels(PodAffinityTerm term, Map<String, String> labels) {
        return term != null && HOSTNAME.equals(term.getTopologyKey()) && matches(term.getLabelSelector(), labels);
    }

    /**
     * Whether a label selector selects these labels: every matchLabels pair,
     * and every In, NotIn, Exists or DoesNotExist expression. A missing
     * selector selects nothing, as Kubernetes reads one in an affinity term.
     */
    static boolean matches(LabelSelector selector, Map<String, String> labels) {
        if (selector == null) {
            return false;
        }
        Map<String, String> have = labels == null ? Map.of() : labels;
        if (selector.getMatchLabels() != null
                && !selector.getMatchLabels().entrySet().stream()
                        .allMatch(e -> e.getValue().equals(have.get(e.getKey())))) {
            return false;
        }
        if (selector.getMatchExpressions() == null) {
            return true;
        }
        for (LabelSelectorRequirement r : selector.getMatchExpressions()) {
            String value = have.get(r.getKey());
            List<String> values = r.getValues() == null ? List.of() : r.getValues();
            boolean ok =
                    switch (r.getOperator()) {
                        case "In" -> value != null && values.contains(value);
                        case "NotIn" -> value == null || !values.contains(value);
                        case "Exists" -> have.containsKey(r.getKey());
                        case "DoesNotExist" -> !have.containsKey(r.getKey());
                        default -> false;
                    };
            if (!ok) {
                return false;
            }
        }
        return true;
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

    @SuppressWarnings("unchecked")
    private static <T extends Comparable<? super T>> void property(
            String name,
            Constraint<T> constraint,
            Map<String, Object> found,
            Map<String, Object> expected,
            List<String> differ) {
        if (constraint == null) {
            return;
        }
        expected.put(name, expected(constraint));
        T value = found == null ? null : (T) found.get(name);
        if (found != null && (value == null || !constraint.holds(value, Comparator.naturalOrder()))) {
            differ.add(name + " is " + render(value));
        }
    }

    private static <T> Observation<T> perListener(
            Map<String, Map<String, Object>> listeners, Function<Map<String, Object>, T> value) {
        Map<String, T> values = new LinkedHashMap<>();
        listeners.forEach((name, properties) -> values.put(name, value.apply(properties)));
        return new Observation<>(values, values.isEmpty() ? "the Kafka CR declares no listener" : null);
    }

    /**
     * Each topic's value by name; the detail counts the topics that miss it, as the subjects stop at 50. With
     * every topic excluded, every topic left holds.
     */
    private static <V> Observation<Integer> perTopic(
            Map<String, V> topics, Function<V, Integer> value, Constraint<Integer> constraint) {
        Map<String, Integer> values = new TreeMap<>();
        topics.forEach((name, topic) -> values.put(name, value.apply(topic)));
        long missing = values.values().stream()
                .filter(v -> v == null || !meets(constraint).test(v))
                .count();
        return new Observation<>(
                values,
                values.isEmpty()
                        ? "no topic to check"
                        : missing == 0 ? null : missing + " of " + values.size() + " topics",
                true);
    }

    /** The smallest replica set across the topic's partitions. */
    static Integer replicationFactor(TopicDescription topic) {
        return topic.partitions().stream()
                .mapToInt(p -> p.replicas().size())
                .min()
                .orElse(0);
    }

    /** The effective value: the topic's own, or the broker default Kafka reports for it. */
    static Integer minInsyncReplicas(Config config) {
        ConfigEntry entry = config.get(TopicConfig.MIN_IN_SYNC_REPLICAS_CONFIG);
        return entry == null || entry.value() == null ? null : Integer.valueOf(entry.value());
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

    /** An Admin call's answer, within the 30 s the topology and health checks give one. */
    private static <T> T await(KafkaFuture<T> future) {
        try {
            return future.get(30, TimeUnit.SECONDS);
        } catch (ExecutionException e) {
            throw new IllegalStateException(e.getCause() != null ? e.getCause().getMessage() : e.getMessage(), e);
        } catch (TimeoutException e) {
            throw new IllegalStateException("no answer within 30 s", e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted", e);
        }
    }

    /**
     * Whether a PodMonitor looks in the namespace: any, one it names, or its
     * own when it names none, as prometheus-operator reads namespaceSelector.
     */
    static boolean watches(GenericKubernetesResource monitor, String namespace) {
        Map<?, ?> selector = at(monitor, "spec", "namespaceSelector") instanceof Map<?, ?> m ? m : Map.of();
        if (Boolean.TRUE.equals(selector.get("any"))) {
            return true;
        }
        return selector.get("matchNames") instanceof List<?> names && !names.isEmpty()
                ? names.contains(namespace)
                : namespace.equals(monitor.getMetadata().getNamespace());
    }

    /** A pool scaled to 0 replicas: it runs no node, so it decides no rule about the nodes. */
    private static boolean idle(GenericKubernetesResource pool) {
        return at(pool, "spec", "replicas") instanceof Number n && n.intValue() == 0;
    }

    private static String notCounted(List<String> idle) {
        return idle.isEmpty() ? null : String.join(", ", idle) + ": 0 replicas, not counted";
    }

    /** The parts that are set, joined; null when none is. */
    private static String detail(String... parts) {
        String joined = Arrays.stream(parts).filter(Objects::nonNull).collect(Collectors.joining("; "));
        return joined.isEmpty() ? null : joined;
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

    /**
     * The values a rule compares, by subject ("" for a rule about the cluster
     * as a whole). With no subject the rule does not hold, unless emptyHolds:
     * no pod on any node is within perNode's limit, and no topic left after
     * exclude misses a topics rule.
     */
    private record Observation<T>(Map<String, T> values, String detail, boolean emptyHolds) {
        Observation(Map<String, T> values, String detail) {
            this(values, detail, false);
        }
    }

    /** Each broker's config by id, and each broker whose config could not be read, with why. */
    private record BrokerConfigs(Map<String, Config> configs, List<String> unread) {}

    /** Each node's labels by name, and where they came from when not the nodes themselves. */
    /**
     * Each node's labels, and where they came from when not from the node
     * list. A partial source covers only some nodes: it does not answer for
     * the others.
     */
    private record NodeLabels(Map<String, Map<String, String>> byNode, String from, boolean partial) {
        Map<String, String> of(String node) {
            Map<String, String> labels = byNode.get(node);
            if (labels == null && partial) {
                throw new Unreadable(
                        node + " runs no broker Kafka reports, and without the node list the zones come from " + from);
            }
            return labels == null ? Map.of() : labels;
        }
    }

    /** The PodMonitors read, null when the CRD is missing, and whether they are only the Kafka namespace's. */
    private record Monitors(List<GenericKubernetesResource> items, boolean ownNamespaceOnly) {}

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

        /** The value once read, or null. */
        T peek() {
            return value;
        }
    }
}
