package com.bmscomp.kates.api;

import static org.hamcrest.Matchers.contains;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.nullValue;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.ws.rs.ClientErrorException;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.SchedulerOffTestProfile;
import com.bmscomp.kates.compliance.ComplianceCheck;
import com.bmscomp.kates.compliance.ComplianceReport;

@QuarkusTest
@TestProfile(SchedulerOffTestProfile.class)
class ComplianceResourceTest {

    private static final String REFERENCE =
            "{\"apiVersion\":\"conformity.kates.io/v1alpha1\",\"kind\":\"KafkaReference\","
                    + "\"metadata\":{\"name\":\"t\"},\"spec\":{\"brokers\":{\"count\":3}}}";

    @InjectMock
    ComplianceCheck check;

    @Inject
    EntityManager em;

    private static ComplianceReport report(String checkedAt, ComplianceReport.Verdict verdict) {
        return new ComplianceReport(
                "conformity.kates.io/v1alpha1",
                "ConformityReport",
                "t",
                new ComplianceReport.Target("strimzi", "kafka", "krafter", "Hn3x2Yk1QeWm0Zp4bLr8Tg"),
                Instant.parse(checkedAt),
                new ComplianceReport.ClusterState(true, 1L, 1L, 0),
                verdict,
                new ComplianceReport.Summary(0, verdict == ComplianceReport.Verdict.FAIL ? 1 : 0, 0, 0, 0),
                List.of());
    }

    @Test
    void returnsTheReportWhateverTheVerdict() {
        Mockito.when(check.check(any())).thenReturn(report("2026-10-19T12:00:00Z", ComplianceReport.Verdict.FAIL));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE)
                .post("/api/conformity/check")
                .then()
                .statusCode(200)
                .body("verdict", equalTo("FAIL"))
                .body("summary.fail", equalTo(1));
    }

    @Test
    void refusesAnInvalidReferenceByPath() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE.replace("\"count\"", "\"cuont\""))
                .post("/api/conformity/check")
                .then()
                .statusCode(400)
                .body("fieldErrors.'spec.brokers.cuont'", equalTo("unknown field"));
        Mockito.verifyNoInteractions(check);
    }

    @Test
    void refusesAReferenceForAnotherClusterWith409() {
        Mockito.when(check.check(any())).thenThrow(new ClientErrorException("The reference is for Kafka x/y", 409));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE)
                .post("/api/conformity/check")
                .then()
                .statusCode(409)
                .body("message", equalTo("The reference is for Kafka x/y"));
    }

    @Test
    void storesEachReportWithWhoRanItAndWhatItWasSent() throws Exception {
        Mockito.when(check.check(any())).thenReturn(report("2026-10-19T12:00:00Z", ComplianceReport.Verdict.FAIL));

        String location = RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE)
                .post("/api/conformity/check")
                .then()
                .statusCode(200)
                .extract()
                .header("Location");
        String id = location.substring(location.lastIndexOf('/') + 1);

        String sha256 = HexFormat.of()
                .formatHex(MessageDigest.getInstance("SHA-256").digest(REFERENCE.getBytes(StandardCharsets.UTF_8)));
        RestAssured.get("/api/conformity/checks/" + id)
                .then()
                .statusCode(200)
                .body("id", equalTo(id))
                .body("checkedAt", equalTo("2026-10-19T12:00:00Z"))
                .body("reference", equalTo("t"))
                .body("clusterId", equalTo("Hn3x2Yk1QeWm0Zp4bLr8Tg"))
                .body("verdict", equalTo("FAIL"))
                .body("actor", equalTo("anonymous"))
                .body("referenceSha256", equalTo(sha256))
                .body("report.summary.fail", equalTo(1))
                .body("report.target.name", equalTo("krafter"));
        RestAssured.get("/api/audit?type=compliance").then().statusCode(200).body("items.target", hasItem(id));
    }

    @Test
    void theAuditRowHoldsTheReportHashSoAnEditInTheDatabaseShows() {
        Mockito.when(check.check(any())).thenReturn(report("2026-10-19T12:00:00Z", ComplianceReport.Verdict.FAIL));
        String location = RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE)
                .post("/api/conformity/check")
                .then()
                .statusCode(200)
                .extract()
                .header("Location");
        String id = location.substring(location.lastIndexOf('/') + 1);
        String recorded = RestAssured.given()
                .queryParam("type", "compliance")
                .queryParam("size", 200)
                .get("/api/audit")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath()
                .getString("items.find { it.target == '" + id + "' }.details");

        String stored = RestAssured.get("/api/conformity/checks/" + id)
                .then()
                .statusCode(200)
                .extract()
                .path("reportSha256");
        String referenceSha256 =
                RestAssured.get("/api/conformity/checks/" + id).then().extract().path("referenceSha256");
        assertEquals("verdict FAIL, report sha256 " + stored + ", reference sha256 " + referenceSha256, recorded);

        // What a database admin could do by hand: turn the failed rule count to 0.
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery(
                                "update compliance_checks set report_json = replace(report_json, '\"fail\":1', '\"fail\":0') where id = ?1")
                        .setParameter(1, id)
                        .executeUpdate());

        RestAssured.get("/api/conformity/checks/" + id).then().statusCode(200).body("report.summary.fail", equalTo(0));
        String after =
                RestAssured.get("/api/conformity/checks/" + id).then().extract().path("reportSha256");
        assertNotEquals(stored, after);
        assertTrue(recorded.contains(stored) && !recorded.contains(after));
    }

    @Test
    void listsTheChecksOfAPeriodNewestFirst() {
        Mockito.when(check.check(any()))
                .thenReturn(
                        report("2031-01-01T00:00:00Z", ComplianceReport.Verdict.PASS),
                        report("2031-01-02T00:00:00Z", ComplianceReport.Verdict.FAIL),
                        report("2031-01-03T00:00:00Z", ComplianceReport.Verdict.PASS));
        for (int i = 0; i < 3; i++) {
            RestAssured.given()
                    .contentType(ContentType.JSON)
                    .body(REFERENCE)
                    .post("/api/conformity/check")
                    .then()
                    .statusCode(200);
        }

        RestAssured.given()
                .queryParam("since", "2031-01-01T12:00:00Z")
                .queryParam("until", "2031-01-03T00:00:00Z")
                .get("/api/conformity/checks")
                .then()
                .statusCode(200)
                .body("total", equalTo(1))
                .body("items.verdict", contains("FAIL"));
        RestAssured.given()
                .queryParam("since", "2031-01-01T00:00:00Z")
                .queryParam("size", 2)
                .get("/api/conformity/checks")
                .then()
                .statusCode(200)
                .body("total", equalTo(3))
                .body("items.checkedAt", contains("2031-01-03T00:00:00Z", "2031-01-02T00:00:00Z"));
    }

    @Test
    void refusesAPeriodThatIsNotAnInstant() {
        RestAssured.given()
                .queryParam("since", "yesterday")
                .get("/api/conformity/checks")
                .then()
                .statusCode(400);
    }

    @Test
    void answers404ForACheckNeverStored() {
        RestAssured.get("/api/conformity/checks/no-such-check")
                .then()
                .statusCode(404)
                .body("message", equalTo("Compliance check not found: no-such-check"));
    }

    @Test
    void storesNothingForAReferenceItRefuses() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body(REFERENCE.replace("\"count\"", "\"cuont\""))
                .post("/api/conformity/check")
                .then()
                .statusCode(400)
                .header("Location", nullValue());
    }
}
