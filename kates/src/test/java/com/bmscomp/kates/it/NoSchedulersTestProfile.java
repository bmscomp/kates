package com.bmscomp.kates.it;

import java.util.HashMap;
import java.util.Map;

/**
 * Integration profile with the background jobs switched OFF.
 *
 * <p>Any test that seeds rows and then asserts on them is racing the two
 * schedulers that mutate those rows: the outbox poller (every 2s) publishes and
 * deletes outbox rows, and the engine reconciler moves runs between states. A
 * test that asserts an exact row count or an exact status has to own the data
 * for the duration of the assertion, so it turns both off and drives them
 * explicitly where it cares.
 *
 * <p>The other scheduled jobs are left running deliberately — the timeout
 * reaper only touches non-terminal runs, and nothing else writes to the tables
 * these tests read.
 *
 * <p>Tests that are specifically about the schedulers doing their job — the
 * reconciler driving a run to DONE, for instance — must NOT use this profile.
 */
public class NoSchedulersTestProfile extends IntegrationTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        Map<String, String> overrides = new HashMap<>(super.getConfigOverrides());
        overrides.put("kates.outbox.poll-interval", "off");
        overrides.put("kates.engine.reconcile-interval", "off");
        return overrides;
    }
}
