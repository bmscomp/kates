package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.List;
import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.audit.Actor;
import com.bmscomp.kates.compliance.ComplianceCheckEntity;
import com.bmscomp.kates.compliance.ComplianceCheckRepository;
import com.bmscomp.kates.compliance.ComplianceReport;

/**
 * Stored compliance checks on real PostgreSQL: V27 as Flyway applies it,
 * and the period queries with and without their bounds, which ComplianceResourceTest
 * only runs on H2.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class ComplianceChecksIT {

    @Inject
    ComplianceCheckRepository checks;

    private String store(String checkedAt, ComplianceReport.Verdict verdict) {
        ComplianceReport report = new ComplianceReport(
                "conformity.kates.io/v1alpha1",
                "ConformityReport",
                "prod-3az",
                new ComplianceReport.Target("strimzi", "kafka", "krafter", "4L6g3nShT-eMCtK--X86sw"),
                Instant.parse(checkedAt),
                new ComplianceReport.ClusterState(true, 7L, 7L, 0),
                verdict,
                new ComplianceReport.Summary(1, 0, 0, 0, 0),
                List.of());
        return checks.store(report, "{}".getBytes(StandardCharsets.UTF_8), new Actor("ci-gate", "agent"))
                .getId();
    }

    @Test
    void storesAndReadsBackWithEveryColumnV27Made() {
        String id = store("2030-06-01T08:00:00.123456Z", ComplianceReport.Verdict.PASS);

        ComplianceCheckEntity row = checks.find(id).orElseThrow();
        assertEquals(Instant.parse("2030-06-01T08:00:00.123456Z"), row.getCheckedAt());
        assertEquals("prod-3az", row.getReference());
        assertEquals("4L6g3nShT-eMCtK--X86sw", row.getClusterId());
        assertEquals("PASS", row.getVerdict());
        assertEquals("ci-gate", row.getActor());
        assertEquals("agent", row.getPrincipalType());
        assertEquals("44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a", row.getReferenceSha256());
        assertTrue(row.getReportJson().contains("\"krafter\""), row.getReportJson());
    }

    @Test
    void boundsThePeriodWithEitherBoundOrNone() {
        store("2031-03-01T00:00:00Z", ComplianceReport.Verdict.PASS);
        store("2031-03-02T00:00:00Z", ComplianceReport.Verdict.FAIL);
        store("2031-03-03T00:00:00Z", ComplianceReport.Verdict.PASS);
        Instant from = Instant.parse("2031-03-01T12:00:00Z");
        Instant to = Instant.parse("2031-03-03T00:00:00Z");

        assertEquals(1, checks.count(from, to));
        assertEquals("FAIL", checks.list(from, to, 0, 10).get(0).getVerdict());
        assertEquals(2, checks.count(from, null));
        List<ComplianceCheckEntity> newest = checks.list(Instant.parse("2031-03-01T00:00:00Z"), null, 0, 2);
        assertEquals(Instant.parse("2031-03-03T00:00:00Z"), newest.get(0).getCheckedAt());
        assertEquals(Instant.parse("2031-03-02T00:00:00Z"), newest.get(1).getCheckedAt());
        assertTrue(checks.count(null, to) >= 2);
        assertTrue(checks.count(null, null) >= 3);
    }
}
