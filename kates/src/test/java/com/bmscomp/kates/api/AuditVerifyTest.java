package com.bmscomp.kates.api;

import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.RestAssured;
import org.junit.jupiter.api.MethodOrderer;
import org.junit.jupiter.api.Order;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestMethodOrder;

import com.bmscomp.kates.SchedulerOffTestProfile;
import com.bmscomp.kates.persistence.AuditEventEntity;
import com.bmscomp.kates.service.AuditService;

/**
 * GET /api/audit/verify against the real write path. A profile of its own
 * gives it a fresh schema, so the row it tampers with breaks no other test's
 * chain.
 */
@QuarkusTest
@TestProfile(AuditVerifyTest.OwnSchema.class)
@TestMethodOrder(MethodOrderer.OrderAnnotation.class)
class AuditVerifyTest {

    public static class OwnSchema extends SchedulerOffTestProfile {}

    @Inject
    AuditService auditService;

    @Inject
    EntityManager em;

    @Test
    @Order(0)
    void pagesEveryRowOfAPeriodPastTheOld500Cap() {
        Instant before = Instant.now();
        for (int i = 0; i < 520; i++) {
            auditService.record("RUN", "paging", "row-" + i, null, null);
        }
        Set<Object> ids = new HashSet<>();
        for (int page = 0; page < 3; page++) {
            List<Object> pageIds = RestAssured.given()
                    .queryParam("type", "paging")
                    .queryParam("page", page)
                    .queryParam("size", 200)
                    .get("/api/audit")
                    .then()
                    .statusCode(200)
                    .body("total", equalTo(520))
                    .extract()
                    .jsonPath()
                    .getList("items.id");
            ids.addAll(pageIds);
        }
        assertEquals(520, ids.size());
        RestAssured.given()
                .queryParam("type", "paging")
                .queryParam("until", before.toString())
                .get("/api/audit")
                .then()
                .statusCode(200)
                .body("total", equalTo(0));
    }

    @Test
    @Order(1)
    void concurrentWritersKeepOneChain() throws Exception {
        ExecutorService pool = Executors.newFixedThreadPool(8);
        try {
            List<Future<?>> writes = new ArrayList<>();
            for (int i = 0; i < 40; i++) {
                String target = "run-" + i;
                writes.add(pool.submit(() -> auditService.record("RUN", "test", target, "HTTP 200", null)));
            }
            for (Future<?> write : writes) {
                write.get();
            }
        } finally {
            pool.shutdown();
        }

        long rows = QuarkusTransaction.requiringNew()
                .call(() -> em.createQuery("select count(e) from AuditEventEntity e", Long.class)
                        .getSingleResult());
        assertTrue(rows >= 40, rows + " rows");
        var verification = RestAssured.get("/api/audit/verify")
                .then()
                .statusCode(200)
                .body("intact", equalTo(true))
                .body("chained", equalTo((int) rows))
                .body("unchained", equalTo(0))
                .extract()
                .jsonPath();

        // The head hash a reader keeps is the head row's rowHash in the list.
        RestAssured.given()
                .queryParam("size", 200)
                .get("/api/audit")
                .then()
                .statusCode(200)
                .body(
                        "items.find { it.id == " + verification.getLong("headId") + " }.rowHash",
                        equalTo(verification.getString("headHash")));
    }

    @Test
    @Order(2)
    void aRowEditedInTheDatabaseIsNamed() {
        Long id = QuarkusTransaction.requiringNew().call(() -> {
            AuditEventEntity row = em.createQuery(
                            "from AuditEventEntity e where e.target = 'run-7'", AuditEventEntity.class)
                    .getSingleResult();
            row.setDetails("HTTP 403");
            return row.getId();
        });

        RestAssured.get("/api/audit/verify")
                .then()
                .statusCode(200)
                .body("intact", equalTo(false))
                .body("brokenAt", equalTo(id.intValue()))
                .body("problem", containsString("does not match its hash"));
    }
}
