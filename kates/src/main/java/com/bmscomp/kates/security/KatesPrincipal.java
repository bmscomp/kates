package com.bmscomp.kates.security;

import java.util.List;

/**
 * Who an API key is: its name, whether a person or an agent holds it, its
 * scopes, and the Kafka clusters it may act on (empty: any).
 *
 * <p>The identity of every authenticated request carries one under
 * {@link #ATTRIBUTE}, with its scopes as the identity's roles.
 */
public record KatesPrincipal(String name, Type type, List<String> scopes, List<String> allowedClusterIds) {

    public static final String ATTRIBUTE = "kates.principal";

    public enum Type {
        HUMAN,
        AGENT;

        /** The lower-case name the API answers with. */
        public String wireName() {
            return name().toLowerCase(java.util.Locale.ROOT);
        }
    }

    public KatesPrincipal {
        scopes = List.copyOf(scopes);
        allowedClusterIds = List.copyOf(allowedClusterIds);
    }
}
