package com.bmscomp.kates.chaos;

import java.util.HashMap;
import java.util.Map;

import io.fabric8.kubernetes.client.server.mock.KubernetesMockServer;
import io.fabric8.kubernetes.client.utils.Serialization;
import io.fabric8.mockwebserver.MockWebServer;
import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;

/**
 * Points the application's Kubernetes client at a mock API server that a test
 * can make lose an answer ({@link LossyDispatcher}). A test needs the
 * application's own beans for this, not ones it builds: the fault tolerance
 * interceptors, the retries among them, exist only on those.
 */
public class LossyApiServer implements QuarkusTestResourceLifecycleManager {

    /** One per test JVM, like the application the test resource starts; reset it before each test. */
    static final LossyDispatcher DISPATCHER = new LossyDispatcher();

    private KubernetesMockServer server;

    @Override
    public Map<String, String> start() {
        server = new KubernetesMockServer(
                // Spelled out: the lifecycle manager interface has a Context of its own.
                new io.fabric8.mockwebserver.Context(Serialization.jsonMapper()),
                new MockWebServer(),
                new HashMap<>(),
                DISPATCHER,
                false);
        server.init();
        return Map.of(
                "quarkus.kubernetes-client.api-server-url",
                server.url("/"),
                // Nothing at startup may write to the server the tests count writes on.
                "kates.chaos.orphan-recovery.enabled",
                "false");
    }

    @Override
    public void stop() {
        if (server != null) {
            server.destroy();
        }
    }
}
