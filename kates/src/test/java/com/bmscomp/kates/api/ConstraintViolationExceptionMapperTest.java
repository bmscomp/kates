package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.ConstraintViolationException;
import jakarta.validation.Validation;
import jakarta.validation.Validator;
import jakarta.validation.ValidatorFactory;
import jakarta.ws.rs.core.Response;

import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

class ConstraintViolationExceptionMapperTest {

    private static ValidatorFactory factory;
    private static Validator validator;

    private final ConstraintViolationExceptionMapper mapper = new ConstraintViolationExceptionMapper();

    @BeforeAll
    static void buildValidator() {
        factory = Validation.buildDefaultValidatorFactory();
        validator = factory.getValidator();
    }

    @AfterAll
    static void closeValidator() {
        factory.close();
    }

    /**
     * The message names each field fieldErrors names, with the same reason,
     * sorted by field. It said only "Request validation failed", so the CLI,
     * which prints the message alone, never said which field to change. The
     * default messages depend on the JVM's locale, so those reasons are read
     * back from fieldErrors.
     */
    @Test
    void theMessageNamesEachFieldWithItsReason() {
        TestSpec spec = new TestSpec();
        spec.setTopic("not a topic!");
        spec.setNumProducers(1000);
        spec.setNumRecords(0);
        CreateTestRequest request = new CreateTestRequest();
        request.setSpec(spec);

        ApiError body = map(validator.validate(request));

        assertEquals("Validation Failed", body.getError());
        Map<String, String> fieldErrors = body.getFieldErrors();
        assertEquals(List.of("numProducers", "numRecords", "topic", "type"), List.copyOf(fieldErrors.keySet()));
        assertEquals(
                "numProducers: " + fieldErrors.get("numProducers")
                        + "; numRecords: " + fieldErrors.get("numRecords")
                        + "; topic: topic must be a legal Kafka topic name"
                        + "; type: Test type is required",
                body.getMessage());
    }

    /**
     * A field that breaks two constraints has one entry in fieldErrors, and
     * the message names it once, with that entry's reason.
     */
    @Test
    void aFieldBreakingTwoConstraintsIsNamedOnce() {
        TestSpec spec = new TestSpec();
        // Blank, and longer than 255 characters.
        spec.setConsumerGroup(" ".repeat(256));
        CreateTestRequest request = new CreateTestRequest();
        request.setType(TestType.LOAD);
        request.setSpec(spec);
        Set<ConstraintViolation<CreateTestRequest>> violations = validator.validate(request);
        assertEquals(2, violations.size());

        ApiError body = map(violations);

        assertEquals(Set.of("consumerGroup"), body.getFieldErrors().keySet());
        assertEquals("consumerGroup: " + body.getFieldErrors().get("consumerGroup"), body.getMessage());
    }

    /** An exception without violations has no field to name. */
    @Test
    void anExceptionWithoutViolationsKeepsTheGeneralMessage() {
        ApiError body = map(Set.of());

        assertEquals("Request validation failed", body.getMessage());
        assertEquals(Map.of(), body.getFieldErrors());
    }

    private ApiError map(Set<? extends ConstraintViolation<?>> violations) {
        Response response = mapper.toResponse(new ConstraintViolationException(violations));
        assertEquals(400, response.getStatus());
        return (ApiError) response.getEntity();
    }
}
