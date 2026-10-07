package com.bmscomp.kates.domain;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;
import java.util.stream.Collectors;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.Validation;
import jakarta.validation.Validator;
import jakarta.validation.ValidatorFactory;

import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * A spec's rates are -1, which is unlimited, or 1 or more. Both benchmark
 * backends run a rate below 1 unthrottled, so a 0 ran flat out, and the
 * {@code @Min(-1)} that held the rates let it through.
 */
class RateTest {

    private static ValidatorFactory factory;
    private static Validator validator;

    @BeforeAll
    static void startValidator() {
        factory = Validation.buildDefaultValidatorFactory();
        validator = factory.getValidator();
    }

    @AfterAll
    static void closeValidator() {
        factory.close();
    }

    private static Map<String, String> violations(TestSpec spec) {
        return validator.validate(spec).stream()
                .collect(Collectors.toMap(v -> v.getPropertyPath().toString(), ConstraintViolation::getMessage));
    }

    private static TestSpec rated(int rate) {
        TestSpec spec = new TestSpec();
        spec.setThroughput(rate);
        spec.setTargetThroughput(rate);
        return spec;
    }

    @ParameterizedTest
    @ValueSource(ints = {0, -2, Integer.MIN_VALUE})
    void aRateBelowOneOtherThanMinusOneIsOutsideTheLimits(int rate) {
        assertEquals(
                Map.of(
                        "throughput", "throughput must be -1 (unlimited) or positive",
                        "targetThroughput", "targetThroughput must be -1 (unlimited) or positive"),
                violations(rated(rate)));
    }

    @ParameterizedTest
    @ValueSource(ints = {-1, 1, Integer.MAX_VALUE})
    void minusOneAndOneOrMoreAreWithinTheLimits(int rate) {
        assertEquals(Map.of(), violations(rated(rate)));
    }

    @Test
    void anUnsetRateIsWithinTheLimits() {
        assertEquals(Map.of(), violations(new TestSpec()));
    }
}
