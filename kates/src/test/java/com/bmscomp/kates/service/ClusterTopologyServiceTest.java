package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import io.fabric8.kubernetes.api.model.StatusBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.VertxPerMockClient;

/** Topology on an account that may read only some of it, such as the kates chart's with rbac.scope=namespace. */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class ClusterTopologyServiceTest {

    KubernetesClient client;
    KubernetesMockServer server;

    ClusterTopologyService service;

    @BeforeEach
    void setUp() {
        service = new ClusterTopologyService();
        service.kubernetesClient = client;
        service.adminService = mock(KafkaAdminService.class);
        service.kafkaNamespace = "kafka";
        service.kafkaCluster = "krafter";
    }

    @Test
    void aClusterReadInFullHasNothingUnread() {
        assertEquals(Map.of(), service.describeTopology().get("unread"));
    }

    @Test
    @SuppressWarnings("unchecked")
    void aRefusedReadNamesItsSectionsInTheApiServersWords() {
        refuse("/apis/kafka.strimzi.io/v1/namespaces/kafka/kafkas/krafter", "get", "kafkas", "kafka.strimzi.io");
        refuse("/api/v1/nodes", "list", "nodes", "");
        refuse(
                "/apis/kafka.strimzi.io/v1/namespaces/kafka/kafkatopics?labelSelector=strimzi.io%2Fcluster%3Dkrafter",
                "list", "kafkatopics", "kafka.strimzi.io");
        refuse(
                "/apis/networking.k8s.io/v1/namespaces/kafka/networkpolicies",
                "list",
                "networkpolicies",
                "networking.k8s.io");

        Map<String, Object> topology = service.describeTopology();
        Map<String, String> unread = (Map<String, String>) topology.get("unread");

        assertEquals(
                List.of(
                        "cluster",
                        "kafkaConfig",
                        "entityOperator",
                        "cruiseControl",
                        "kafkaExporter",
                        "certificates",
                        "metrics",
                        "kubernetes",
                        "topics",
                        "networkPolicies"),
                new ArrayList<>(unread.keySet()));
        assertEquals(refusal("list", "kafkatopics", "kafka.strimzi.io"), unread.get("topics"));
        assertEquals(refusal("get", "kafkas", "kafka.strimzi.io"), unread.get("certificates"));
        // Node pools may be read, and there are none.
        assertEquals(List.of(), topology.get("nodePools"));
    }

    @Test
    void aRefusedPodListNamesTheGrantRatherThanKubernetes() {
        refuse("/api/v1/namespaces/kafka/pods", "list", "pods", "");

        var e = assertThrows(ClusterTopologyService.KubernetesNotAvailableException.class, service::describeTopology);

        assertEquals(
                "The Kates API's service account may not list pods in kafka: bind it a Role there that grants it,"
                        + " such as the kates chart's with rbac.scope=namespace. The API server said: "
                        + refusal("list", "pods", ""),
                e.getMessage());
    }

    void refuse(String path, String verb, String resource, String group) {
        server.expect()
                .get()
                .withPath(path)
                .andReturn(
                        403,
                        new StatusBuilder()
                                .withStatus("Failure")
                                .withReason("Forbidden")
                                .withCode(403)
                                .withMessage(refusal(verb, resource, group))
                                .build())
                .always();
    }

    /** What the API server says when RBAC refuses a request in the Kafka namespace, or a cluster-scoped one. */
    static String refusal(String verb, String resource, String group) {
        String where = resource.equals("nodes") ? " at the cluster scope" : " in the namespace \"kafka\"";
        return resource + (group.isEmpty() ? "" : "." + group)
                + " is forbidden: User \"system:serviceaccount:kates:kates\" cannot " + verb + " resource \""
                + resource + "\" in API group \"" + group + "\"" + where;
    }
}
