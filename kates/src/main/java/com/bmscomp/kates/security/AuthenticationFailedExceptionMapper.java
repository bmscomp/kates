package com.bmscomp.kates.security;

import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.UriInfo;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

import io.quarkus.security.AuthenticationFailedException;
import org.jboss.logging.Logger;

/**
 * An API key no principal holds: 403, with the body the API has always sent
 * for it. Quarkus would answer 401 with a challenge; the CLI and the
 * troubleshooting docs tell a wrong key (403) from a missing one (401).
 */
@Provider
public class AuthenticationFailedExceptionMapper implements ExceptionMapper<AuthenticationFailedException> {

    private static final Logger LOG = Logger.getLogger(AuthenticationFailedExceptionMapper.class);

    @Context
    UriInfo uriInfo;

    @Override
    public Response toResponse(AuthenticationFailedException exception) {
        LOG.warnf("Invalid API key for request to %s", uriInfo.getPath());
        return AuthErrors.response(Response.Status.FORBIDDEN, "Invalid API key", "The provided API key is not valid");
    }
}
