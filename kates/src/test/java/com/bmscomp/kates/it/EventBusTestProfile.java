package com.bmscomp.kates.it;

import java.util.HashMap;
import java.util.Map;

/**
 * Integration profile with the event-bus channels on the real Kafka connector.
 *
 * <p>Every other test routes {@code test-events-out} and {@code test-events-in}
 * to the in-memory connector, which connects nothing to anything: an event sent
 * by the outbox never reaches the webhook consumer. This profile puts both
 * channels back on Kafka, on the topic production uses.
 *
 * <p>The outbox poller stays off, as in {@link NoSchedulersTestProfile}, so the
 * test publishes when it chooses to.
 *
 * <p>The consumer's group and offset reset are production's, from {@code
 * application.properties}: the tests are about that consumer. This profile used
 * to force {@code auto.offset.reset=earliest}, because the consumer then joined
 * a new group at every start and, with {@code latest}, skipped an event
 * published before the group's first assignment.
 */
public class EventBusTestProfile extends NoSchedulersTestProfile {

    static final String TOPIC = "kates-test-events";

    /** The webhook consumer's group, set in {@code application.properties} and not overridden here. */
    static final String GROUP = "kates-webhooks";

    @Override
    public Map<String, String> getConfigOverrides() {
        Map<String, String> overrides = new HashMap<>(super.getConfigOverrides());
        overrides.put("mp.messaging.outgoing.test-events-out.connector", "smallrye-kafka");
        overrides.put("mp.messaging.outgoing.test-events-out.topic", TOPIC);
        overrides.put("mp.messaging.incoming.test-events-in.connector", "smallrye-kafka");
        overrides.put("mp.messaging.incoming.test-events-in.topic", TOPIC);
        overrides.put("kates.webhooks.allow-loopback", "true");
        return overrides;
    }
}
