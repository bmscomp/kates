package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.*;

import io.fabric8.kubernetes.client.Config;
import io.fabric8.kubernetes.client.ConfigBuilder;
import io.fabric8.kubernetes.client.KubernetesClientException;
import io.fabric8.kubernetes.client.http.HttpClient;
import io.fabric8.kubernetes.client.vertx.VertxHttpClientFactory;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * A {@code NO_PROXY} with a {@code *.} wildcard, as curl reads it, made the
 * Kubernetes client throw while building its HTTP client, so Kates did not
 * start: {@code MalformedURLException: NO_PROXY URL contains invalid entry:
 * '*.svc.cluster.local'}.
 */
class KubernetesNoProxyCustomizerTest {

    @Test
    @DisplayName("Entries the client accepts are left exactly as they are")
    void validEntriesUntouched() {
        String[] in = {"localhost", "127.0.0.1", "10.0.0.0/8", ".svc.cluster.local", "::1", "api.example.com:443"};
        assertArrayEquals(in, KubernetesNoProxyCustomizer.normalize(in));
    }

    @Test
    @DisplayName("*.domain becomes the .domain suffix the client matches")
    void wildcardDomainBecomesSuffix() {
        assertArrayEquals(
                new String[] {".svc.cluster.local", "localhost"},
                KubernetesNoProxyCustomizer.normalize(new String[] {"*.svc.cluster.local", "localhost"}));
    }

    @Test
    @DisplayName("A lone * becomes the empty suffix, which matches every host")
    void loneStarMatchesEverything() {
        assertArrayEquals(new String[] {""}, KubernetesNoProxyCustomizer.normalize(new String[] {"*"}));
    }

    @Test
    @DisplayName("Spaces around an entry are trimmed; an entry still invalid is dropped")
    void trimsAndDropsWhatCannotBeRead() {
        assertArrayEquals(
                new String[] {"example.com", ".corp.internal"},
                KubernetesNoProxyCustomizer.normalize(
                        new String[] {" example.com ", "*.corp.internal ", "my_host", "[::1]"}));
    }

    @Test
    @DisplayName("The client builds its HTTP client from the environment that used to stop Kates")
    void clientBuildsWithWildcardNoProxy() {
        Config config = new ConfigBuilder(Config.empty())
                .withMasterUrl("https://kubernetes.default.svc:443")
                .withHttpsProxy("http://proxy.example.com:3128")
                .withNoProxy("localhost", ".svc.cluster.local", "*.svc.cluster.local")
                .build();

        // newBuilder applies the proxy settings, and is where the entries are checked.
        HttpClient.Factory factory = new VertxHttpClientFactory();
        KubernetesClientException refused =
                assertThrows(KubernetesClientException.class, () -> factory.newBuilder(config));
        assertTrue(String.valueOf(refused.getCause()).contains("*.svc.cluster.local"), String.valueOf(refused));

        new KubernetesNoProxyCustomizer().customize(config);
        assertDoesNotThrow(() -> factory.newBuilder(config));
        assertArrayEquals(new String[] {"localhost", ".svc.cluster.local", ".svc.cluster.local"}, config.getNoProxy());
    }

    @Test
    @DisplayName("A config with no NO_PROXY is left alone")
    void noNoProxy() {
        Config config = new ConfigBuilder(Config.empty()).build();
        config.setNoProxy(null);
        new KubernetesNoProxyCustomizer().customize(config);
        assertNull(config.getNoProxy());
    }
}
