package com.bmscomp.kates.security;

import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.QuarkusPrincipal;
import io.quarkus.security.runtime.QuarkusSecurityIdentity;

/** Builds the {@link SecurityIdentity} of a {@link KatesPrincipal}. */
public final class KatesIdentities {

    private KatesIdentities() {}

    /** principal's identity: its name, its scopes as roles, and itself under {@link KatesPrincipal#ATTRIBUTE}. */
    public static SecurityIdentity of(KatesPrincipal principal) {
        return QuarkusSecurityIdentity.builder()
                .setPrincipal(new QuarkusPrincipal(principal.name()))
                .addRoles(new java.util.HashSet<>(principal.scopes()))
                .addAttribute(KatesPrincipal.ATTRIBUTE, principal)
                .build();
    }

    /** The principal of identity, or null when it has none, as an anonymous identity has not. */
    public static KatesPrincipal principalOf(SecurityIdentity identity) {
        return identity == null ? null : identity.getAttribute(KatesPrincipal.ATTRIBUTE);
    }
}
