package com.bmscomp.kates.security;

import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.HttpHeaders;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.UriInfo;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

import io.quarkus.security.UnauthorizedException;
import org.jboss.logging.Logger;

/**
 * A request without an API key to an endpoint that needs one: 401, with the
 * body the API has always sent for it.
 */
@Provider
public class UnauthorizedExceptionMapper implements ExceptionMapper<UnauthorizedException> {

    private static final Logger LOG = Logger.getLogger(UnauthorizedExceptionMapper.class);

    @Context
    UriInfo uriInfo;

    @Context
    HttpHeaders headers;

    @Override
    public Response toResponse(UnauthorizedException exception) {
        // X-Forwarded-For is the caller's to set: logged at DEBUG and cleaned,
        // so an unauthenticated caller cannot forge log lines (CRLF injection)
        // or flood WARN-level logs at will.
        LOG.warnf("Unauthenticated request to %s", uriInfo.getPath());
        LOG.debugf("  claimed origin (unverified): %s", sanitizeHeader(headers.getHeaderString("X-Forwarded-For")));
        return AuthErrors.response(
                Response.Status.UNAUTHORIZED,
                "Missing API key",
                "Provide a token via 'Authorization: Bearer <key>' or 'X-API-Key: <key>' header");
    }

    /** Strips control characters and caps length before a client-supplied header is logged. */
    static String sanitizeHeader(String value) {
        if (value == null || value.isBlank()) {
            return "unknown";
        }
        String cleaned = value.replaceAll("[\\p{Cntrl}]", "");
        return cleaned.length() <= 128 ? cleaned : cleaned.substring(0, 128) + "…";
    }
}
