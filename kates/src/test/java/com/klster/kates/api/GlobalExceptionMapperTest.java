package com.klster.kates.api;

import jakarta.ws.rs.core.Response;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeoutException;

import static org.junit.jupiter.api.Assertions.*;

class GlobalExceptionMapperTest {

    private GlobalExceptionMapper mapper;

    @BeforeEach
    void setUp() {
        mapper = new GlobalExceptionMapper();
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
    void unknownExceptionReturnsInternalServerError() {
        Response response = mapper.toResponse(new RuntimeException("something broke"));
        assertEquals(500, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertEquals("Internal Server Error", body.getError());
        assertTrue(body.getMessage().contains("something broke"));
    }

    @Test
    void exceptionWithNullMessageUsesClassName() {
        Response response = mapper.toResponse(new NullPointerException());
        assertEquals(500, response.getStatus());
        ApiError body = (ApiError) response.getEntity();
        assertNotNull(body.getMessage());
    }
}
