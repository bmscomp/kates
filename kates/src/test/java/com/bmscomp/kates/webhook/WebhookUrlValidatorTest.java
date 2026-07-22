package com.bmscomp.kates.webhook;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Optional;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Pure unit test of the SSRF policy (no Quarkus, no network — IP literals
 * avoid DNS except where a hostname is the point of the test).
 */
class WebhookUrlValidatorTest {

    private WebhookUrlValidator validator;

    @BeforeEach
    void setUp() {
        validator = new WebhookUrlValidator();
        validator.allowedSchemes = List.of("http", "https");
        validator.blockPrivateAddresses = false;
        validator.allowLoopback = false;
        validator.allowlist = Optional.empty();
    }

    @Test
    void acceptsPublicAddress() {
        assertDoesNotThrow(() -> validator.validate("https://93.184.216.34/hook"));
    }

    @Test
    void rejectsDisallowedScheme() {
        var e = assertThrows(IllegalArgumentException.class, () -> validator.validate("ftp://93.184.216.34/x"));
        assertTrue(e.getMessage().contains("scheme"));
    }

    @Test
    void rejectsMissingHost() {
        assertThrows(IllegalArgumentException.class, () -> validator.validate("http:///nohost"));
    }

    @Test
    void rejectsLoopbackIp() {
        var e = assertThrows(IllegalArgumentException.class, () -> validator.validate("http://127.0.0.1:9000/x"));
        assertTrue(e.getMessage().contains("loopback"));
    }

    @Test
    void rejectsLocalhostHostname() {
        assertThrows(IllegalArgumentException.class, () -> validator.validate("http://localhost/cb"));
    }

    @Test
    void allowsLoopbackWhenExplicitlyEnabled() {
        validator.allowLoopback = true;
        assertDoesNotThrow(() -> validator.validate("http://127.0.0.1:9000/x"));
    }

    @Test
    void rejectsCloudMetadataEndpoint() {
        var e = assertThrows(
                IllegalArgumentException.class, () -> validator.validate("http://169.254.169.254/latest/meta-data"));
        assertTrue(e.getMessage().contains("link-local"));
    }

    @Test
    void privateAddressesAllowedByDefaultForInClusterTargets() {
        assertDoesNotThrow(() -> validator.validate("http://10.42.0.15:8080/hook"));
    }

    @Test
    void privateAddressesRejectedWhenBlocked() {
        validator.blockPrivateAddresses = true;
        var e = assertThrows(IllegalArgumentException.class, () -> validator.validate("http://10.42.0.15:8080/hook"));
        assertTrue(e.getMessage().contains("private"));
    }

    @Test
    void allowlistRestrictsUrls() {
        validator.allowlist = Optional.of(List.of("https://hooks.internal.example/"));
        assertThrows(IllegalArgumentException.class, () -> validator.validate("https://93.184.216.34/hook"));
    }
}
