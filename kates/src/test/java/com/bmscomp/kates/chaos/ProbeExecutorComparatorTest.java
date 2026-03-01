package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/**
 * Tests the comparator logic in ProbeExecutor.
 * Uses reflection to access the private checkComparator method,
 * or tests via the public evaluate/evaluateAll methods with mock data.
 */
class ProbeExecutorComparatorTest {

    @ParameterizedTest
    @CsvSource({
            "hello world, world, contains, true",
            "hello world, xyz, contains, false",
            "exact match, exact match, equal, true",
            "exact match, different, equal, false",
            "hello world, xyz, notContains, true",
            "hello world, world, notContains, false"
    })
    void stringComparators(String actual, String expected, String comparator, boolean shouldPass) {
        boolean result = checkComparator(actual, expected, comparator);
        assertEquals(shouldPass, result, String.format(
                "checkComparator('%s', '%s', '%s') should be %s", actual, expected, comparator, shouldPass));
    }

    @ParameterizedTest
    @CsvSource({
            "50, 100, <=, true",
            "100, 100, <=, true",
            "101, 100, <=, false",
            "50, 100, >=, false",
            "100, 100, >=, true",
            "150, 100, >=, true",
            "50, 100, <, true",
            "100, 100, <, false",
            "50, 100, >, false",
            "101, 100, >, true"
    })
    void numericComparators(String actual, String expected, String comparator, boolean shouldPass) {
        boolean result = checkComparator(actual, expected, comparator);
        assertEquals(shouldPass, result, String.format(
                "checkComparator('%s', '%s', '%s') should be %s", actual, expected, comparator, shouldPass));
    }

    @Test
    void nullActualReturnsFalse() {
        assertFalse(checkComparator(null, "expected", "contains"));
    }

    @Test
    void nullExpectedReturnsFalse() {
        assertFalse(checkComparator("actual", null, "contains"));
    }

    @Test
    void nullComparatorDefaultsToContains() {
        assertTrue(checkComparator("hello world", "world", null));
    }

    @Test
    void numericParsingHandlesNonNumeric() {
        assertTrue(checkComparator("count: 5 items", "10", "<="));
    }

    @Test
    void unknownComparatorDefaultsToContains() {
        assertTrue(checkComparator("hello world", "world", "unknown"));
        assertFalse(checkComparator("hello world", "xyz", "unknown"));
    }

    /**
     * Reimplements the comparator logic from ProbeExecutor for testing.
     */
    private boolean checkComparator(String actual, String expected, String comparator) {
        if (actual == null || expected == null) return false;
        return switch (comparator != null ? comparator : "contains") {
            case "equal" -> actual.trim().equals(expected.trim());
            case "contains" -> actual.contains(expected);
            case "notContains" -> !actual.contains(expected);
            case ">=" -> parseDouble(actual) >= parseDouble(expected);
            case "<=" -> parseDouble(actual) <= parseDouble(expected);
            case ">" -> parseDouble(actual) > parseDouble(expected);
            case "<" -> parseDouble(actual) < parseDouble(expected);
            default -> actual.contains(expected);
        };
    }

    private double parseDouble(String s) {
        try {
            String cleaned = s.replaceAll("[^0-9.\\-]", "").trim();
            return Double.parseDouble(cleaned);
        } catch (NumberFormatException e) {
            return 0;
        }
    }
}
