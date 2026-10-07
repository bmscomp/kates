package com.bmscomp.kates.security;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.security.AuthenticationFailedException;
import io.quarkus.security.identity.AuthenticationRequestContext;
import io.quarkus.security.identity.IdentityProvider;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.mutiny.Uni;

/**
 * Turns the key {@link ApiKeyAuthenticationMechanism} read into the identity
 * of the principal that holds it. A key no principal holds fails with
 * {@link AuthenticationFailedException}, which the REST layer answers with
 * 403 ({@link AuthenticationFailedExceptionMapper}).
 */
@ApplicationScoped
public class ApiKeyIdentityProvider implements IdentityProvider<ApiKeyAuthenticationRequest> {

    @Inject
    ApiKeys keys;

    @Override
    public Class<ApiKeyAuthenticationRequest> getRequestType() {
        return ApiKeyAuthenticationRequest.class;
    }

    @Override
    public Uni<SecurityIdentity> authenticate(
            ApiKeyAuthenticationRequest request, AuthenticationRequestContext context) {
        return keys.resolve(request.key())
                .map(principal -> Uni.createFrom().item(KatesIdentities.of(principal)))
                .orElseGet(() -> Uni.createFrom().failure(new AuthenticationFailedException("Invalid API key")));
    }
}
