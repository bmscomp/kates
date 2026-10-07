package com.bmscomp.kates.api;

import java.util.concurrent.TimeUnit;
import jakarta.enterprise.event.Event;
import jakarta.inject.Inject;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.TestLifecycleEvent;

/**
 * The event stream's observer takes the orchestrator's lifecycle events, which
 * are fired with no request behind them. EventStreamResource is @RolesAllowed
 * for its endpoint, and a class-level role holds every method of the bean to
 * it: the observer was refused, and the stream sent subscribers nothing. A
 * refused observer fails the async delivery, so this needs no subscriber.
 */
@QuarkusTest
class EventStreamObserverTest {

    @Inject
    Event<TestLifecycleEvent> lifecycleEvents;

    @Test
    void aLifecycleEventReachesTheObserverWithNoRequestBehindIt() throws Exception {
        lifecycleEvents
                .fireAsync(new TestLifecycleEvent("observer-test", "LOAD", TestLifecycleEvent.EventKind.DONE, "done"))
                .toCompletableFuture()
                .get(10, TimeUnit.SECONDS);
    }
}
