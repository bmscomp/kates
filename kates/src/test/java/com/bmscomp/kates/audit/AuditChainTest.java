package com.bmscomp.kates.audit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.persistence.AuditEventEntity;

class AuditChainTest {

    private static AuditEventEntity row(long id, String details) {
        AuditEventEntity e = new AuditEventEntity("RUN", "test", "run-" + id, details);
        e.setId(id);
        e.setCreatedAt(Instant.parse("2026-10-30T12:00:00.123456Z").plusSeconds(id));
        e.setActor("ci-gate");
        e.setPrincipalType("agent");
        return e;
    }

    /** Rows 1..n, chained as AuditChain.append chains them. */
    private static List<AuditEventEntity> chain(int n) {
        List<AuditEventEntity> rows = new ArrayList<>();
        String last = AuditChain.GENESIS;
        for (long id = 1; id <= n; id++) {
            AuditEventEntity e = row(id, "HTTP 200");
            e.setPrevHash(last);
            e.setRowHash(AuditChain.hash(e));
            last = e.getRowHash();
            rows.add(e);
        }
        return rows;
    }

    private static AuditChain.Walk walk(List<AuditEventEntity> rows, String headHash) {
        AuditChain.Walk walk = new AuditChain.Walk();
        for (AuditEventEntity row : rows) {
            if (!walk.accept(row)) {
                break;
            }
        }
        walk.finish((long) rows.size(), headHash);
        return walk;
    }

    @Test
    void anUntouchedChainHolds() {
        List<AuditEventEntity> rows = chain(5);
        AuditChain.Walk walk = walk(rows, rows.get(4).getRowHash());
        assertNull(walk.problem);
        assertEquals(5, walk.chained);
    }

    @Test
    void anEditedRowBreaksTheChainThere() {
        List<AuditEventEntity> rows = chain(5);
        rows.get(2).setDetails("HTTP 403");
        AuditChain.Walk walk = walk(rows, rows.get(4).getRowHash());
        assertEquals(3L, walk.brokenAt);
        assertTrue(walk.problem.contains("does not match its hash"), walk.problem);
    }

    @Test
    void aRemovedRowBreaksTheChainAtTheNext() {
        List<AuditEventEntity> rows = chain(5);
        String head = rows.get(4).getRowHash();
        rows.remove(2);
        AuditChain.Walk walk = walk(rows, head);
        assertEquals(4L, walk.brokenAt);
        assertTrue(walk.problem.contains("does not follow"), walk.problem);
    }

    @Test
    void removedOldestRowsBreakTheChainAtTheFirstLeft() {
        List<AuditEventEntity> rows = chain(5);
        String head = rows.get(4).getRowHash();
        rows.remove(0);
        assertEquals(2L, walk(rows, head).brokenAt);
    }

    @Test
    void removedNewestRowsShowAgainstTheHead() {
        List<AuditEventEntity> rows = chain(5);
        String head = rows.get(4).getRowHash();
        rows.remove(4);
        AuditChain.Walk walk = walk(rows, head);
        assertNull(walk.brokenAt);
        assertTrue(walk.problem.contains("newest rows are missing"), walk.problem);
    }

    @Test
    void rowsFromBeforeTheChainAreCountedNotChecked() {
        List<AuditEventEntity> rows = new ArrayList<>(List.of(row(-2, "old"), row(-1, "old")));
        List<AuditEventEntity> chained = chain(3);
        rows.addAll(chained);
        AuditChain.Walk walk = walk(rows, chained.get(2).getRowHash());
        assertNull(walk.problem);
        assertEquals(2, walk.unchained);
        assertEquals(3, walk.chained);
    }

    @Test
    void aRowWithoutAHashAfterTheChainBeganBreaksIt() {
        List<AuditEventEntity> rows = chain(3);
        rows.add(2, row(99, "slipped in"));
        AuditChain.Walk walk = walk(rows, rows.get(3).getRowHash());
        assertEquals(99L, walk.brokenAt);
    }

    @Test
    void theHashSeesWhereOneFieldEndsAndTheNextBegins() {
        AuditEventEntity a = row(1, "ab");
        a.setActor("c");
        AuditEventEntity b = row(1, "a");
        b.setActor("bc");
        assertNotEquals(AuditChain.hash(a), AuditChain.hash(b));

        a.setActor(null);
        b.setDetails("ab");
        b.setActor("");
        assertFalse(AuditChain.hash(a).equals(AuditChain.hash(b)), "null and empty must hash apart");
    }
}
