package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Optional;
import java.util.Properties;
import javax.security.auth.login.AppConfigurationEntry;

import org.apache.kafka.common.config.SaslConfigs;
import org.apache.kafka.common.security.JaasContext;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

/**
 * Pins the JAAS quoting fix. Credentials used to be concatenated into the JAAS
 * string raw, so a password containing a double quote closed the value early and
 * produced a config the login module rejects — surfacing as an opaque parse
 * error rather than "your password has a quote in it".
 */
class KafkaSecurityConfigTest {

    private static String passwordFrom(String jaas) {
        AppConfigurationEntry entry = JaasContext.loadClientContext(java.util.Map.of(
                        org.apache.kafka.common.config.SaslConfigs.SASL_JAAS_CONFIG,
                        new org.apache.kafka.common.config.types.Password(jaas)))
                .configurationEntries()
                .get(0);
        return (String) entry.getOptions().get("password");
    }

    private static String scramJaas(String user, String password) {
        return "org.apache.kafka.common.security.scram.ScramLoginModule required "
                + "username=" + KafkaSecurityConfig.jaasQuote(user) + " "
                + "password=" + KafkaSecurityConfig.jaasQuote(password) + ";";
    }

    @Test
    void plainPasswordRoundTrips() {
        assertEquals("s3cret", passwordFrom(scramJaas("kates", "s3cret")));
    }

    @Test
    void passwordWithDoubleQuoteRoundTrips() {
        String password = "pa\"ss";
        assertEquals(password, passwordFrom(scramJaas("kates", password)));
    }

    @Test
    void passwordWithBackslashRoundTrips() {
        String password = "pa\\ss";
        assertEquals(password, passwordFrom(scramJaas("kates", password)));
    }

    @Test
    void passwordWithBothRoundTrips() {
        String password = "a\\b\"c\\\"d";
        assertEquals(password, passwordFrom(scramJaas("kates", password)));
    }

    @Test
    void quoteEscapesBackslashBeforeQuotes() {
        // Backslash must be escaped first — escaping quotes first would leave
        // the added backslashes to be doubled by the later pass.
        assertEquals("\"a\\\\b\\\"c\"", KafkaSecurityConfig.jaasQuote("a\\b\"c"));
    }

    private static KafkaSecurityConfig config(
            String protocol, Optional<String> mechanism, Optional<String> user, Optional<String> password) {
        return new KafkaSecurityConfig(
                protocol,
                mechanism,
                user,
                password,
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty(),
                Optional.empty());
    }

    /**
     * A SASL protocol with no credentials used to produce a client carrying
     * {@code security.protocol} and {@code sasl.mechanism} but no {@code
     * sasl.jaas.config}. Kafka reads that as "look in the JVM's JAAS file", finds
     * none, and every client constructor throws {@code Could not find a
     * 'KafkaClient' entry in the JAAS configuration} — which took the whole
     * application down at boot, naming a file this project never uses instead of
     * the unset secret that caused it.
     */
    @Nested
    @DisplayName("SASL without credentials")
    class MissingCredentials {

        @Test
        @DisplayName("is refused, naming the property that is missing")
        void scramWithoutPasswordIsRefused() {
            IllegalStateException thrown = assertThrows(
                    IllegalStateException.class,
                    () -> config(
                            "SASL_PLAINTEXT",
                            Optional.of("SCRAM-SHA-512"),
                            Optional.of("kates-backend"),
                            Optional.empty()));
            assertTrue(
                    thrown.getMessage().contains("KATES_KAFKA_SASL_PASSWORD"),
                    "the message must name the unset secret, not the JVM JAAS file: " + thrown.getMessage());
            assertTrue(
                    thrown.getMessage().contains("PLAINTEXT"),
                    "and must offer the way out for a broker without auth: " + thrown.getMessage());
        }

        @Test
        @DisplayName("names both properties when neither is set")
        void scramWithoutEitherNamesBoth() {
            IllegalStateException thrown = assertThrows(
                    IllegalStateException.class,
                    () -> config("SASL_SSL", Optional.of("PLAIN"), Optional.empty(), Optional.empty()));
            assertTrue(thrown.getMessage().contains("KATES_KAFKA_SASL_USERNAME"), thrown.getMessage());
            assertTrue(thrown.getMessage().contains("KATES_KAFKA_SASL_PASSWORD"), thrown.getMessage());
        }

        @Test
        @DisplayName("does not apply to PLAINTEXT, which needs none")
        void plaintextNeedsNoCredentials() {
            assertDoesNotThrow(() -> config("PLAINTEXT", Optional.empty(), Optional.empty(), Optional.empty()));
        }

        @Test
        @DisplayName("does not apply to OAUTHBEARER, whose login module takes no password")
        void oauthbearerNeedsNoCredentials() {
            assertDoesNotThrow(
                    () -> config("SASL_SSL", Optional.of("OAUTHBEARER"), Optional.empty(), Optional.empty()));
        }

        @Test
        @DisplayName("does not apply to SSL, which authenticates with a certificate")
        void mutualTlsNeedsNoCredentials() {
            assertDoesNotThrow(() -> config("SSL", Optional.empty(), Optional.empty(), Optional.empty()));
        }
    }

    @Test
    @DisplayName("a configured SASL client always carries its JAAS entry")
    void configuredSaslCarriesJaas() {
        Properties props = new Properties();
        config("SASL_PLAINTEXT", Optional.of("SCRAM-SHA-512"), Optional.of("kates-backend"), Optional.of("s3cret"))
                .apply(props);
        assertEquals("SASL_PLAINTEXT", props.get("security.protocol"));
        assertEquals("SCRAM-SHA-512", props.get(SaslConfigs.SASL_MECHANISM));
        assertNotNull(props.get(SaslConfigs.SASL_JAAS_CONFIG), "sasl.mechanism without sasl.jaas.config is the bug");
        assertEquals("s3cret", passwordFrom((String) props.get(SaslConfigs.SASL_JAAS_CONFIG)));
    }
}
