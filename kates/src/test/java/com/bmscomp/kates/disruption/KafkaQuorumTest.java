package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.OptionalLong;
import java.util.Set;

import org.apache.kafka.clients.admin.QuorumInfo;
import org.junit.jupiter.api.Test;

class KafkaQuorumTest {

    private static QuorumInfo.ReplicaState voter(int id, OptionalLong caughtUpAt) {
        QuorumInfo.ReplicaState state = mock(QuorumInfo.ReplicaState.class);
        when(state.replicaId()).thenReturn(id);
        when(state.lastCaughtUpTimestamp()).thenReturn(caughtUpAt);
        return state;
    }

    private static QuorumInfo quorum(int leader, QuorumInfo.ReplicaState... voters) {
        QuorumInfo info = mock(QuorumInfo.class);
        when(info.leaderId()).thenReturn(leader);
        when(info.voters()).thenReturn(List.of(voters));
        return info;
    }

    @Test
    void aVoterBehindTheLeaderOrNeverCaughtUpIsNotCounted() {
        var read = KafkaIntelligenceService.quorumOf(quorum(
                        3,
                        voter(3, OptionalLong.of(10_000)),
                        voter(4, OptionalLong.of(9_000)),
                        voter(5, OptionalLong.of(8_000)),
                        voter(6, OptionalLong.empty())))
                .orElseThrow();

        assertEquals(Set.of(3, 4, 5, 6), read.voters());
        assertEquals(Set.of(3, 4), read.caughtUp());
    }

    @Test
    void aQuorumWithoutALeaderIsNotRead() {
        assertTrue(KafkaIntelligenceService.quorumOf(quorum(-1, voter(3, OptionalLong.of(10_000))))
                .isEmpty());
    }
}
