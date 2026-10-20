package com.bmscomp.kates.conformity;

import java.time.LocalDate;
import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.databind.JsonNode;
import io.fabric8.kubernetes.api.model.Quantity;

/**
 * The Kafka platform a team means to run, as a conformity.kates.io/v1alpha1
 * KafkaReference: each leaf constraint is one rule the live cluster is checked
 * against, and its path under {@code spec} is the rule's id.
 *
 * <p>cli/examples/conformity holds the JSON Schema editors validate against,
 * and two examples. {@link KafkaReferenceReader} reads one strictly and holds
 * it to the schema's limits; a field left out is null, and declares no rule.
 */
public record KafkaReference(String apiVersion, String kind, Metadata metadata, Spec spec) {

    public record Metadata(String name, String description) {}

    /**
     * @param target the cluster the reference is for; when it names another
     *     than the one Kates checks, the check stops
     * @param zoneLabel the node label that names a zone; topology.kubernetes.io/zone when null
     */
    public record Spec(
            Target target,
            String zoneLabel,
            Versions versions,
            Role controllers,
            Role brokers,
            Kafka kafka,
            Topics topics,
            Monitoring monitoring,
            List<Waiver> waivers) {}

    /** Each one optional: strimzi, the only platform in v1alpha1, and the Kafka CR's namespace, name and cluster id. */
    public record Target(String platform, String namespace, String name, String clusterId) {}

    public record Versions(Constraint<Version> kubernetes, Constraint<Version> strimzi, Constraint<Version> kafka) {}

    /**
     * The nodes of one role, controllers or brokers.
     *
     * @param dedicated controllers only: no node pool has both roles
     * @param antiAffinity required, preferred or none
     */
    public record Role(
            Constraint<Integer> count,
            Constraint<Boolean> dedicated,
            Resources requests,
            Resources limits,
            Storage storage,
            Constraint<Integer> zones,
            Constraint<Integer> skew,
            Constraint<Integer> perNode,
            Constraint<String> antiAffinity) {}

    /** Of each node's kafka container. */
    public record Resources(Constraint<Quantity> cpu, Constraint<Quantity> memory) {}

    /**
     * @param type ephemeral, persistent-claim or jbod
     * @param size the bound capacity of each node's volumes, added up
     */
    public record Storage(
            Constraint<String> type,
            Constraint<Quantity> size,
            @JsonProperty("class") Constraint<String> storageClass) {}

    /**
     * @param source where config and rackAwareness read from: cr, the Kafka CR
     *     (the default, no Kafka access), or brokers, the Kafka Admin API
     * @param authorization none, simple, opa, keycloak or custom
     * @param config broker config name to the value the Kafka CR sets, or with
     *     source brokers the effective value every broker reports
     */
    public record Kafka(
            String source,
            Constraint<Boolean> rackAwareness,
            Constraint<String> authorization,
            Listeners listeners,
            Map<String, Constraint<JsonNode>> config) {}

    /**
     * @param require listeners that must exist under their name, each one rule
     * @param every properties every listener must have, whatever its name
     */
    public record Listeners(List<RequiredListener> require, ListenerProperties every) {}

    /**
     * One rule: the listener exists with every property listed. Its severity is
     * the rule's, fail unless it says warn, and its properties take none of
     * their own.
     */
    public record RequiredListener(
            String name,
            Constraint<String> type,
            Constraint<Integer> port,
            Constraint<Boolean> tls,
            Constraint<String> auth,
            Severity severity) {

        public RequiredListener {
            severity = severity != null ? severity : Severity.FAIL;
        }
    }

    /**
     * @param type internal, cluster-ip, route, loadbalancer, nodeport or ingress
     * @param auth none, tls, scram-sha-512, oauth or custom
     */
    public record ListenerProperties(Constraint<String> type, Constraint<Boolean> tls, Constraint<String> auth) {}

    /**
     * Each topic's own durability, internal topics included, read through the
     * Kafka Admin API whatever kafka.source says.
     *
     * @param replicationFactor the smallest replica set across the topic's partitions
     * @param minInsyncReplicas the topic's effective min.insync.replicas
     * @param exclude topic-name prefixes left out, such as __ for Kafka's internal topics
     */
    public record Topics(
            Constraint<Integer> replicationFactor, Constraint<Integer> minInsyncReplicas, List<String> exclude) {}

    /** @param metrics none, jmxPrometheusExporter or strimziMetricsReporter */
    public record Monitoring(
            Constraint<String> metrics, Constraint<Boolean> kafkaExporter, Constraint<Boolean> podMonitor) {}

    /**
     * A known gap: the rule it names is reported as WAIVED, and stops counting
     * towards the verdict, until the end of {@code until} in UTC.
     */
    public record Waiver(String rule, String reason, String by, LocalDate until) {}
}
