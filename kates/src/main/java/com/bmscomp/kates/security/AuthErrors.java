package com.bmscomp.kates.security;

import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import com.bmscomp.kates.api.ApiError;

/** The JSON body of an authentication refusal: status, error and message. */
final class AuthErrors {

    private AuthErrors() {}

    static Response response(Response.Status status, String error, String message) {
        return Response.status(status)
                .type(MediaType.APPLICATION_JSON)
                .entity(ApiError.of(status.getStatusCode(), error, message))
                .build();
    }
}
