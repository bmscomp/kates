package com.bmscomp.kates.api;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.stream.Collectors;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.ConstraintViolationException;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

/**
 * Maps Bean Validation constraint violations into structured JSON error responses.
 *
 * <p>fieldErrors keys each field by the last segment of its property path, and
 * the message names each of those fields with its reason, as a 400 built in
 * code does ({@link ApiError#validationFailed}). The message said only
 * "Request validation failed", so a client that prints only the message, such
 * as the CLI, never said which field to change.
 */
@Provider
public class ConstraintViolationExceptionMapper implements ExceptionMapper<ConstraintViolationException> {

    @Override
    public Response toResponse(ConstraintViolationException e) {
        // Sorted by field, then reason, so that a field breaking two
        // constraints keeps the same reason each time, and the message names
        // the fields in the same order.
        Map<String, String> fieldErrors = new LinkedHashMap<>();
        e.getConstraintViolations().stream()
                .map(v -> Map.entry(extractFieldName(v), v.getMessage()))
                .sorted(Map.Entry.<String, String>comparingByKey().thenComparing(Map.Entry.comparingByValue()))
                .forEach(field -> fieldErrors.putIfAbsent(field.getKey(), field.getValue()));
        // An exception without violations has no field to name.
        String message = fieldErrors.isEmpty()
                ? "Request validation failed"
                : fieldErrors.entrySet().stream()
                        .map(field -> field.getKey() + ": " + field.getValue())
                        .collect(Collectors.joining("; "));

        return Response.status(Response.Status.BAD_REQUEST)
                .entity(ApiError.validationFailed(message, fieldErrors))
                .build();
    }

    private String extractFieldName(ConstraintViolation<?> violation) {
        String path = violation.getPropertyPath().toString();
        int lastDot = path.lastIndexOf('.');
        return lastDot >= 0 ? path.substring(lastDot + 1) : path;
    }
}
