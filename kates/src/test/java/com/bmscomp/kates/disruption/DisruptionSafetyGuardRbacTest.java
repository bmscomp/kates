package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.fabric8.kubernetes.api.model.authorization.v1.ResourceAttributes;
import io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReview;
import io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.server.mock.EnableKubernetesMockClient;
import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;

/**
 * The dry-run RBAC check has to ask about the verb the provider sends and the
 * API group the resource lives in. A review with no group asks about the core
 * group, which a networking.k8s.io or apps rule never matches — so the dry run
 * reported "Insufficient RBAC permissions" even where the chart granted them.
 */
@EnableKubernetesMockClient
class DisruptionSafetyGuardRbacTest {

    private static final String SSAR_PATH = "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews";

    KubernetesMockServer server;
    KubernetesClient client;

    private DisruptionSafetyGuard guard;

    @BeforeEach
    void setup() {
        guard = new DisruptionSafetyGuard();
        guard.kubeClient = client;
    }

    @ParameterizedTest
    @CsvSource({
        "NETWORK_PARTITION, create, networking.k8s.io, networkpolicies",
        "SCALE_DOWN,        patch,  apps,              statefulsets",
        "ROLLING_RESTART,   patch,  apps,              statefulsets",
    })
    void asksForTheVerbAndGroupTheProviderUses(DisruptionType type, String verb, String group, String resource)
            throws InterruptedException {
        server.expect()
                .post()
                .withPath(SSAR_PATH)
                .andReturn(
                        201,
                        new SelfSubjectAccessReviewBuilder()
                                .withNewStatus()
                                .withAllowed(true)
                                .endStatus()
                                .build())
                .once();

        FaultSpec spec = FaultSpec.builder("rbac").disruptionType(type).build();

        assertTrue(guard.checkRbacPermissions(spec));

        ResourceAttributes asked = client.getKubernetesSerialization()
                .unmarshal(server.takeRequest().getUtf8Body(), SelfSubjectAccessReview.class)
                .getSpec()
                .getResourceAttributes();
        assertEquals(verb, asked.getVerb());
        assertEquals(group, asked.getGroup());
        assertEquals(resource, asked.getResource());
        assertEquals("kafka", asked.getNamespace());
    }
}
