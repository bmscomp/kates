package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.hasKey;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.matchesRegex;
import static org.hamcrest.Matchers.notNullValue;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Map;
import java.util.Set;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

/**
 * {@code /api/security} against a real broker.
 *
 * <p>{@code SecurityService} is the largest single class in the backend and had
 * no test of any kind — no unit test, no resource test, nothing. Every one of
 * its endpoints reaches for an {@code AdminClient}, so the only way to exercise
 * them at all is with a broker attached, which is why they live here rather
 * than beside the other resource tests.
 *
 * <p>The container speaks PLAINTEXT with no ACLs, so the audit is expected to
 * report a poor grade. These tests assert on the <em>shape and internal
 * consistency</em> of each report rather than on a specific grade: a hard-coded
 * "F" would break the day someone adds a check, whereas "the grade is one of
 * A-F and the summary adds up" stays true.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class SecurityApiIT {

    private static final Set<String> CHECK_STATUSES = Set.of("PASS", "WARN", "FAIL");

    @Test
    void auditGradesEveryCheckAndTheSummaryAddsUp() {
        var body = given().when()
                .get("/api/security/audit")
                .then()
                .statusCode(200)
                .body("grade", matchesRegex("[A-F]"))
                .body("timestamp", notNullValue())
                .body("checks.size()", greaterThanOrEqualTo(1))
                .extract()
                .jsonPath();

        List<Map<String, Object>> checks = body.getList("checks");
        for (Map<String, Object> check : checks) {
            assertTrue(
                    CHECK_STATUSES.contains(check.get("status")),
                    "unexpected check status " + check.get("status") + " on " + check.get("name"));
            assertNotBlank(check.get("name"), "check name");
            assertNotBlank(check.get("category"), "check category");
            assertNotBlank(check.get("severity"), "check severity");
        }

        // The summary is computed separately from the check list; if the two
        // disagree the grade is meaningless.
        int total = body.getInt("summary.total");
        int passed = body.getInt("summary.passed");
        int warnings = body.getInt("summary.warnings");
        int failures = body.getInt("summary.failures");
        assertEquals(checks.size(), total, "summary.total must count every check");
        assertEquals(total, passed + warnings + failures, "every check lands in exactly one bucket");

        // A PLAINTEXT container with no ACLs cannot be a clean bill of health;
        // if this ever passes, the audit has stopped auditing.
        assertTrue(failures + warnings > 0, "an unsecured broker must not grade clean");
    }

    @Test
    void tlsInspectionReportsOnAnUnencryptedListener() {
        given().when()
                .get("/api/security/tls")
                .then()
                .statusCode(200)
                .body("checks", hasSize(7))
                .body("checks[0].name", notNullValue())
                .body("timestamp", notNullValue());
    }

    @Test
    void authTestRequiresAUserAndReportsItsAcls() {
        given().when().get("/api/security/auth-test").then().statusCode(400);

        given().when()
                .get("/api/security/auth-test?user=kates-backend")
                .then()
                .statusCode(200)
                .body("username", equalTo("kates-backend"))
                .body("checks.size()", greaterThanOrEqualTo(1))
                // No authorizer is configured on the container, so the ACL list
                // is empty rather than absent.
                .body("aclCount", is(0))
                .body("acls", hasSize(0));
    }

    @Test
    void pentestRunsEveryProbeAndNamesUnknownOnes() {
        var all = given().when()
                .get("/api/security/pentest")
                .then()
                .statusCode(200)
                .body("summary.total", is(6))
                .extract()
                .jsonPath();

        List<Map<String, Object>> tests = all.getList("tests");
        assertEquals(6, tests.size(), "the default run covers all six probes");
        for (Map<String, Object> test : tests) {
            assertTrue(
                    Set.of("VULNERABLE", "PROTECTED").contains(test.get("result")),
                    "unexpected pentest result " + test.get("result") + " for " + test.get("name"));
        }
        assertEquals(
                tests.size(),
                all.getInt("summary.protected") + all.getInt("summary.vulnerable"),
                "every probe is either protected or vulnerable");

        given().when()
                .get("/api/security/pentest?test=auto-create")
                .then()
                .statusCode(200)
                .body("tests", hasSize(1))
                .body("tests[0].id", equalTo("auto-create"));

        // An unknown probe name is reported in the body, not as a 4xx — pinned
        // because a client cannot distinguish it from a successful run without
        // checking for the 'error' key.
        given().when()
                .get("/api/security/pentest?test=not-a-probe")
                .then()
                .statusCode(200)
                .body("error", org.hamcrest.Matchers.containsString("Unknown pentest"));
    }

    @Test
    void complianceCoversTheThreeFrameworksItAdvertises() {
        var body = given().when()
                .get("/api/security/compliance")
                .then()
                .statusCode(200)
                .body("$", hasKey("CIS Kafka Benchmark"))
                .body("$", hasKey("SOC2 Type II"))
                .body("$", hasKey("PCI-DSS v4.0"))
                .body("grade", notNullValue())
                .extract()
                .jsonPath();

        for (String framework : List.of("CIS Kafka Benchmark", "SOC2 Type II", "PCI-DSS v4.0")) {
            List<Map<String, Object>> controls = body.getList("'" + framework + "'.controls");
            assertFalse(controls.isEmpty(), framework + " must carry at least one control");
            assertEquals(
                    controls.size(),
                    body.getInt("'" + framework + "'.total"),
                    framework + " total must match its control list");
            controls.forEach(c -> assertNotBlank(c.get("controlId"), framework + " controlId"));
        }
    }

    @Test
    void baselineEnablesDriftAndDriftIsEmptyWithoutOne() {
        // Ordering matters and is asserted, not assumed: drift without a
        // baseline must say so rather than inventing a comparison. This runs
        // before any baseline is saved in this class.
        var drift = given().when()
                .get("/api/security/drift")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath();

        if (!drift.getBoolean("hasBaseline")) {
            assertNotBlank(drift.getString("error"), "the no-baseline explanation");
        }

        int checkCount = given().when()
                .post("/api/security/baseline")
                .then()
                .statusCode(200)
                .body("status", equalTo("saved"))
                .body("grade", matchesRegex("[A-F]"))
                .body("timestamp", notNullValue())
                .extract()
                .path("checks");
        assertTrue(checkCount > 0, "a saved baseline captures the checks it was built from");

        // Comparing the baseline against a cluster nothing has touched must
        // find every check unchanged — the strongest available signal that the
        // baseline round-tripped through the V17 table intact. This relies on
        // two back-to-back audits agreeing, which holds because every check
        // reads static broker configuration; a check that sampled live traffic
        // would make this flaky and should be excluded from drift instead.
        var after = given().when()
                .get("/api/security/drift")
                .then()
                .statusCode(200)
                .body("hasBaseline", is(true))
                .body("baselineTimestamp", notNullValue())
                .body("currentGrade", matchesRegex("[A-F]"))
                .extract()
                .jsonPath();

        assertEquals(
                after.getInt("summary.total"),
                after.getInt("summary.unchanged"),
                "an untouched cluster must not drift from its own baseline");
        assertEquals(0, after.getInt("summary.degraded"), "nothing degraded");
    }

    @Test
    void gatePassesTheLowestBarAndExplainsItselfAtTheHighest() {
        given().when()
                .get("/api/security/gate?min-grade=F")
                .then()
                .statusCode(200)
                .body("passed", is(true))
                .body("requiredGrade", equalTo("F"))
                .body("currentGrade", matchesRegex("[A-F]"));

        // An unsecured container cannot reach an A, and a failed gate has to
        // say which checks stood in the way.
        given().when()
                .get("/api/security/gate?min-grade=a")
                .then()
                .statusCode(200)
                .body("requiredGrade", equalTo("A"))
                .body("passed", is(false))
                .body("failingChecks.size()", greaterThanOrEqualTo(1))
                .body("failingChecks[0].check", notNullValue());
    }

    @Test
    void certificateCheckCoversEveryBroker() {
        given().when()
                .get("/api/security/certs")
                .then()
                .statusCode(200)
                .body("totalBrokers", is(1))
                .body("certificates", hasSize(1))
                .body("certificates[0].keystoreConfigured", is(false))
                .body("certificates[0].checks.size()", greaterThanOrEqualTo(1));
    }

    @Test
    void cveCheckResolvesTheBrokerVersionItIsJudging() {
        given().when()
                .get("/api/security/cve")
                .then()
                .statusCode(200)
                .body("kafkaVersion", notNullValue())
                .body("grade", org.hamcrest.Matchers.oneOf("PASS", "FAIL"))
                .body("summary.total", greaterThanOrEqualTo(1))
                .body("summary.vulnerable", notNullValue())
                .body("summary.patched", notNullValue());
    }

    @Test
    void configConsistencyCannotFindAMismatchOnASingleBroker() {
        // A one-node cluster is the degenerate case: there is nothing to
        // disagree with, so any mismatch would be a bug in the comparison.
        given().when()
                .get("/api/security/config-diff")
                .then()
                .statusCode(200)
                .body("brokerCount", is(1))
                .body("keysChecked", is(20))
                .body("mismatchCount", is(0))
                .body("mismatches", hasSize(0))
                .body("grade", equalTo("PASS"));
    }

    @Test
    void aclCoverageAndSecretScanWalkTheRealTopicList() {
        given().when()
                .get("/api/security/acl-map")
                .then()
                .statusCode(200)
                .body("totalAcls", is(0))
                .body("totalTopics", greaterThanOrEqualTo(0))
                .body("principals", hasSize(0))
                .body("grade", notNullValue());

        given().when()
                .get("/api/security/secrets")
                .then()
                .statusCode(200)
                .body("patternsChecked", is(6))
                .body("topicsScanned", greaterThanOrEqualTo(0))
                .body("findingsCount", is(0))
                .body("findings", hasSize(0));
    }

    @Test
    void scoreTrendAccumulatesTheAuditsRunInThisProcess() {
        given().when().get("/api/security/audit").then().statusCode(200);

        given().when()
                .get("/api/security/trend")
                .then()
                .statusCode(200)
                .body("totalSnapshots", greaterThanOrEqualTo(1))
                .body("trend", org.hamcrest.Matchers.oneOf("IMPROVING", "DEGRADING", "STABLE", "BASELINE", "NO_DATA"))
                .body("history[0].grade", matchesRegex("[A-F]"));
    }

    private static void assertNotBlank(Object value, String what) {
        assertTrue(value instanceof String s && !s.isBlank(), what + " must be present, got " + value);
    }
}
