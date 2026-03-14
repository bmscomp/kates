package com.bmscomp.kates.config;

import io.quarkus.runtime.annotations.RegisterForReflection;

/**
 * Registers all domain classes for GraalVM native image reflection.
 * Without this, Jackson cannot serialize domain objects in native mode
 * because the getter methods are not discoverable via reflection.
 */
@RegisterForReflection(targets = {
    com.bmscomp.kates.domain.TestRun.class,
    com.bmscomp.kates.domain.TestResult.class,
    com.bmscomp.kates.domain.TestSpec.class,
    com.bmscomp.kates.domain.TestType.class,
    com.bmscomp.kates.domain.SlaDefinition.class,
    com.bmscomp.kates.domain.SlaVerdict.class,
    com.bmscomp.kates.domain.SlaViolation.class,
    com.bmscomp.kates.domain.CreateTestRequest.class,
    com.bmscomp.kates.domain.TestScenario.class,
    com.bmscomp.kates.domain.ScenarioPhase.class,
    com.bmscomp.kates.domain.MetricsSample.class,
    com.bmscomp.kates.domain.IntegrityResult.class,
    com.bmscomp.kates.domain.IntegrityEvent.class,
    com.bmscomp.kates.domain.LostRange.class,
    com.bmscomp.kates.domain.BaselineResponse.class,
    com.bmscomp.kates.domain.SetBaselineRequest.class,
    com.bmscomp.kates.domain.BulkCreateResponse.class,
    com.bmscomp.kates.domain.BulkDeleteRequest.class,
    com.bmscomp.kates.domain.BulkDeleteResponse.class
})
public class NativeReflectionConfig {
}
