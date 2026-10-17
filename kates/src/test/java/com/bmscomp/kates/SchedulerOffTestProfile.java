package com.bmscomp.kates;

import java.util.Map;

import io.quarkus.test.junit.QuarkusTestProfile;

/**
 * Turns every {@code @Scheduled} job off, for a test that stubs an
 * {@code @InjectMock} bean a job also calls.
 *
 * <p>Mockito ties {@code when(mock.call()).thenReturn(...)} to the last call
 * made on that mock, from any thread. A job that calls the mock while the
 * test stubs it, as the timeout reaper's {@code findByStatus} did, takes the
 * stub, and the test fails with {@code WrongTypeOfReturnValue}.
 */
public class SchedulerOffTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of("quarkus.scheduler.enabled", "false");
    }
}
