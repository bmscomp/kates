package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.TreeSet;

import io.fabric8.kubernetes.api.model.OwnerReference;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;

/**
 * Decides what a {@code SCALE_DOWN} step scales down. The step works on the
 * workloads that run the pods it selects, and each of them loses one broker.
 * The provider and the safety guard both go through here, so the dry run names
 * the brokers the step will remove.
 *
 * <p>The selected pods are {@code targetPod} when it is set, otherwise every pod
 * {@code targetLabel} matches in {@code targetNamespace}. {@code targetAll} and
 * {@code targetBrokerId} do not apply. A pod run by a StrimziPodSet selects its
 * KafkaNodePool, and a pod run by a StatefulSet selects that StatefulSet. Strimzi
 * only scales down node pools whose nodes are brokers and nothing else, so a
 * pod with the controller role is skipped. So is a pod owned by neither.
 */
public final class ScaleDownTargets {

    static final String CLUSTER_LABEL = "strimzi.io/cluster";
    static final String POOL_LABEL = "strimzi.io/pool-name";
    static final String BROKER_ROLE_LABEL = "strimzi.io/broker-role";
    static final String CONTROLLER_ROLE_LABEL = "strimzi.io/controller-role";

    private ScaleDownTargets() {}

    /** A KafkaNodePool, with the Kafka cluster it belongs to. */
    public record Pool(String cluster, String name) {}

    /**
     * @param pools        node pools that lose one broker each, in name order
     * @param statefulSets StatefulSets that lose one replica each, in name order
     * @param skipped      why selected pods that select neither were left out
     */
    public record Targets(List<Pool> pools, List<String> statefulSets, List<String> skipped) {
        public boolean isEmpty() {
            return pools.isEmpty() && statefulSets.isEmpty();
        }
    }

    /**
     * @param targets     what the step scales down
     * @param removedPods the broker each target loses
     * @param notes       why a selected pod removes no broker
     */
    public record Preview(Targets targets, List<String> removedPods, List<String> notes) {}

    /**
     * The targets of {@code spec}, looking its pods up in {@code targetNamespace}.
     *
     * @throws IllegalStateException when the step selects no pod
     */
    public static Targets resolve(KubernetesClient client, FaultSpec spec) {
        String namespace = spec.targetNamespace();
        if (hasTargetPod(spec)) {
            Pod pod = client.pods()
                    .inNamespace(namespace)
                    .withName(spec.targetPod())
                    .get();
            if (pod == null) {
                throw new IllegalStateException("Pod not found: " + spec.targetPod());
            }
            return of(List.of(pod));
        }
        ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
        List<Pod> pods = client.pods()
                .inNamespace(namespace)
                .withLabelSelector(selector.toString())
                .list()
                .getItems();
        if (pods.isEmpty()) {
            throw new IllegalStateException(
                    "No pods found matching label selector '" + selector + "' in namespace '" + namespace + "'");
        }
        return of(pods);
    }

    /**
     * What {@code spec} would remove, read from {@code pods} without calling
     * the API. {@code pods} must hold every pod of the workloads involved: the
     * broker a node pool loses is the one with its highest node ID, and a
     * StatefulSet loses its highest ordinal but never its last replica.
     */
    public static Preview preview(FaultSpec spec, List<Pod> pods) {
        List<Pod> selected;
        if (hasTargetPod(spec)) {
            selected = pods.stream()
                    .filter(p -> spec.targetPod().equals(p.getMetadata().getName()))
                    .toList();
        } else {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
            selected = pods.stream()
                    .filter(p -> selector.matches(p.getMetadata().getLabels()))
                    .toList();
        }
        Targets targets = of(selected);
        List<String> removed = new ArrayList<>();
        List<String> notes = new ArrayList<>(targets.skipped());
        for (Pool pool : targets.pools()) {
            highest(pods.stream().filter(p -> pool.equals(pool(p))).toList()).ifPresent(removed::add);
        }
        for (String statefulSet : targets.statefulSets()) {
            List<Pod> own = pods.stream()
                    .filter(p ->
                            owner(p).filter(o -> isStatefulSet(o, statefulSet)).isPresent())
                    .toList();
            if (own.size() > 1) {
                highest(own).ifPresent(removed::add);
            } else {
                notes.add("StatefulSet " + statefulSet + " has one replica, and SCALE_DOWN leaves the last one");
            }
        }
        return new Preview(targets, removed, notes);
    }

    static Targets of(List<Pod> selected) {
        Set<Pool> pools = new TreeSet<>(Comparator.comparing(Pool::name).thenComparing(Pool::cluster));
        Set<String> statefulSets = new TreeSet<>();
        Set<String> skipped = new LinkedHashSet<>();
        for (Pod pod : selected) {
            String name = pod.getMetadata().getName();
            Optional<OwnerReference> owner = owner(pod);
            if (owner.isEmpty()) {
                skipped.add(name + " is not run by a StrimziPodSet or a StatefulSet");
            } else if ("StatefulSet".equals(owner.get().getKind())) {
                statefulSets.add(owner.get().getName());
            } else {
                Pool pool = pool(pod);
                Map<String, String> labels = labels(pod);
                if (pool == null) {
                    skipped.add(name + " has no " + POOL_LABEL + " label");
                } else if (!"true".equals(labels.get(BROKER_ROLE_LABEL))
                        || "true".equals(labels.get(CONTROLLER_ROLE_LABEL))) {
                    skipped.add("node pool " + pool.name()
                            + " runs KRaft controllers, and Strimzi only scales down broker-only pools");
                } else {
                    pools.add(pool);
                }
            }
        }
        return new Targets(List.copyOf(pools), List.copyOf(statefulSets), List.copyOf(skipped));
    }

    private static boolean hasTargetPod(FaultSpec spec) {
        return spec.targetPod() != null && !spec.targetPod().isEmpty();
    }

    private static Optional<OwnerReference> owner(Pod pod) {
        List<OwnerReference> owners = pod.getMetadata().getOwnerReferences();
        if (owners == null) {
            return Optional.empty();
        }
        return owners.stream()
                .filter(o -> "StrimziPodSet".equals(o.getKind()) || "StatefulSet".equals(o.getKind()))
                .findFirst();
    }

    private static boolean isStatefulSet(OwnerReference owner, String name) {
        return "StatefulSet".equals(owner.getKind()) && name.equals(owner.getName());
    }

    /** The node pool of a pod run by a StrimziPodSet; null for any other pod. */
    private static Pool pool(Pod pod) {
        if (owner(pod).filter(o -> "StrimziPodSet".equals(o.getKind())).isEmpty()) {
            return null;
        }
        Map<String, String> labels = labels(pod);
        String cluster = labels.get(CLUSTER_LABEL);
        String pool = labels.get(POOL_LABEL);
        return cluster == null || pool == null ? null : new Pool(cluster, pool);
    }

    private static Map<String, String> labels(Pod pod) {
        Map<String, String> labels = pod.getMetadata().getLabels();
        return labels != null ? labels : Map.of();
    }

    /** The pod whose name ends in the highest number: a node ID or an ordinal. */
    private static Optional<String> highest(List<Pod> pods) {
        return pods.stream()
                .map(p -> p.getMetadata().getName())
                .filter(n -> ordinal(n) >= 0)
                .max(Comparator.comparingInt(ScaleDownTargets::ordinal));
    }

    static int ordinal(String podName) {
        String suffix = podName.substring(podName.lastIndexOf('-') + 1);
        try {
            return Integer.parseInt(suffix);
        } catch (NumberFormatException e) {
            return -1;
        }
    }
}
