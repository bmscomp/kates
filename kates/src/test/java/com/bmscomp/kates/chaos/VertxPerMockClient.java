package com.bmscomp.kates.chaos;

import java.util.function.Consumer;

import io.fabric8.kubernetes.client.KubernetesClientBuilder;
import io.fabric8.kubernetes.client.vertx.VertxHttpClientFactory;

/**
 * Gives each fabric8 mock client a Vert.x instance of its own, closed with
 * the client. Left to itself, the builder loads Quarkus's HTTP client factory,
 * which outside a Quarkus application starts a Vert.x instance that nothing
 * closes: one per test, each holding a selector per event loop, until the test
 * JVM runs out of file descriptors (the JVM caps them at 10240 on macOS).
 *
 * <p>Use it as {@code @EnableKubernetesMockClient(kubernetesClientBuilderCustomizer = VertxPerMockClient.class)}.
 */
public class VertxPerMockClient implements Consumer<KubernetesClientBuilder> {

    @Override
    public void accept(KubernetesClientBuilder builder) {
        builder.withHttpClientFactory(new VertxHttpClientFactory());
    }
}
