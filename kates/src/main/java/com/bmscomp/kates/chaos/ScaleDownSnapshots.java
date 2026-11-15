package com.bmscomp.kates.chaos;

import java.net.HttpURLConnection;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Predicate;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.ObjectMeta;
import io.fabric8.kubernetes.api.model.apps.StatefulSet;
import io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientException;
import org.jboss.logging.Logger;

/**
 * Undoes {@code SCALE_DOWN}. Every StatefulSet and KafkaNodePool of a namespace
 * that still carries the {@link KubernetesChaosProvider#ORIGINAL_REPLICAS_ANNOTATION}
 * snapshot gets that many replicas back, and loses the snapshot. Rollback and
 * startup orphan recovery both come here.
 *
 * <p>The snapshot, not the step's selector, says what to restore: a node pool
 * scaled down to zero has no pod left for a selector to match.
 */
public final class ScaleDownSnapshots {

    private static final Logger LOG = Logger.getLogger(ScaleDownSnapshots.class);

    private ScaleDownSnapshots() {}

    public record Restored(int statefulSets, int nodePools) {
        public int total() {
            return statefulSets + nodePools;
        }
    }

    /**
     * Restores the snapshots in {@code namespace} whose resource {@code eligible}
     * accepts. Failures are logged, and do not stop the other restores.
     *
     * @return how many resources got replicas back; a snapshot on a resource that
     *         already has its replicas is only cleared, and not counted
     */
    public static Restored restore(KubernetesClient client, String namespace, Predicate<ObjectMeta> eligible) {
        return new Restored(
                restoreStatefulSets(client, namespace, null, eligible),
                restoreNodePools(client, namespace, null, eligible));
    }

    /**
     * As {@link #restore}, in every namespace: a playbook can scale down outside Kafka's. Where the
     * backend may not list across namespaces (rbac.scope=namespace), {@code fallback} only.
     */
    public static Restored restoreEverywhere(
            KubernetesClient client, String fallback, Predicate<ObjectMeta> eligible) {
        return new Restored(
                restoreStatefulSets(client, null, fallback, eligible),
                restoreNodePools(client, null, fallback, eligible));
    }

    /** Lists in {@code namespace}, or in every namespace when it is null, else {@code fallback} on a 403. */
    private static <T> List<T> list(
            String namespace,
            String fallback,
            java.util.function.Function<String, List<T>> in,
            java.util.function.Supplier<List<T>> everywhere) {
        if (namespace != null) {
            return in.apply(namespace);
        }
        try {
            return everywhere.get();
        } catch (KubernetesClientException e) {
            if (e.getCode() != 403 || fallback == null) {
                throw e;
            }
            return in.apply(fallback);
        }
    }

    private static int restoreStatefulSets(
            KubernetesClient client, String namespace, String fallback, Predicate<ObjectMeta> eligible) {
        int restored = 0;
        try {
            for (StatefulSet ss : list(
                    namespace,
                    fallback,
                    ns -> client.apps().statefulSets().inNamespace(ns).list().getItems(),
                    () -> client.apps().statefulSets().inAnyNamespace().list().getItems())) {
                Integer original = snapshot(ss.getMetadata());
                if (original == null || !eligible.test(ss.getMetadata())) {
                    continue;
                }
                String name = ss.getMetadata().getName();
                String namespaceOf = ss.getMetadata().getNamespace();
                try {
                    int current =
                            ss.getSpec().getReplicas() != null ? ss.getSpec().getReplicas() : original;
                    if (current < original) {
                        LOG.warnf("Restoring StatefulSet %s from %d → %d", name, current, original);
                        client.apps()
                                .statefulSets()
                                .inNamespace(namespaceOf)
                                .withName(name)
                                .scale(original);
                        restored++;
                    }
                    client.apps()
                            .statefulSets()
                            .inNamespace(namespaceOf)
                            .withName(name)
                            .edit(s -> new StatefulSetBuilder(s)
                                    .editMetadata()
                                    .removeFromAnnotations(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION)
                                    .removeFromAnnotations(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION)
                                    .endMetadata()
                                    .build());
                } catch (KubernetesClientException e) {
                    LOG.warnf("Could not restore StatefulSet %s: %s", name, e.getMessage());
                }
            }
        } catch (KubernetesClientException e) {
            LOG.warnf("Could not list StatefulSets in %s to restore: %s", namespace, e.getMessage());
        }
        return restored;
    }

    private static int restoreNodePools(
            KubernetesClient client, String namespace, String fallback, Predicate<ObjectMeta> eligible) {
        int restored = 0;
        try {
            for (GenericKubernetesResource pool : list(
                    namespace,
                    fallback,
                    ns -> client.genericKubernetesResources(NodePoolScaleDown.NODE_POOLS)
                            .inNamespace(ns)
                            .list()
                            .getItems(),
                    () -> client.genericKubernetesResources(NodePoolScaleDown.NODE_POOLS)
                            .inAnyNamespace()
                            .list()
                            .getItems())) {
                Integer original = snapshot(pool.getMetadata());
                if (original == null || !eligible.test(pool.getMetadata())) {
                    continue;
                }
                String name = pool.getMetadata().getName();
                String namespaceOf = pool.getMetadata().getNamespace();
                int current = NodePoolScaleDown.replicas(pool);
                try {
                    // One patch: the replicas and the snapshot go together, so a
                    // failure leaves the snapshot for the next attempt.
                    client.genericKubernetesResources(NodePoolScaleDown.NODE_POOLS)
                            .inNamespace(namespaceOf)
                            .withName(name)
                            .edit(p -> {
                                if (current < original) {
                                    NodePoolScaleDown.spec(p).put("replicas", original);
                                }
                                Map<String, String> a =
                                        new HashMap<>(p.getMetadata().getAnnotations());
                                a.remove(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION);
                                a.remove(KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION);
                                p.getMetadata().setAnnotations(a);
                                return p;
                            });
                    if (current < original) {
                        LOG.warnf("Restoring KafkaNodePool %s from %d → %d", name, current, original);
                        restored++;
                    }
                } catch (KubernetesClientException e) {
                    LOG.warnf("Could not restore KafkaNodePool %s: %s", name, e.getMessage());
                }
            }
        } catch (KubernetesClientException e) {
            // No KafkaNodePool resource type: Kafka is not run by Strimzi here.
            if (e.getCode() != HttpURLConnection.HTTP_NOT_FOUND) {
                LOG.warnf("Could not list KafkaNodePools in %s to restore: %s", namespace, e.getMessage());
            }
        }
        return restored;
    }

    /** The original replica count recorded on a resource, or null when there is none to restore. */
    private static Integer snapshot(ObjectMeta meta) {
        Map<String, String> annotations = meta.getAnnotations();
        String snapshot =
                annotations != null ? annotations.get(KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION) : null;
        if (snapshot == null) {
            return null;
        }
        try {
            return Integer.parseInt(snapshot);
        } catch (NumberFormatException e) {
            LOG.warnf("Unparseable replica snapshot '%s' on %s, leaving it", snapshot, meta.getName());
            return null;
        }
    }
}
