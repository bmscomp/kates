package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.GenericKubernetesResourceList;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientException;
import io.fabric8.kubernetes.client.dsl.NonNamespaceOperation;
import io.fabric8.kubernetes.client.dsl.Resource;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.ScaleDownTargets.Pool;

/**
 * {@code SCALE_DOWN} on Strimzi. Strimzi runs Kafka pods from StrimziPodSets
 * and creates no StatefulSet, so a broker is removed by lowering
 * {@code spec.replicas} of its KafkaNodePool. The Cluster Operator reconciles
 * as soon as the pool changes and removes the pool's highest node ID.
 *
 * <p>The operator holds back a scale-down whose broker still hosts partition
 * replicas. It reverts the change in memory for the reconciliation, keeps every
 * node in the pool's status, and checks again at each reconciliation. The
 * lowered {@code spec.replicas} stays, so the broker would be removed later, as
 * soon as nothing is left on it: in the middle of another step, or after the
 * plan. So a scale-down that does not finish within {@code chaosDurationSec} is
 * undone: every pool the step lowered gets its replicas back, and the step
 * fails. A held-back scale-down fails at once, unless the Kafka resource has a
 * {@code remove-brokers} auto-rebalance: then Cruise Control is moving the
 * replicas off, and the operator removes the broker when it is done.
 */
final class NodePoolScaleDown {

    private static final Logger LOG = Logger.getLogger(NodePoolScaleDown.class);

    static final CustomResourceDefinitionContext NODE_POOLS = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withKind("KafkaNodePool")
            .withPlural("kafkanodepools")
            .withScope("Namespaced")
            .build();

    static final CustomResourceDefinitionContext KAFKAS = new CustomResourceDefinitionContext.Builder()
            .withGroup("kafka.strimzi.io")
            .withVersion("v1")
            .withKind("Kafka")
            .withPlural("kafkas")
            .withScope("Namespaced")
            .build();

    private final KubernetesClient client;
    private final long pollIntervalMs;

    /** A pool this step lowered, with what it had before, so a failure can put it back. */
    private record Change(Pool pool, int before, List<Integer> nodeIdsBefore, long generation, boolean snapshotted) {}

    private enum Progress {
        /** The operator has not reconciled the change yet, or the broker pod is still shutting down. */
        PENDING,
        /** The operator reconciled the change and kept every node. */
        HELD_BACK,
        REMOVED
    }

    NodePoolScaleDown(KubernetesClient client, long pollIntervalMs) {
        this.client = client;
        this.pollIntervalMs = pollIntervalMs;
    }

    /**
     * Lowers each pool by one, then waits up to {@code chaosDurationSec} for the
     * operator to remove the brokers. {@code chaosDurationSec} 0 does not wait.
     *
     * @throws KubernetesChaosProvider.IncompleteScaleDownException when the
     *         operator holds a scale-down back or does not finish in time; the
     *         pools have their replicas back by then
     */
    void run(FaultSpec spec, List<Pool> pools) throws InterruptedException {
        String namespace = spec.targetNamespace();
        Map<Pool, GenericKubernetesResource> current = check(namespace, pools);

        List<Change> changes = new ArrayList<>();
        try {
            for (Pool pool : pools) {
                changes.add(lower(namespace, pool, current.get(pool)));
            }
            if (spec.chaosDurationSec() <= 0) {
                LOG.info(
                        "SCALE_DOWN: chaosDurationSec is 0, not waiting for the Cluster Operator to remove the brokers");
                return;
            }
            await(spec, changes);
        } catch (Exception e) {
            revert(namespace, changes);
            throw e;
        }
    }

    /** Reads every pool and refuses the step before anything changes. */
    private Map<Pool, GenericKubernetesResource> check(String namespace, List<Pool> pools) {
        Map<Pool, GenericKubernetesResource> current = new LinkedHashMap<>();
        for (Pool pool : pools) {
            GenericKubernetesResource resource =
                    nodePools(namespace).withName(pool.name()).get();
            if (resource == null) {
                throw new IllegalStateException(
                        "KafkaNodePool " + pool.name() + " not found in namespace '" + namespace + "'");
            }
            if (!roles(resource).equals(List.of("broker"))) {
                throw new IllegalStateException("KafkaNodePool " + pool.name() + " has roles " + roles(resource)
                        + ", and Strimzi only scales down broker-only pools");
            }
            List<Integer> nodeIds = nodeIds(resource);
            int replicas = replicas(resource);
            if (nodeIds == null) {
                throw new IllegalStateException("KafkaNodePool " + pool.name()
                        + " has no node IDs in its status: the Cluster Operator has not reconciled it");
            }
            if (nodeIds.size() != replicas || observedGeneration(resource) < generation(resource)) {
                throw new IllegalStateException("KafkaNodePool " + pool.name()
                        + " is already changing size (spec.replicas "
                        + replicas + ", " + nodeIds.size() + " nodes in its status). SCALE_DOWN does not add a change"
                        + " to one the Cluster Operator has not finished");
            }
            if (replicas == 0) {
                throw new IllegalStateException("KafkaNodePool " + pool.name() + " has no broker to remove");
            }
            current.put(pool, resource);
        }

        Map<String, Long> selectedPerCluster =
                pools.stream().collect(Collectors.groupingBy(Pool::cluster, Collectors.counting()));
        for (Map.Entry<String, Long> e : selectedPerCluster.entrySet()) {
            int brokers =
                    nodePools(namespace)
                            .withLabel(ScaleDownTargets.CLUSTER_LABEL, e.getKey())
                            .list()
                            .getItems()
                            .stream()
                            .filter(p -> roles(p).contains("broker"))
                            .mapToInt(NodePoolScaleDown::replicas)
                            .sum();
            if (brokers - e.getValue() < 1) {
                throw new IllegalStateException("SCALE_DOWN would remove the last broker of Kafka cluster " + e.getKey()
                        + " (" + brokers + " brokers, " + e.getValue() + " node pools to lose one each)");
            }
        }
        return current;
    }

