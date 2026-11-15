package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeoutException;
import jakarta.ws.rs.core.Response;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class GlobalExceptionMapperTest {

    private GlobalExceptionMapper mapper;

    @BeforeEach
    void setUp() {
        mapper = new GlobalExceptionMapper();
    }

    @Test
    void aConnectionFailureIsAnOutage() {
        Response response = mapper.toResponse(new RuntimeException(new java.sql.SQLException("x", "08006")));
        assertEquals(503, response.getStatus());
        assertEquals("5", response.getHeaderString("Retry-After"));
        assertEquals(
                "The Kates API cannot reach its database; retry once it is back",
                ((ApiError) response.getEntity()).getMessage());
    }

    @Test
    void anAcquisitionTimeoutIsAnOutage() {
        Response response =
                mapper.toResponse(new RuntimeException(new java.sql.SQLException("Sorry, acquisition timeout!")));
        assertEquals(503, response.getStatus());
    }

    @Test
    void aDatabaseShuttingDownOrStartingIsAnOutage() {
        // What a CloudNativePG switchover leaves on the connections it kills.
        for (String state : new String[] {"57P01", "57P02", "57P03"}) {
            Response response = mapper.toResponse(new RuntimeException(new java.sql.SQLException("x", state)));
            assertEquals(503, response.getStatus(), state);
        }
    }

    @Test
    void aConstraintViolationIsNotAnOutage() {
        Response response = mapper.toResponse(new RuntimeException(new java.sql.SQLException("dup", "23505")));
        assertEquals(500, response.getStatus());
    }

    @Test
    void illegalArgumentReturnsBadRequest() {
        Response response = mapper.toResponse(new IllegalArgumentException("bad param"));
        assertEquals(400, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertEquals("Bad Request", body.getError());
        assertTrue(body.getMessage().contains("bad param"));
    }

    @Test
    void timeoutReturnsGatewayTimeout() {
        Response response = mapper.toResponse(new TimeoutException("timed out"));
        assertEquals(504, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertEquals("Gateway Timeout", body.getError());
    }

    @Test
    void wrappedTimeoutInExecutionExceptionReturns504() {
        ExecutionException wrapped = new ExecutionException(new TimeoutException("kafka timeout"));
        Response response = mapper.toResponse(wrapped);
        assertEquals(504, response.getStatus());
    }

    @Test
    void wrappedIllegalArgumentInExecutionExceptionReturns400() {
        ExecutionException wrapped = new ExecutionException(new IllegalArgumentException("invalid"));
        Response response = mapper.toResponse(wrapped);
        assertEquals(400, response.getStatus());
    }

    @Test
    void unknownExceptionReturnsInternalServerErrorWithoutLeakingInternals() {
        Response response = mapper.toResponse(new RuntimeException("something broke"));
        assertEquals(500, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertEquals("Internal Server Error", body.getError());
        // The internal exception message must NOT be echoed to clients
        // (information disclosure). A fixed, safe message is returned instead;
        // the real detail is in the server log.
        assertFalse(body.getMessage().contains("something broke"));
        assertTrue(body.getMessage().toLowerCase().contains("server error"));
    }

    @Test
    void exceptionWithNullMessageUsesClassName() {
        Response response = mapper.toResponse(new NullPointerException());
        assertEquals(500, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertNotNull(body.getMessage());
    }
}
