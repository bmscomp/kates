package com.bmscomp.kates.audit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;

import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;

import com.bmscomp.kates.security.KatesIdentities;
import com.bmscomp.kates.security.KatesPrincipal;

/** The actor of the current REST request, from its identity. */
@ApplicationScoped
public class Actors {

    @Inject
    Instance<SecurityIdentity> identity;

    /**
     * The principal of the current REST request's key, or null outside a
     * request, or when the request has no principal: no key, or a wrong one.
     */
    public Actor current() {
        if (!Arc.container().requestContext().isActive()) {
            return null;
        }
        try {
            SecurityIdentity id = identity.get();
            if (id == null || id.isAnonymous()) {
                return null;
            }
            KatesPrincipal principal = KatesIdentities.principalOf(id);
            return principal != null ? Actor.of(principal) : null;
        } catch (RuntimeException e) {
            // Reading the identity of a request whose key was refused fails
            // the way its authentication did.
            return null;
        }
    }
}
