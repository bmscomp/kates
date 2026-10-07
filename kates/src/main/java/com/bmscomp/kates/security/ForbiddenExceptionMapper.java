package com.bmscomp.kates.security;

import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.UriInfo;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

import io.quarkus.security.ForbiddenException;
import org.jboss.logging.Logger;

/**
 * A valid API key whose principal lacks the scope an endpoint needs: 403, with
 * an error that tells it from a wrong key ("Invalid API key").
 */
@Provider
public class ForbiddenExceptionMapper implements ExceptionMapper<ForbiddenException> {

    private static final Logger LOG = Logger.getLogger(ForbiddenExceptionMapper.class);

    static final String MISSING_SCOPE =
            "The API key's principal lacks the scope this endpoint needs; GET /api/whoami lists its scopes";

    @Context
    UriInfo uriInfo;

    @Override
    public Response toResponse(ForbiddenException exception) {
        String message = exception.getMessage();
        LOG.warnf("Refused %s: %s", uriInfo.getPath(), message == null ? "missing scope" : message);
        return AuthErrors.response(
                Response.Status.FORBIDDEN, "Forbidden", message == null || message.isBlank() ? MISSING_SCOPE : message);
    }
}
