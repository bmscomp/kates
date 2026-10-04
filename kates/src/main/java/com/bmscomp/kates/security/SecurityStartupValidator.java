package com.bmscomp.kates.security;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;

import io.quarkus.runtime.StartupEvent;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Fails startup loudly instead of running "protected" by a missing or
 * well-known API key. Dev and test profiles disable API security
 * ({@code %dev.kates.api.security-enabled=false}), so this only bites
 * deployments that claim to be secured.
 */
@ApplicationScoped
public class SecurityStartupValidator {

    @ConfigProperty(name = "kates.api.security-enabled", defaultValue = "true")
    boolean securityEnabled;

    // Optional: SmallRye treats an empty-string property as missing, which
    // would fail plain String injection before this validator could speak.
    @ConfigProperty(name = "kates.api.key")
    java.util.Optional<String> configuredKey;

    void onStart(@Observes StartupEvent event) {
        if (!securityEnabled) {
            return;
        }
        String apiKey = configuredKey.orElse("");
        if (apiKey.isBlank() || "changeme".equals(apiKey)) {
            throw new IllegalStateException("kates.api.security-enabled=true but kates.api.key is "
                    + (apiKey.isBlank() ? "not set" : "the well-known default 'changeme'")
                    + ". Set the KATES_API_KEY environment variable (from a Kubernetes Secret) to a"
                    + " strong random value, or explicitly disable API auth for local development"
                    + " with KATES_API_SECURITY_ENABLED=false.");
        }
    }
}
