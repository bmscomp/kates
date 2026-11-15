package com.bmscomp.kates.service;

import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verifyNoInteractions;

import jakarta.persistence.EntityManager;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.quorum.Quorum;

class OutboxPollerQuorumTest {

    @Test
    void aMemberThatDoesNotLeadPublishesNothing() {
        OutboxPoller poller = new OutboxPoller();
        poller.em = mock(EntityManager.class);
        poller.quorum = mock(Quorum.class); // acceptsWrites() answers false

        poller.processOutbox();

        verifyNoInteractions(poller.em);
    }
}
