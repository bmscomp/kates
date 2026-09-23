package com.bmscomp.kates.chaos;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Predicate;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.ObjectMetaBuilder;
import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.base.CustomResourceDefinitionContext;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;

/**
 * A Strimzi Kafka cluster in the fabric8 mock server: KafkaNodePools with a
 * status, their pods as a StrimziPodSet runs them, and the Kafka resource.
 * {@link #operator} stands in for the Cluster Operator reconciling a node pool
 * scale-down.
 */
public final class StrimziTestCluster {

    public static final String NAMESPACE = "kafka";
    public static final String CLUSTER = "krafter";

    private final KubernetesClient client;

    public StrimziTestCluster(KubernetesMockServer server, KubernetesClient client) {
        this.client = client;
        for (var ctx : List.of(NodePoolScaleDown.NODE_POOLS, NodePoolScaleDown.KAFKAS)) {
            server.expectCustomResource(new CustomResourceDefinitionContext.Builder()
                    .withGroup(ctx.getGroup())
                    .withVersion(ctx.getVersion())
                    .withKind(ctx.getKind())
                    .withPlural(ctx.getPlural())
                    .withScope(ctx.getScope())
                    .withStatusSubresource(true)
                    .build());
        }
    }

    /** A reconciled node pool and its pods. {@code roles} is {@code broker}, {@code controller} or both, comma-separated. */
    public StrimziTestCluster pool(String name, String roles, int... nodeIds) {
        List<String> roleList = Arrays.asList(roles.split(","));
        GenericKubernetesResource pool = new GenericKubernetesResource();
        pool.setApiVersion("kafka.strimzi.io/v1");
        pool.setKind("KafkaNodePool");
        pool.setMetadata(new ObjectMetaBuilder()
                .withName(name)
                .withNamespace(NAMESPACE)
                .addToLabels("strimzi.io/cluster", CLUSTER)
                .build());
        pool.setAdditionalProperty("spec", new HashMap<>(Map.of("replicas", nodeIds.length, "roles", roleList)));
        nodePools().resource(pool).create();
        setStatus(name, Arrays.stream(nodeIds).boxed().toList(), 1);

        for (int id : nodeIds) {
            client.pods()
                    .inNamespace(NAMESPACE)
                    .resource(new PodBuilder()
                            .withNewMetadata()
                            .withName(CLUSTER + "-" + name + "-" + id)
                            .withNamespace(NAMESPACE)
                            .addToLabels("strimzi.io/cluster", CLUSTER)
                            .addToLabels("strimzi.io/component-type", "kafka")
                            .addToLabels("strimzi.io/pool-name", name)
                            .addToLabels("strimzi.io/broker-role", String.valueOf(roleList.contains("broker")))
                            .addToLabels("strimzi.io/controller-role", String.valueOf(roleList.contains("controller")))
                            .addNewOwnerReference()
                            .withApiVersion("core.strimzi.io/v1")
                            .withKind("StrimziPodSet")
                            .withName(CLUSTER + "-" + name)
                            .withUid("podset-" + name)
                            .endOwnerReference()
                            .endMetadata()
                            .withNewStatus()
                            .withPhase("Running")
                            .addNewCondition()
                            .withType("Ready")
                            .withStatus("True")
                            .endCondition()
                            .endStatus()
                            .build())
                    .create();
        }
        return this;
    }

    /** The Kafka resource, with or without a remove-brokers auto-rebalance. */
    public StrimziTestCluster kafka(boolean removeBrokersAutoRebalance) {
        GenericKubernetesResource kafka = new GenericKubernetesResource();
        kafka.setApiVersion("kafka.strimzi.io/v1");
        kafka.setKind("Kafka");
        kafka.setMetadata(new ObjectMetaBuilder()
                .withName(CLUSTER)
                .withNamespace(NAMESPACE)
                .build());
        Map<String, Object> cruiseControl = removeBrokersAutoRebalance
                ? Map.of("autoRebalance", List.of(Map.of("mode", "add-brokers"), Map.of("mode", "remove-brokers")))
                : Map.of();
        kafka.setAdditionalProperty("spec", Map.of("cruiseControl", cruiseControl));
        client.genericKubernetesResources(NodePoolScaleDown.KAFKAS)
                .inNamespace(NAMESPACE)
                .resource(kafka)
                .create();
        return this;
    }

    public GenericKubernetesResource nodePool(String name) {
        return nodePools().withName(name).get();
    }

    public int replicas(String pool) {
        return NodePoolScaleDown.replicas(nodePool(pool));
    }

