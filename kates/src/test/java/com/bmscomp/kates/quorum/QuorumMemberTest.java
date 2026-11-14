package com.bmscomp.kates.quorum;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.FileInputStream;
import java.io.IOException;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.Key;
import java.security.KeyStore;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Base64;
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
        Pki pki = new Pki(tmp.resolve("pki"));
        List<String> peers = peers(3);
        for (int i = 0; i < 3; i++) {
            started.add(QuorumMember.start(settings(peers, i, pki.member("m" + i))));
        }

        QuorumMember first = await(() -> soleLeader(started));
        long term = first.term();
        for (QuorumMember member : started) {
            await(() -> first.memberId().equals(member.leaderId()) ? member : null);
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
        Pki pki = new Pki(tmp.resolve("pki"));
        Pki other = new Pki(tmp.resolve("other"));
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

    private static List<String> peers(int count) throws IOException {
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

    /** A CA, and member certificates it signs, made with the JDK's keytool. */
    private static final class Pki {
        private static final String PASS = "changeit";
        private final Path dir;

        Pki(Path dir) throws Exception {
            this.dir = Files.createDirectories(dir);
            keytool(
                    "-genkeypair",
                    "-alias",
                    "ca",
                    "-keyalg",
                    "EC",
                    "-groupname",
                    "secp256r1",
                    "-dname",
                    "CN=ca",
                    "-ext",
                    "bc:c",
                    "-validity",
                    "2",
                    "-keystore",
                    "ca.p12");
            keytool("-exportcert", "-rfc", "-alias", "ca", "-keystore", "ca.p12", "-file", "ca.pem");
        }

        /** The member's certificate, key and the CA, as the three PEM files a member reads. */
        Path[] member(String name) throws Exception {
            String store = name + ".p12";
            keytool(
                    "-genkeypair",
                    "-alias",
                    name,
                    "-keyalg",
                    "EC",
                    "-groupname",
                    "secp256r1",
                    "-dname",
                    "CN=" + name,
                    "-validity",
                    "2",
                    "-keystore",
                    store);
            keytool("-certreq", "-alias", name, "-keystore", store, "-file", name + ".csr");
            keytool(
                    "-gencert",
                    "-alias",
                    "ca",
                    "-keystore",
                    "ca.p12",
                    "-infile",
                    name + ".csr",
                    "-outfile",
                    name + ".pem",
                    "-rfc",
                    "-validity",
                    "2",
                    "-ext",
                    "san=dns:localhost,ip:127.0.0.1",
                    "-ext",
                    "eku=serverAuth,clientAuth");
            KeyStore keys = KeyStore.getInstance("PKCS12");
            try (FileInputStream in = new FileInputStream(dir.resolve(store).toFile())) {
                keys.load(in, PASS.toCharArray());
            }
            Key key = keys.getKey(name, PASS.toCharArray());
            Path pem = dir.resolve(name + ".key");
            Files.writeString(
                    pem,
                    "-----BEGIN PRIVATE KEY-----\n"
                            + Base64.getMimeEncoder(64, "\n".getBytes()).encodeToString(key.getEncoded())
                            + "\n-----END PRIVATE KEY-----\n");
            return new Path[] {dir.resolve(name + ".pem"), pem, dir.resolve("ca.pem")};
        }

        private void keytool(String... args) throws Exception {
            List<String> command = new ArrayList<>();
            command.add(
                    Path.of(System.getProperty("java.home"), "bin", "keytool").toString());
            command.addAll(List.of(args));
            command.addAll(List.of("-storepass", PASS, "-keypass", PASS, "-storetype", "PKCS12", "-noprompt"));
            Process process = new ProcessBuilder(command)
                    .directory(dir.toFile())
                    .redirectErrorStream(true)
                    .start();
            String output = new String(process.getInputStream().readAllBytes());
            if (process.waitFor() != 0) {
                throw new IllegalStateException(String.join(" ", args) + ": " + output);
            }
        }
    }
}
