package com.bmscomp.kates.quorum;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class QuorumMemberTest {

    private static final Duration ELECTION = Duration.ofSeconds(20);

    @TempDir
    Path tmp;

    private final List<QuorumMember> started = new ArrayList<>();

    @AfterEach
    void closeMembers() throws IOException {
        for (QuorumMember member : started) {
            member.close();
        }
    }

    @Test
    void settingsRefuseWhatIsMissingOrWrong() throws IOException {
        Files.writeString(tmp.resolve("f"), "x");
        String f = tmp.resolve("f").toString();
        List<String> peers = List.of("a=127.0.0.1:1", "b=127.0.0.1:2");

        assertRefused("is not id=host:port", List.of("a"), "a", "d", f, f, f);
        assertRefused("is not a port", List.of("a=h:70000"), "a", "d", f, f, f);
        assertRefused("listed twice", List.of("a=h:1", "a=h:2"), "a", "d", f, f, f);
        assertRefused("kates.quorum.member-id", peers, "c", "d", f, f, f);
        assertRefused("kates.quorum.member-id", peers, null, "d", f, f, f);
        assertRefused("kates.quorum.dir is required", peers, "a", "", f, f, f);
        assertRefused("kates.quorum.tls.cert is required", peers, "a", "d", null, f, f);
        assertRefused(
                "kates.quorum.tls.ca: ",
                peers,
                "a",
                "d",
                f,
                f,
                tmp.resolve("none").toString());

        QuorumMember.Settings settings = QuorumMember.Settings.parse(peers, "b", "d", f, f, f);
        assertEquals("127.0.0.1:2", settings.self().getAddress());
    }

    @Test
    void threeMembersElectOneLeaderAndAnotherWhenItGoes() throws Exception {
        TestPki pki = new TestPki(tmp.resolve("pki"));
        List<String> peers = peers(3);
        for (int i = 0; i < 3; i++) {
            started.add(QuorumMember.start(settings(peers, i, pki.member("m" + i))));
        }

        QuorumMember first = await(() -> soleLeader(started));
        long term = first.term();
        for (QuorumMember member : started) {
            await(() -> first.memberId().equals(member.leaderId()) ? member : null);
        }
        assertTrue(first.confirmLeadership(), "the leader confirms it leads");
        for (QuorumMember member : started) {
            if (member != first) {
                assertFalse(member.confirmLeadership(), "a follower does not");
            }
        }

        first.close();
        List<QuorumMember> rest = new ArrayList<>(started);
        rest.remove(first);
        started.remove(first);
        QuorumMember second = await(() -> soleLeader(rest));
        assertTrue(second.term() > term, "a new leader has a higher term");
    }

    @Test
    void aMemberWithAnotherCasCertificateCannotJoin() throws Exception {
        TestPki pki = new TestPki(tmp.resolve("pki"));
        TestPki other = new TestPki(tmp.resolve("other"));
        List<String> peers = peers(3);
        started.add(QuorumMember.start(settings(peers, 0, pki.member("m0"))));
        started.add(QuorumMember.start(settings(peers, 1, pki.member("m1"))));
        // Its own CA trusts its own certificate only: it can neither reach nor be reached.
        QuorumMember stranger = QuorumMember.start(settings(peers, 2, other.member("m2")));
        started.add(stranger);

        QuorumMember leader = await(() -> soleLeader(started.subList(0, 2)));
        TimeUnit.SECONDS.sleep(5);
        assertFalse(stranger.isLeader());
        assertNull(stranger.leaderId(), "the stranger learns of no leader");
        assertTrue(leader.isLeader(), "the two trusted members keep their leader");
    }

    @Test
    void aRestartedMemberKeepsItsTermAndRejoins() throws Exception {
        TestPki pki = new TestPki(tmp.resolve("pki"));
        List<String> peers = peers(3);
        List<QuorumMember.Settings> settings = new ArrayList<>();
        for (int i = 0; i < 3; i++) {
            settings.add(settings(peers, i, pki.member("m" + i)));
            started.add(QuorumMember.start(settings.get(i)));
        }
        QuorumMember leader = await(() -> soleLeader(started));
        long term = leader.term();
        for (QuorumMember member : started) {
            member.close();
        }
        started.clear();

        // Alone, nothing can tell it the term: it reads it, and its vote, back from its dir.
        QuorumMember alone = QuorumMember.start(settings.get(0));
        started.add(alone);
        assertTrue(alone.term() >= term, "term " + alone.term() + " read back, was " + term);

        started.add(QuorumMember.start(settings.get(1)));
        QuorumMember next = await(() -> soleLeader(started));
        assertTrue(next.term() > term, "the members elect again in a later term");
        await(() -> next.memberId().equals(alone.leaderId()) ? alone : null);
    }

    @Test
    void aLeaderThatLosesItsMajorityStopsLeading() throws Exception {
        TestPki pki = new TestPki(tmp.resolve("pki"));
        List<String> peers = peers(3);
        for (int i = 0; i < 3; i++) {
            started.add(QuorumMember.start(settings(peers, i, pki.member("m" + i))));
        }
        QuorumMember leader = await(() -> soleLeader(started));
        for (QuorumMember member : new ArrayList<>(started)) {
            if (member != leader) {
                member.close();
                started.remove(member);
            }
        }

        // Its lease runs out before it steps down: from then on it no longer confirms.
        long cut = System.nanoTime();
        await(() -> leader.confirmLeadership() ? null : leader);
        assertTrue(
                Duration.ofNanos(System.nanoTime() - cut).compareTo(Duration.ofSeconds(3)) < 0,
                "a cut-off leader stops confirming within its lease and the read timeout");
        await(() -> leader.isLeader() ? null : leader);
        TimeUnit.SECONDS.sleep(3);
        assertFalse(leader.isLeader(), "alone, it does not lead again");
    }

    private static void assertRefused(
            String message, List<String> peers, String id, String dir, String cert, String key, String ca) {
        IllegalArgumentException refused = assertThrows(
                IllegalArgumentException.class, () -> QuorumMember.Settings.parse(peers, id, dir, cert, key, ca));
        assertTrue(refused.getMessage().contains(message), refused.getMessage());
    }

    private static QuorumMember soleLeader(List<QuorumMember> members) {
        List<QuorumMember> leaders =
                members.stream().filter(QuorumMember::isLeader).toList();
        return leaders.size() == 1 ? leaders.get(0) : null;
    }

    private static <T> T await(Supplier<T> found) throws InterruptedException {
        long deadline = System.nanoTime() + ELECTION.toNanos();
        while (System.nanoTime() < deadline) {
            T value = found.get();
            if (value != null) {
                return value;
            }
            TimeUnit.MILLISECONDS.sleep(100);
        }
        throw new AssertionError("not within " + ELECTION);
    }

    static List<String> peers(int count) throws IOException {
        List<String> peers = new ArrayList<>();
        for (int i = 0; i < count; i++) {
            try (ServerSocket socket = new ServerSocket(0)) {
                peers.add("m" + i + "=127.0.0.1:" + socket.getLocalPort());
            }
        }
        return peers;
    }

    private QuorumMember.Settings settings(List<String> peers, int index, Path[] files) {
        return QuorumMember.Settings.parse(
                peers,
                "m" + index,
                tmp.resolve("data-m" + index).toString(),
                files[0].toString(),
                files[1].toString(),
                files[2].toString());
    }
}
