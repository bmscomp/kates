package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;
import java.util.Optional;

import io.smallrye.config.PropertiesConfigSource;
import io.smallrye.config.SmallRyeConfigBuilder;
import org.eclipse.microprofile.config.Config;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * The reactive-messaging channels used to dial {@code localhost:9092} in every
 * environment: the connector reads its own {@code bootstrap.servers} and nothing
 * ever set it, so the outbox emitter and webhook consumer were pointed at a
 * broker that was not there while the rest of the application talked to the real
 * one. These pin the customizer that closes the gap, including the cases where
 * it must keep its hands off.
 */
class MessagingKafkaConfigCustomizerTest {

    private static final String REAL_BROKER = "krafter-kafka-bootstrap.kafka.svc:9092";

    private static Config channelConfig(Map<String, String> properties) {
        return new SmallRyeConfigBuilder()
                .withSources(new PropertiesConfigSource(properties, "channel", 100))
                .build();
    }

    /** Plaintext: no credentials anywhere. */
    private static KafkaSecurityConfig plaintext() {
        return security("PLAINTEXT", Optional.empty(), Optional.empty(), Optional.empty());
    }

    private static KafkaSecurityConfig scram(String user, String password) {
        return security("SASL_PLAINTEXT", Optional.of("SCRAM-SHA-512"), Optional.of(user), Optional.of(password));
    }

    private static KafkaSecurityConfig security(
            String protocol, Optional<String> mechanism, Optional<String> user, Optional<String> password) {
        return new KafkaSecurityConfig(
                protocol,
                mechanism,
                user,
                password,
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.of("/etc/kafka/ssl/truststore/ca.p12"),
                Optional.of("trustpass"),
                Optional.empty(),
                Optional.empty());
    }

    /**
     * Mirrors what SmallRye actually hands a customizer: the client config it
     * has already derived from the channel, plus the channel config itself. The
     * two agree in production, so a test that seeds only one of them is testing
     * a situation that cannot happen.
     */
    private static Map<String, Object> customize(KafkaSecurityConfig security, Map<String, String> channelProperties) {
        Map<String, Object> derived = new java.util.HashMap<>(channelProperties);
        derived.remove("tls-configuration-name"); // a connector option, not a client one
        return new MessagingKafkaConfigCustomizer(REAL_BROKER, security)
                .customize("test-events-out", channelConfig(channelProperties), derived);
    }

    @Test
    @DisplayName("an unconfigured channel is pointed at the application's broker")
    void unconfiguredChannelGetsTheRealBroker() {
        assertEquals(REAL_BROKER, customize(plaintext(), Map.of()).get("bootstrap.servers"));
    }

    @Test
    @DisplayName("the connector's localhost default is treated as nobody having chosen")
    void connectorDefaultIsOverridden() {
        // This is the exact production bug: the value was present, so a naive
        // "is it set?" check would have left it alone.
        Map<String, Object> result = customize(plaintext(), Map.of("bootstrap.servers", "localhost:9092"));
        assertEquals(REAL_BROKER, result.get("bootstrap.servers"));
    }

    @Test
    @DisplayName("an explicit broker wins — this is what keeps Dev Services working")
    void explicitBrokerIsPreserved() {
        // Dev Services publishes its container's address this way. Overriding it
        // would send dev mode at a Kubernetes DNS name that does not resolve.
        Map<String, Object> result = customize(plaintext(), Map.of("bootstrap.servers", "localhost:34567"));
        assertEquals("localhost:34567", result.get("bootstrap.servers"));
    }

    @Test
    @DisplayName("SASL credentials reach the channel, not just the clients Kates builds itself")
    void saslIsApplied() {
        Map<String, Object> result = customize(scram("kates-backend", "s3cret"), Map.of());

        assertEquals("SASL_PLAINTEXT", result.get("security.protocol"));
        assertEquals("SCRAM-SHA-512", result.get("sasl.mechanism"));
        assertTrue(
                ((String) result.get("sasl.jaas.config")).contains("username=\"kates-backend\""),
                "the JAAS entry should carry the configured user: " + result.get("sasl.jaas.config"));
    }

    @Test
    @DisplayName("plaintext adds no security keys at all")
    void plaintextStaysBare() {
        Map<String, Object> result = customize(plaintext(), Map.of());

        assertEquals(Map.of("bootstrap.servers", REAL_BROKER), result);
    }

    @Test
    @DisplayName("a channel that names its own SASL mechanism keeps it")
    void explicitSaslSettingIsPreserved() {
        Map<String, Object> result = customize(scram("kates-backend", "s3cret"), Map.of("sasl.mechanism", "PLAIN"));

        assertEquals("PLAIN", result.get("sasl.mechanism"), "the channel's own choice was overwritten");
        // The rest of the credentials still arrive: only the stated key is left alone.
        assertEquals("SASL_PLAINTEXT", result.get("security.protocol"));
    }

    @Test
    @DisplayName("TLS handled by the registry is left to the registry")
    void managedTlsSuppressesRawSslKeys() {
        // Quarkus rejects tls-configuration-name alongside raw ssl.* keys. Which
        // customizer runs first would otherwise decide whether that check fires
        // or is silently bypassed.
        Map<String, Object> result =
                customize(scram("kates-backend", "s3cret"), Map.of("tls-configuration-name", "kafka-tls"));

        assertFalse(result.containsKey("ssl.truststore.location"), "raw SSL config would conflict with the registry");
        assertEquals("SCRAM-SHA-512", result.get("sasl.mechanism"), "SASL is unrelated to TLS and still belongs");
    }

    @Test
    @DisplayName("the caller's map is not mutated")
    void inputMapIsNotMutated() {
        // SmallRye passes a map it may reuse; Map.of() would throw, but an
        // ordinary HashMap would silently accumulate across channels.
        Map<String, Object> original = new java.util.HashMap<>(Map.of("key.serializer", "StringSerializer"));
        Map<String, Object> result = new MessagingKafkaConfigCustomizer(REAL_BROKER, plaintext())
                .customize("test-events-out", channelConfig(Map.of()), original);

        assertEquals(Map.of("key.serializer", "StringSerializer"), original);
        assertEquals(REAL_BROKER, result.get("bootstrap.servers"));
    }
}
