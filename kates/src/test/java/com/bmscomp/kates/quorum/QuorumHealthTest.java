package com.bmscomp.kates.quorum;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.is;

import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

/** Kates as configured, a quorum of one: the settings reach the member, and health reports it. */
@QuarkusTest
@TestProfile(QuorumHealthTest.OneMember.class)
class QuorumHealthTest {

    public static class OneMember implements QuarkusTestProfile {
        // A fresh CA, member and port per run; the directory is left in java.io.tmpdir.
        @Override
        public Map<String, String> getConfigOverrides() {
            try {
                Path dir = Files.createTempDirectory("kates-quorum-test");
                Path[] files = new TestPki(dir.resolve("pki")).member("k0");
                int port;
                try (ServerSocket socket = new ServerSocket(0)) {
                    port = socket.getLocalPort();
                }
                return Map.of(
                        "kates.quorum.peers", "k0=127.0.0.1:" + port,
                        "kates.quorum.member-id", "k0",
                        "kates.quorum.dir", dir.resolve("data").toString(),
                        "kates.quorum.tls.cert", files[0].toString(),
                        "kates.quorum.tls.key", files[1].toString(),
                        "kates.quorum.tls.ca", files[2].toString());
            } catch (Exception e) {
                throw new IllegalStateException(e);
            }
        }
    }

    @Inject
    Quorum quorum;

    @Test
    void healthReportsTheMemberOnceItLeads() throws InterruptedException {
        for (int i = 0; i < 200 && !quorum.isLeader(); i++) {
            TimeUnit.MILLISECONDS.sleep(100);
        }

        given().when()
                .get("/api/health")
                .then()
                .statusCode(200)
                .body("quorum.enabled", is(true))
                .body("quorum.memberId", is("k0"))
                .body("quorum.leader", is(true))
                .body("quorum.leaderId", is("k0"))
                .body("quorum.term", greaterThanOrEqualTo(1))
                .body("quorum.appliedIndex", greaterThanOrEqualTo(0));
    }
}
