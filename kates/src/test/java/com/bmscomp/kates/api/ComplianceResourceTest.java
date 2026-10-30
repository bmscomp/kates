package com.bmscomp.kates.api;

import static org.hamcrest.Matchers.equalTo;
import static org.mockito.ArgumentMatchers.any;

import java.time.Instant;
import java.util.List;
import jakarta.ws.rs.ClientErrorException;

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

    @Test
    void returnsTheReportWhateverTheVerdict() {
        Mockito.when(check.check(any()))
                .thenReturn(new ComplianceReport(
                        "conformity.kates.io/v1alpha1",
                        "ConformityReport",
                        "t",
                        new ComplianceReport.Target("strimzi", "kafka", "krafter", null),
                        Instant.parse("2026-10-19T12:00:00Z"),
                        new ComplianceReport.ClusterState(true, 1L, 1L, 0),
                        ComplianceReport.Verdict.FAIL,
                        new ComplianceReport.Summary(0, 1, 0, 0, 0),
                        List.of()));

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
}
