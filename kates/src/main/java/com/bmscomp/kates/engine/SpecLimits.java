package com.bmscomp.kates.engine;

import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import com.bmscomp.kates.domain.ScenarioPhase;
import com.bmscomp.kates.domain.TestScenario;
import com.bmscomp.kates.domain.TestSpec;

/**
 * The limits TestSpec's constraints set on a spec's values: a numRecords of at
 * least 1, a topic Kafka can create, an acks Kafka knows.
 *
 * <p>Bean validation holds a request's own spec to them on POST /api/tests and
 * POST /api/schedules, and stops there. A cascade into the scenario would key
 * each value by its field name alone, as ConstraintViolationExceptionMapper
 * keys every violation, so a phase's value would not say which phase it is in.
 * TestOrchestrator.refusal holds a scenario's specs to them instead, keyed by
 * their path in the scenario. ResilienceResource holds the request's own spec
 * to them too, since POST /api/resilience runs no bean validation.
 */
@ApplicationScoped
public class SpecLimits {

    private final Validator validator;

    @Inject
    public SpecLimits(Validator validator) {
        this.validator = validator;
    }

    /**
     * Each value of the spec outside its limits, keyed by field name, with the
     * reason; empty when all are within them, or there is no spec. Sorted, so
     * that a message naming them names them in the same order each time.
     */
    public Map<String, String> violations(TestSpec spec) {
        Map<String, String> found = new LinkedHashMap<>();
        if (spec == null) {
            return found;
        }
        validator.validate(spec).stream()
                .sorted(Comparator.comparing(v -> v.getPropertyPath() + ": " + v.getMessage()))
                .forEach(v -> found.putIfAbsent(v.getPropertyPath().toString(), v.getMessage()));
        return found;
    }

    /**
     * Each value of the scenario's base spec and phase specs outside its
     * limits, keyed by its path in the scenario ({@code baseSpec.x},
     * {@code phases[i].spec.x}): the base spec's first, then each phase's in
     * order. Empty when all are within them, or there is no scenario.
     */
    public Map<String, String> violations(TestScenario scenario) {
        Map<String, String> found = new LinkedHashMap<>();
        if (scenario == null) {
            return found;
        }
        violations(scenario.getBaseSpec()).forEach((field, why) -> found.put("baseSpec." + field, why));
        List<ScenarioPhase> phases = scenario.getPhases() != null ? scenario.getPhases() : List.of();
        for (int i = 0; i < phases.size(); i++) {
            ScenarioPhase phase = phases.get(i);
            // A null phase has no spec to check.
            if (phase != null) {
                String path = "phases[" + i + "].spec.";
                violations(phase.getSpec()).forEach((field, why) -> found.put(path + field, why));
            }
        }
        return found;
    }
}
