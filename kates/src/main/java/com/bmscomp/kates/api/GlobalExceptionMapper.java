package com.bmscomp.kates.api;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeoutException;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.ExceptionMapper;
import jakarta.ws.rs.ext.Provider;

import org.jboss.logging.Logger;

/**
 * Global safety net for any exception that leaks past endpoint-level try/catch.
 * Maps well-known exception types to appropriate HTTP status codes and always
 * returns a structured {@link ApiError} body.
 */
@Provider
public class GlobalExceptionMapper implements ExceptionMapper<Exception> {

    private static final Logger LOG = Logger.getLogger(GlobalExceptionMapper.class);

    @Override
    public Response toResponse(Exception exception) {
        Throwable root = unwrap(exception);

        if (root instanceof jakarta.ws.rs.WebApplicationException wae) {
            int status = wae.getResponse().getStatus();
            return error(status, Response.Status.fromStatusCode(status).getReasonPhrase(), root.getMessage());
        }

        if (root instanceof IllegalArgumentException) {
            return error(400, "Bad Request", root.getMessage());
        }

        if (root instanceof TimeoutException) {
            LOG.warn("Request timed out", exception);
            return error(504, "Gateway Timeout", root.getMessage());
        }

        if (isKafkaNotFound(root)) {
            return error(404, "Not Found", root.getMessage());
        }

        LOG.error("Unhandled exception in REST endpoint", exception);
        return error(
                500,
                "Internal Server Error",
                root.getMessage() != null
                        ? root.getMessage()
                        : exception.getClass().getSimpleName());
    }

    private static Response error(int status, String label, String message) {
        return Response.status(status)
                .type(MediaType.APPLICATION_JSON_TYPE)
                .entity(ApiError.of(status, label, message))
                .build();
    }

    private static Throwable unwrap(Throwable t) {
        if (t instanceof ExecutionException && t.getCause() != null) {
            return t.getCause();
        }
        return t;
    }

    private static boolean isKafkaNotFound(Throwable t) {
        return t.getClass().getName().equals("org.apache.kafka.common.errors.UnknownTopicOrPartitionException");
    }
}
