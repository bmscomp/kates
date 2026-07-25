package com.bmscomp.kates.it;

import java.util.HashMap;
import java.util.Map;

/**
 * Integration profile with the scheduled outbox poller switched OFF.
 *
 * <p>Row-count assertions ("one event per state change") are only meaningful if
 * nothing drains the table underneath them — the poller runs every two seconds
 * and would publish and delete rows mid-assertion. The tests drive
 * {@code processOutbox()} explicitly instead, which is also what makes the
 * publish→ack→delete ordering observable.
 */
public class OutboxTestProfile extends IntegrationTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        Map<String, String> overrides = new HashMap<>(super.getConfigOverrides());
        overrides.put("kates.outbox.poll-interval", "off");
        // Keep the reaper and reconciler away from these runs too: this test
        // asserts exact persisted state, not lifecycle behaviour.
        overrides.put("kates.engine.reconcile-interval", "off");
        return overrides;
    }
}
