package com.bmscomp.kates.security;

import io.quarkus.security.identity.request.BaseAuthenticationRequest;

/** An API key a request presented, for {@link ApiKeyIdentityProvider}. */
public final class ApiKeyAuthenticationRequest extends BaseAuthenticationRequest {

    private final String key;

    public ApiKeyAuthenticationRequest(String key) {
        this.key = key;
    }

    public String key() {
        return key;
    }
}
