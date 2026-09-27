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
 * <p>{@code auto.offset.reset=earliest} because the webhook consumer joins a
 * group of its own on every start, and with the default {@code latest} an event
 * published before the group's first assignment is skipped. The test publishes
 * as soon as the topic exists, which can be before that assignment.
 */
public class EventBusTestProfile extends NoSchedulersTestProfile {

    static final String TOPIC = "kates-test-events";

    @Override
    public Map<String, String> getConfigOverrides() {
        Map<String, String> overrides = new HashMap<>(super.getConfigOverrides());
        overrides.put("mp.messaging.outgoing.test-events-out.connector", "smallrye-kafka");
        overrides.put("mp.messaging.outgoing.test-events-out.topic", TOPIC);
        overrides.put("mp.messaging.incoming.test-events-in.connector", "smallrye-kafka");
        overrides.put("mp.messaging.incoming.test-events-in.topic", TOPIC);
        overrides.put("mp.messaging.incoming.test-events-in.auto.offset.reset", "earliest");
        overrides.put("kates.webhooks.allow-loopback", "true");
        return overrides;
    }
}
