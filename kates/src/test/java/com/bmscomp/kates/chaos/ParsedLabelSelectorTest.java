package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class ParsedLabelSelectorTest {

    /** Labels the kafka-cluster chart puts on a pod of a pool pinned with `zone: alpha`. */
    private static final Map<String, String> ALPHA_BROKER = Map.of(
            "strimzi.io/cluster", "krafter",
            "strimzi.io/component-type", "kafka",
            "strimzi.io/pool-name", "brokers-alpha",
            "zone", "alpha");

    private static final Map<String, String> SIGMA_BROKER = Map.of(
            "strimzi.io/cluster", "krafter",
            "strimzi.io/component-type", "kafka",
            "strimzi.io/pool-name", "brokers-sigma",
            "zone", "sigma");

    @Test
    void multiLabelSelectorIsEveryRequirement() {
        ParsedLabelSelector s = ParsedLabelSelector.parse("strimzi.io/component-type=kafka,zone=alpha");

        assertEquals(2, s.requirements().size());
        assertTrue(s.matches(ALPHA_BROKER));
        assertFalse(s.matches(SIGMA_BROKER));
        assertFalse(s.matches(Map.of("strimzi.io/component-type", "kafka")), "a pod with no zone label");
    }

    @Test
    void oldAzFailureSelectorMatchesNoChartPod() {
        // Parsed for what it is — two requirements, not one key with a value of
        // "kafka,topology.kubernetes.io/zone=zone-a" — and still matching no pod:
        // node topology labels never reach pods.
        ParsedLabelSelector s =
                ParsedLabelSelector.parse("strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a");

        assertEquals(2, s.requirements().size());
        assertEquals("topology.kubernetes.io/zone", s.requirements().get(1).key());
        assertFalse(s.matches(ALPHA_BROKER));
        assertFalse(s.matches(SIGMA_BROKER));
    }

    @Test
    void setBasedRequirements() {
        assertTrue(ParsedLabelSelector.parse("zone in (alpha,sigma)").matches(SIGMA_BROKER));
        assertFalse(ParsedLabelSelector.parse("zone in (gamma)").matches(SIGMA_BROKER));
        assertTrue(ParsedLabelSelector.parse("zone notin (gamma)").matches(SIGMA_BROKER));
        assertFalse(ParsedLabelSelector.parse("zone notin (alpha,sigma)").matches(SIGMA_BROKER));
        assertTrue(ParsedLabelSelector.parse("zone").matches(ALPHA_BROKER));
        assertFalse(ParsedLabelSelector.parse("!zone").matches(ALPHA_BROKER));
    }

    @Test
    void exclusionsMatchPodsWithoutTheKey() {
        // Kubernetes semantics: `!=` and `notin` select objects lacking the key.
        Map<String, String> unzoned = Map.of("strimzi.io/component-type", "kafka");

        assertTrue(ParsedLabelSelector.parse("zone!=alpha").matches(unzoned));
        assertTrue(ParsedLabelSelector.parse("zone notin (alpha)").matches(unzoned));
        assertTrue(ParsedLabelSelector.parse("!zone").matches(unzoned));
        assertFalse(ParsedLabelSelector.parse("zone in (alpha)").matches(unzoned));
        assertTrue(ParsedLabelSelector.parse("!zone").matches(null), "null labels are no labels");
    }

    @Test
    void canonicalFormIsWhatTheApiServerGets() {
        assertEquals(
                "app=kafka,tier in (a,b),zone notin (c),!legacy,managed,x!=y,k=v",
                ParsedLabelSelector.parse(
                                " app = kafka , tier in ( a , b ),zone notin(c), ! legacy,managed,x != y,k==v")
                        .toString());
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "strimzi.io/component-type=kafka",
                "strimzi.io/cluster=krafter,zone=alpha",
                "zone in (alpha,sigma),!legacy",
                "app,tier notin (cache),env!=prod",
                "empty="
            })
    void canonicalFormRoundTrips(String selector) {
        ParsedLabelSelector once = ParsedLabelSelector.parse(selector);
        assertEquals(once, ParsedLabelSelector.parse(once.toString()));
    }

    @Test
    void emptyValueIsAValue() {
        ParsedLabelSelector s = ParsedLabelSelector.parse("tier=");
        assertTrue(s.matches(Map.of("tier", "")));
        assertFalse(s.matches(Map.of()));
    }

    @Test
    void blankSelectorIsRejectedNotMatchAll() {
        assertThrows(IllegalArgumentException.class, () -> ParsedLabelSelector.parse(null));
        assertThrows(IllegalArgumentException.class, () -> ParsedLabelSelector.parse(""));
        assertThrows(IllegalArgumentException.class, () -> ParsedLabelSelector.parse("   "));
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "zone=alpha,", // trailing comma
                "a=b,,c=d", // empty requirement
                "=alpha", // no key
                "zone alpha", // no operator
                "zone in ()", // empty set
                "zone in (alpha", // unclosed set
                "zone in alpha", // set without parentheses
                "zone=zone a", // space inside a value
                "replicas>1", // gt/lt: node selectors only
                "zone=alpha@1", // invalid value character
                "-zone=alpha", // key must start alphanumeric
                "Strimzi.IO/kind=Kafka", // prefix is a lowercase DNS subdomain
                "a/b/c=d" // one slash at most
            })
    void malformedSelectorsAreRejectedWithTheInput(String selector) {
        IllegalArgumentException e =
                assertThrows(IllegalArgumentException.class, () -> ParsedLabelSelector.parse(selector));
        assertTrue(e.getMessage().contains(selector), e.getMessage());
    }
}