    /** Takes one replica off the pool, recording the original count on it unless an earlier step did. */
    private Change lower(String namespace, Pool pool, GenericKubernetesResource before) {
        int replicas = replicas(before);
        Map<String, String> annotations = before.getMetadata().getAnnotations();
        boolean snapshot =
                annotations == null || !annotations.containsKey(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION);
        String scaledDownAt = String.valueOf(System.currentTimeMillis());

        GenericKubernetesResource after = nodePools(namespace)
                .withName(pool.name())
                .edit(p -> {
                    if (replicas(p) != replicas) {
                        throw new IllegalStateException(
                                "KafkaNodePool " + pool.name() + " changed size while SCALE_DOWN was lowering it");
                    }
                    if (snapshot) {
                        Map<String, String> a = new HashMap<>(
                                p.getMetadata().getAnnotations() != null
                                        ? p.getMetadata().getAnnotations()
                                        : Map.of());
                        a.put(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, String.valueOf(replicas));
                        a.put(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION, scaledDownAt);
                        p.getMetadata().setAnnotations(a);
                    }
                    spec(p).put("replicas", replicas - 1);
                    return p;
                });

        LOG.info("SCALE_DOWN: KafkaNodePool " + pool.name() + " from " + replicas + " → " + (replicas - 1));
        return new Change(pool, replicas, nodeIds(before), generation(after), snapshot);
    }

    private void await(FaultSpec spec, List<Change> changes) throws InterruptedException {
        String namespace = spec.targetNamespace();
        long deadline = System.nanoTime() + spec.chaosDurationSec() * 1_000_000_000L;
        Map<String, Boolean> drains = new HashMap<>();
        Map<Change, Progress> pending = new LinkedHashMap<>();
        changes.forEach(c -> pending.put(c, Progress.PENDING));

        while (true) {
            for (Change c : List.copyOf(pending.keySet())) {
                Progress progress = progress(namespace, c);
                if (progress == Progress.REMOVED) {
                    pending.remove(c);
                    continue;
                }
                pending.put(c, progress);
                if (progress == Progress.HELD_BACK
                        && !drains.computeIfAbsent(c.pool().cluster(), k -> drainsOnScaleDown(namespace, k))) {
                    throw new KubernetesChaosProvider.IncompleteScaleDownException(String.format(
                            "SCALE_DOWN: the Cluster Operator held back the scale-down of KafkaNodePool %s: the"
                                    + " broker it would remove still hosts partition replicas, and Kafka %s has no"
                                    + " remove-brokers auto-rebalance to move them off. Move them first (a"
                                    + " KafkaRebalance in remove-brokers mode), or set"
                                    + " strimzi.io/skip-broker-scaledown-check=true on the Kafka resource to remove"
                                    + " a broker that still has replicas. %s",
                            c.pool().name(), c.pool().cluster(), putBack(changes)));
                }
            }
            long remainingMs = (deadline - System.nanoTime()) / 1_000_000L;
            if (pending.isEmpty()) {
                LOG.info("SCALE_DOWN: the Cluster Operator removed a broker from "
                        + changes.stream().map(c -> c.pool().name()).toList());
                return;
            }
            if (remainingMs <= 0) {
                break;
            }
            Thread.sleep(Math.min(pollIntervalMs, remainingMs));
        }

        List<String> why = new ArrayList<>();
        pending.forEach((c, progress) -> {
            String state = progress == Progress.HELD_BACK
                    ? "held back while Cruise Control moves the partition replicas off the broker, which cannot"
                            + " finish when the brokers left cannot hold every replica"
                    : "broker not removed yet (has the Cluster Operator reconciled the change?)";
            why.add("KafkaNodePool " + c.pool().name() + ": " + state);
        });
        throw new KubernetesChaosProvider.IncompleteScaleDownException("SCALE_DOWN: not finished within"
                + " chaosDurationSec=" + spec.chaosDurationSec() + "s. " + String.join("; ", why) + ". "
                + putBack(changes));
    }

