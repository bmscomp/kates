package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.Optional;

import io.fabric8.kubernetes.api.model.PodBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import org.junit.jupiter.api.Test;

/**
 * The Kates API finds its node from its own pod, so drains can be kept off
 * it, and knows none outside a pod.
 */
@EnableKubernetesMockClient(crud = true, kubernetesClientBuilderCustomizer = VertxPerMockClient.class)
class KatesNodeTest {

    KubernetesClient client;

    private KatesNode inPod(String pod) {
        KatesNode node = new KatesNode() {
            @Override
            String podName() {
                return pod;
            }
        };
        node.client = client;
        return node;
    }

    private void createPod(String name, String node) {
        client.pods()
                .inNamespace(client.getNamespace())
                .resource(new PodBuilder()
                        .withNewMetadata()
                        .withName(name)
                        .endMetadata()
                        .withNewSpec()
                        .withNodeName(node)
                        .endSpec()
                        .build())
                .create();
    }

    @Test
    void itIsTheNodeOfThePodNamedLikeTheHost() {
        createPod("kates-5c7d9", "sigma");

        assertEquals(Optional.of("sigma"), inPod("kates-5c7d9").name());
    }

    @Test
    void itIsLookedUpOnce() {
        createPod("kates-5c7d9", "sigma");
        KatesNode node = inPod("kates-5c7d9");
        node.name();

        client.pods().inNamespace(client.getNamespace()).withName("kates-5c7d9").delete();

        assertEquals(Optional.of("sigma"), node.name());
    }

    @Test
    void outsideAPodItIsUnknown() {
        createPod("kates-5c7d9", "sigma");

        assertEquals(Optional.empty(), inPod(null).name());
        assertEquals(Optional.empty(), inPod("my-laptop").name());
    }
}
