package com.bmscomp.kates.security;

import java.util.Set;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.handler.codec.http.HttpResponseStatus;
import io.quarkus.security.identity.IdentityProviderManager;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.identity.request.AuthenticationRequest;
import io.quarkus.vertx.http.runtime.security.ChallengeData;
import io.quarkus.vertx.http.runtime.security.HttpAuthenticationMechanism;
import io.quarkus.vertx.http.runtime.security.HttpSecurityUtils;
import io.smallrye.mutiny.Uni;
import io.vertx.ext.web.RoutingContext;

/**
 * Reads the API key of an HTTP request, from {@code Authorization: Bearer} or
 * {@code X-API-Key}, for every route Quarkus serves: the REST endpoints and
 * {@code /q/openapi} alike. It replaces a JAX-RS filter, which saw only the
 * REST endpoints and could give a request no identity.
 *
 * <p>Authentication is not proactive ({@code quarkus.http.auth.proactive}):
 * it runs only when a request reaches a route that needs an identity. Every
 * REST endpoint names the scope it needs ({@code @RolesAllowed}) unless it is
 * {@code @PermitAll}. A request without a key is anonymous, and then refused
 * with 401 ({@link UnauthorizedExceptionMapper}); one whose principal lacks
 * the scope, with 403 ({@link ForbiddenExceptionMapper}).
 *
 * <p>With {@code kates.api.security-enabled=false}, as in the dev and test
 * profiles, every request is {@link ApiKeys#UNSECURED}, with every scope.
 */
@ApplicationScoped
public class ApiKeyAuthenticationMechanism implements HttpAuthenticationMechanism {

    @Inject
    ApiKeys keys;

    @Override
    public Uni<SecurityIdentity> authenticate(RoutingContext context, IdentityProviderManager identityProviderManager) {
        if (!keys.securityEnabled()) {
            return Uni.createFrom().item(KatesIdentities.of(ApiKeys.UNSECURED));
        }
        String key = ApiKeys.presented(
                context.request().getHeader("Authorization"), context.request().getHeader("X-API-Key"));
        if (key == null) {
            return Uni.createFrom().nullItem();
        }
        return identityProviderManager.authenticate(
                HttpSecurityUtils.setRoutingContextAttribute(new ApiKeyAuthenticationRequest(key), context));
    }

    @Override
    public Uni<ChallengeData> getChallenge(RoutingContext context) {
        return Uni.createFrom()
                .item(new ChallengeData(
                        HttpResponseStatus.UNAUTHORIZED.code(), HttpHeaderNames.WWW_AUTHENTICATE, "Bearer"));
    }

    @Override
    public Set<Class<? extends AuthenticationRequest>> getCredentialTypes() {
        return Set.of(ApiKeyAuthenticationRequest.class);
    }
}
