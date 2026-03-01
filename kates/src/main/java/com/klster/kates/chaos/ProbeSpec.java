package com.klster.kates.chaos;

/**
 * Generic descriptor for an assertion/probe to be evaluated during chaos.
 * Maps to backend-specific probes (like Litmus cmdProbe or k8sProbe).
 */
public record ProbeSpec(String name, String type, String command, String expectedOutput) {}
