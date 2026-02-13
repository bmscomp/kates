package com.klster.kates.api;

import jakarta.validation.ConstraintViolation;
import jakarta.validation.ConstraintViolationException;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

import java.util.Map;
import java.util.stream.Collectors;

/**
 * Maps Bean Validation constraint violations into structured JSON error responses.
 */
@Provider
public class ConstraintViolationExceptionMapper implements ExceptionMapper<ConstraintViolationException> {

    @Override
    public Response toResponse(ConstraintViolationException e) {
        Map<String, String> fieldErrors = e.getConstraintViolations().stream()
                .collect(Collectors.toMap(
                        v -> extractFieldName(v),
                        ConstraintViolation::getMessage,
                        (a, b) -> a
                ));

        ApiError error = new ApiError(400, "Validation Failed", "Request validation failed");
        error.setFieldErrors(fieldErrors);

        return Response.status(Response.Status.BAD_REQUEST)
                .entity(error)
                .build();
    }

    private String extractFieldName(ConstraintViolation<?> violation) {
        String path = violation.getPropertyPath().toString();
        int lastDot = path.lastIndexOf('.');
        return lastDot >= 0 ? path.substring(lastDot + 1) : path;
    }
}