    public Map<String, String> annotations(String pool) {
        Map<String, String> annotations = nodePool(pool).getMetadata().getAnnotations();
        return annotations != null ? annotations : Map.of();
    }

    public List<String> pods() {
        return client.pods().inNamespace(NAMESPACE).list().getItems().stream()
                .map(p -> p.getMetadata().getName())
                .sorted()
                .toList();
    }

    /**
     * The pool as a SCALE_DOWN long finished leaves it: {@code replicas} in its
     * spec and status, its original size recorded, and the removed pods gone.
     */
    public void scaledDown(String pool, int replicas) {
        scaledDown(pool, replicas, 0);
    }

    /** {@link #scaledDown(String, int)}, by a step that ran at {@code atMillis}. */
    public void scaledDown(String pool, int replicas, long atMillis) {
        GenericKubernetesResource before = nodePool(pool);
        int original = NodePoolScaleDown.replicas(before);
        List<Integer> nodeIds = NodePoolScaleDown.nodeIds(before);
        GenericKubernetesResource after = nodePools().withName(pool).edit(p -> {
            p.getMetadata()
                    .setAnnotations(Map.of(
                            KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION, String.valueOf(original),
                            KubernetesChaosProvider.SCALED_DOWN_AT_ANNOTATION, String.valueOf(atMillis)));
            NodePoolScaleDown.spec(p).put("replicas", replicas);
            return p;
        });
        for (int id : nodeIds.subList(replicas, nodeIds.size())) {
            client.pods()
                    .inNamespace(NAMESPACE)
                    .withName(CLUSTER + "-" + pool + "-" + id)
                    .delete();
        }
        setStatus(pool, nodeIds.subList(0, replicas), after.getMetadata().getGeneration());
    }

    public void setStatus(String pool, List<Integer> nodeIds, long observedGeneration) {
        nodePools().withName(pool).editStatus(p -> {
            p.setAdditionalProperty(
                    "status",
                    Map.of(
                            "nodeIds",
                            nodeIds,
                            "replicas",
                            nodeIds.size(),
                            "observedGeneration",
                            observedGeneration,
                            "roles",
                            p.get("spec", "roles")));
            return p;
        });
    }

    /**
     * Stands in for the Cluster Operator. On each pass it reconciles every pool
     * whose generation it has not observed or whose scale-down is pending. When
     * {@code removes} accepts the pool it deletes the pods with the highest node
     * IDs and drops them from the status; otherwise it holds the scale-down
     * back, as when the brokers still host partition replicas: every node stays
     * in the status, which records the generation all the same.
     */
    public Operator operator(Predicate<String> removes) {
        return new Operator(removes);
    }

    public final class Operator implements AutoCloseable {
        private final AtomicBoolean done = new AtomicBoolean();
        private final Thread thread;

        private Operator(Predicate<String> removes) {
            thread = new Thread(() -> {
                while (!done.get()) {
                    nodePools().list().getItems().forEach(p -> reconcile(p, removes));
                    try {
                        Thread.sleep(30);
                    } catch (InterruptedException e) {
                        return;
                    }
                }
            });
            thread.start();
        }

        private void reconcile(GenericKubernetesResource pool, Predicate<String> removes) {
            String name = pool.getMetadata().getName();
            long generation = pool.getMetadata().getGeneration();
            long observed = pool.get("status", "observedGeneration") instanceof Number n ? n.longValue() : 0;
            List<Integer> nodeIds = new ArrayList<>(NodePoolScaleDown.nodeIds(pool));
            int replicas = NodePoolScaleDown.replicas(pool);
            if (observed >= generation && replicas >= nodeIds.size()) {
                return;
            }
            if (removes.test(name)) {
                while (nodeIds.size() > replicas) {
                    int id = nodeIds.removeLast();
                    client.pods()
                            .inNamespace(NAMESPACE)
                            .withName(CLUSTER + "-" + name + "-" + id)
                            .delete();
                }
            }
            setStatus(name, List.copyOf(nodeIds), generation);
        }

        @Override
        public void close() throws InterruptedException {
            done.set(true);
            thread.join();
        }
    }

    private io.fabric8.kubernetes.client.dsl.NonNamespaceOperation<
                    GenericKubernetesResource,
                    io.fabric8.kubernetes.api.model.GenericKubernetesResourceList,
                    io.fabric8.kubernetes.client.dsl.Resource<GenericKubernetesResource>>
            nodePools() {
        return client.genericKubernetesResources(NodePoolScaleDown.NODE_POOLS).inNamespace(NAMESPACE);
    }
}