    /**
     * Where the operator is with a change. It writes the pool's status in the
     * same reconciliation that removes the broker: {@code observedGeneration}
     * reaches the change and the removed node leaves {@code nodeIds}. A
     * held-back scale-down writes the same generation with every node kept.
     */
    private Progress progress(String namespace, Change c) {
        GenericKubernetesResource resource;
        try {
            resource = nodePools(namespace).withName(c.pool().name()).get();
        } catch (KubernetesClientException e) {
            LOG.debug("SCALE_DOWN: could not read KafkaNodePool " + c.pool().name() + ", checking again", e);
            return Progress.PENDING;
        }
        if (resource == null) {
            throw new IllegalStateException("KafkaNodePool " + c.pool().name() + " was deleted during SCALE_DOWN");
        }
        List<Integer> nodeIds = nodeIds(resource);
        if (observedGeneration(resource) < c.generation() || nodeIds == null) {
            return Progress.PENDING;
        }
        if (nodeIds.size() >= c.before()) {
            return Progress.HELD_BACK;
        }
        for (Integer id : c.nodeIdsBefore()) {
            String pod = c.pool().cluster() + "-" + c.pool().name() + "-" + id;
            if (!nodeIds.contains(id)
                    && client.pods().inNamespace(namespace).withName(pod).get() != null) {
                return Progress.PENDING;
            }
        }
        return Progress.REMOVED;
    }

    /**
     * Whether the operator drains a held-back broker itself: the Kafka resource
     * has a {@code remove-brokers} auto-rebalance. Unreadable counts as yes, so
     * the step waits rather than failing on a guess.
     */
    private boolean drainsOnScaleDown(String namespace, String cluster) {
        try {
            GenericKubernetesResource kafka = client.genericKubernetesResources(KAFKAS)
                    .inNamespace(namespace)
                    .withName(cluster)
                    .get();
            if (kafka == null) {
                return true;
            }
            Object modes = kafka.get("spec", "cruiseControl", "autoRebalance");
            return modes instanceof List<?> list
                    && list.stream()
                            .anyMatch(m -> m instanceof Map<?, ?> map && "remove-brokers".equals(map.get("mode")));
        } catch (KubernetesClientException e) {
            LOG.debug("SCALE_DOWN: could not read Kafka " + cluster, e);
            return true;
        }
    }

    /** Gives every pool the step lowered its replicas back, and removes the snapshots the step wrote. */
    private void revert(String namespace, List<Change> changes) {
        for (Change c : changes) {
            try {
                nodePools(namespace).withName(c.pool().name()).edit(p -> {
                    spec(p).put("replicas", c.before());
                    if (c.snapshotted() && p.getMetadata().getAnnotations() != null) {
                        Map<String, String> a = new HashMap<>(p.getMetadata().getAnnotations());
                        a.remove(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION);
                        a.remove(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION);
                        p.getMetadata().setAnnotations(a);
                    }
                    return p;
                });
                LOG.warn("SCALE_DOWN: KafkaNodePool " + c.pool().name() + " back to " + c.before() + " replicas");
            } catch (KubernetesClientException e) {
                LOG.error(
                        "SCALE_DOWN: could not give KafkaNodePool " + c.pool().name() + " its " + c.before()
                                + " replicas back; rollback or orphan recovery restores it from its snapshot",
                        e);
            }
        }
    }

    private static String putBack(List<Change> changes) {
        return "Kates put the replicas back: "
                + changes.stream().map(c -> c.pool().name() + "=" + c.before()).collect(Collectors.joining(", "));
    }

    private NonNamespaceOperation<
                    GenericKubernetesResource, GenericKubernetesResourceList, Resource<GenericKubernetesResource>>
            nodePools(String namespace) {
        return client.genericKubernetesResources(NODE_POOLS).inNamespace(namespace);
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> spec(GenericKubernetesResource pool) {
        return (Map<String, Object>) pool.getAdditionalProperties().computeIfAbsent("spec", k -> new HashMap<>());
    }

    static int replicas(GenericKubernetesResource pool) {
        return pool.get("spec", "replicas") instanceof Number n ? n.intValue() : 0;
    }

    static List<String> roles(GenericKubernetesResource pool) {
        return pool.get("spec", "roles") instanceof List<?> roles
                ? roles.stream().map(String::valueOf).toList()
                : List.of();
    }

    /** {@code status.nodeIds}, or null before the operator has written a status. */
    static List<Integer> nodeIds(GenericKubernetesResource pool) {
        return pool.get("status", "nodeIds") instanceof List<?> ids
                ? ids.stream().map(id -> ((Number) id).intValue()).toList()
                : null;
    }

    private static long observedGeneration(GenericKubernetesResource pool) {
        return pool.get("status", "observedGeneration") instanceof Number n ? n.longValue() : -1;
    }

    private static long generation(GenericKubernetesResource pool) {
        Long generation = pool.getMetadata().getGeneration();
        return generation != null ? generation : 0;
    }
}
