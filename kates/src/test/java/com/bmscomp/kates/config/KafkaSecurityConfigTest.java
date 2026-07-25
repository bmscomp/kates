package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.assertEquals;

import javax.security.auth.login.AppConfigurationEntry;

import org.apache.kafka.common.security.JaasContext;
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
}
