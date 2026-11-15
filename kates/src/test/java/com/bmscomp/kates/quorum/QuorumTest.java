package com.bmscomp.kates.quorum;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import jakarta.enterprise.event.Event;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Quorum as Kates uses it: which member takes writes, and the recovery it runs first. */
class QuorumTest {

    @TempDir
    Path tmp;

    private final List<Quorum> members = new ArrayList<>();
    /** Each takeover any member fired, and which member fired it. */
    private final List<String> takeovers = new CopyOnWriteArrayList<>();

    @AfterEach
    void stop() {
        members.forEach(member -> member.stop(null));
    }

    @Test
    void onlyTheLeaderTakesWritesAndItRecoversOncePerTerm() throws Exception {
        start(3, new AtomicBoolean(false));

        Quorum leader = soleWriter(members);
        assertEquals(1, takeovers.size(), "one takeover: " + takeovers);
        assertTrue(takeovers.get(0).startsWith(leader.memberId() + "@"), takeovers.toString());
        assertTrue(leader.acceptsWrites());
        assertEquals(1, takeovers.size(), "not again in the same term");

        // A member that has applied nothing is no voter: wait until each has the leader's entries.
        long applied = applied(leader);
        for (Quorum member : members) {
            for (int i = 0; i < 200 && applied(member) < applied; i++) {
                TimeUnit.MILLISECONDS.sleep(100);
            }
        }
        leader.stop(null);
        members.remove(leader);
        Quorum next = soleWriter(members);
        assertEquals(2, takeovers.size(), "the next leader recovers too: " + takeovers);
        assertTrue(takeovers.get(1).startsWith(next.memberId() + "@"), takeovers.toString());
    }

    @Test
    void aLeaderWhoseRecoveryFailsTakesNoWriteUntilItSucceeds() throws Exception {
        AtomicBoolean failing = new AtomicBoolean(true);
        start(3, failing);
        TimeUnit.SECONDS.sleep(4);
        assertTrue(members.stream().noneMatch(Quorum::acceptsWrites), "no member takes writes");

        failing.set(false);
        Quorum leader = soleWriter(members);
        assertTrue(takeovers.get(takeovers.size() - 1).startsWith(leader.memberId() + "@"));
    }

    @Test
    void aClosedMemberIsRestartedBySchedule() throws Exception {
        start(1, new AtomicBoolean(false));
        Quorum quorum = members.get(0);
        soleWriter(members);

        quorum.member().closeServer();
        assertTrue(quorum.notServingSince().isEmpty(), "nothing noticed before the schedule runs");
        quorum.takeOver();

        assertTrue(quorum.notServingSince().isEmpty(), "restarted: " + quorum.health());
        assertEquals("RUNNING", quorum.health().get("state"));
        assertTrue(quorum.member().serving());
        soleWriter(members);
    }

    @Test
    void healthWhileClosedNamesTheState() throws Exception {
        start(1, new AtomicBoolean(false));
        Quorum quorum = members.get(0);
        soleWriter(members);
        int port = Integer.parseInt(quorum.peers.orElseThrow().get(0).replaceAll(".*:", ""));

        quorum.member().closeServer();
        // The port held by someone else: the new server cannot bind, so the member stays down.
        try (java.net.ServerSocket held = new java.net.ServerSocket(port)) {
            quorum.takeOver();

            assertEquals("CLOSED", quorum.health().get("state"));
            assertFalse(quorum.health().containsKey("term"), "no stale term: " + quorum.health());
            assertTrue(quorum.notServingSince().isPresent());
            assertFalse(quorum.acceptsWrites(), "a member that does not serve takes no write");
        }
    }

    @Test
    void noRestartAfterShutdown() throws Exception {
        start(1, new AtomicBoolean(false));
        Quorum quorum = members.get(0);
        QuorumMember before = quorum.member();

        quorum.stop(null);
        quorum.takeOver();

        assertTrue(before == quorum.member(), "no member started after shutdown");
        assertFalse(quorum.member().serving());
    }

    private void start(int count, AtomicBoolean failing) throws Exception {
        TestPki pki = new TestPki(tmp.resolve("pki"));
        Path[][] certificates = QuorumMemberTest.certificates(pki, count);
        List<String> peers = QuorumMemberTest.peers(count);
        for (int i = 0; i < count; i++) {
            Path[] files = certificates[i];
            Quorum quorum = new Quorum();
            quorum.peers = Optional.of(peers);
            quorum.memberId = Optional.of("m" + i);
            quorum.dir = Optional.of(tmp.resolve("data-m" + i).toString());
            quorum.cert = Optional.of(files[0].toString());
            quorum.key = Optional.of(files[1].toString());
            quorum.ca = Optional.of(files[2].toString());
            quorum.takeover = recorder(quorum, failing);
            quorum.start(null);
            members.add(quorum);
        }
    }

    @SuppressWarnings("unchecked")
    private Event<LeadershipAcquired> recorder(Quorum quorum, AtomicBoolean failing) {
        Event<LeadershipAcquired> event = mock(Event.class);
        doAnswer(invocation -> {
                    if (failing.get()) {
                        throw new IllegalStateException("PostgreSQL is down");
                    }
                    takeovers.add(quorum.memberId() + "@"
                            + invocation.<LeadershipAcquired>getArgument(0).term());
                    return null;
                })
                .when(event)
                .fire(any());
        return event;
    }

    /** The one member that takes writes, once there is exactly one; the others say no. */
    private static Quorum soleWriter(List<Quorum> members) throws InterruptedException {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(45);
        while (System.nanoTime() < deadline) {
            List<Quorum> writers =
                    members.stream().filter(Quorum::acceptsWrites).toList();
            if (writers.size() == 1) {
                return writers.get(0);
            }
            assertFalse(writers.size() > 1, "two members took writes: " + views(members));
            TimeUnit.MILLISECONDS.sleep(100);
        }
        throw new AssertionError("no single member took writes within 45 s: " + views(members));
    }

    private static long applied(Quorum member) {
        return ((Number) member.health().get("appliedIndex")).longValue();
    }

    private static String views(List<Quorum> members) {
        return members.stream()
                .map(member -> member.memberId() + member.health())
                .toList()
                .toString();
    }
}
