package com.bmscomp.kates.audit;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.HexFormat;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.persistence.LockModeType;
import jakarta.persistence.TypedQuery;

import com.bmscomp.kates.persistence.AuditChainHead;
import com.bmscomp.kates.persistence.AuditEventEntity;

/**
 * Chains the audit log (V28): each row holds the hash of the row before it
 * and a SHA-256 over that and its own fields, so a row changed, removed or
 * slipped in by hand in the database breaks the chain where it happened.
 *
 * <p>Writers take turns on the one {@code audit_chain_head} row, with a row
 * lock held until their transaction ends, so the chain never forks, whatever
 * number of replicas write. The chain does not stop someone who can write
 * the database from rewriting all of it, hashes included; a head hash kept
 * outside it, as a signed evidence pack holds, is what pins it.
 */
@ApplicationScoped
public class AuditChain {

    /** The prev_hash of the chain's first row. */
    public static final String GENESIS = "0".repeat(64);

    private static final int PAGE = 1000;

    @Inject
    EntityManager em;

    /** Links event after the newest row and persists it, in the caller's transaction. */
    public void append(AuditEventEntity event) {
        AuditChainHead head = em.find(AuditChainHead.class, AuditChainHead.ID, LockModeType.PESSIMISTIC_WRITE);
        if (head == null) {
            throw new IllegalStateException("audit_chain_head has no row: migration V28 has not run");
        }
        event.setPrevHash(head.getLastHash());
        event.setRowHash(hash(event));
        em.persist(event);
        head.advance(event.getId(), event.getRowHash());
    }

    /** What a walk of the whole chain found. */
    public record Verification(
            boolean intact,
            long chained,
            long unchained,
            Long headId,
            String headHash,
            Long brokenAt,
            String problem) {}

    /**
     * Walks every row up to the head as it stood when the walk began, oldest
     * first; rows written meanwhile wait for the next walk.
     */
    public Verification verify() {
        AuditChainHead head = em.find(AuditChainHead.class, AuditChainHead.ID);
        Long headId = head == null ? null : head.getLastId();
        String headHash = head == null ? GENESIS : head.getLastHash();
        Walk walk = new Walk();
        long after = Long.MIN_VALUE;
        boolean more = true;
        while (more && walk.brokenAt == null) {
            TypedQuery<AuditEventEntity> query = em.createQuery(
                            "from AuditEventEntity e where e.id > :after" + (headId == null ? "" : " and e.id <= :last")
                                    + " order by e.id",
                            AuditEventEntity.class)
                    .setParameter("after", after)
                    .setMaxResults(PAGE);
            if (headId != null) {
                query.setParameter("last", headId);
            }
            List<AuditEventEntity> rows = query.getResultList();
            more = rows.size() == PAGE;
            for (AuditEventEntity row : rows) {
                if (headId == null && row.getRowHash() != null) {
                    // Chained after the head was read, so not this walk's.
                    more = false;
                    break;
                }
                if (!walk.accept(row)) {
                    break;
                }
                after = row.getId();
            }
            em.clear();
        }
        walk.finish(headId, headHash);
        return new Verification(
                walk.brokenAt == null && walk.problem == null,
                walk.chained,
                walk.unchained,
                headId,
                headHash,
                walk.brokenAt,
                walk.problem);
    }

    /** The checks of a walk, row by row, oldest first. */
    static final class Walk {

        private String last;
        long chained;
        long unchained;
        Long brokenAt;
        String problem;

        /** Takes the next row, and says whether the chain still holds. */
        boolean accept(AuditEventEntity row) {
            if (row.getRowHash() == null) {
                if (last != null) {
                    return broken(row.getId(), "has no hash, though rows before it do");
                }
                unchained++;
                return true;
            }
            String expected = last == null ? GENESIS : last;
            if (!expected.equals(row.getPrevHash())) {
                return broken(
                        row.getId(), "does not follow the row before it: a row was changed, removed or added there");
            }
            if (!hash(row).equals(row.getRowHash())) {
                return broken(row.getId(), "does not match its hash: it was changed");
            }
            last = row.getRowHash();
            chained++;
            return true;
        }

        /** Holds the last row walked to the head: a removed newest row shows only here. */
        void finish(Long headId, String headHash) {
            if (brokenAt == null && !(last == null ? GENESIS : last).equals(headHash)) {
                problem = "the newest rows are missing: the chain ends before row " + headId + ", its head";
            }
        }

        private boolean broken(Long id, String why) {
            brokenAt = id;
            problem = "row " + id + " " + why;
            return false;
        }
    }

    /** SHA-256 over prev_hash and the row's fields, each length-prefixed so no two rows read alike. */
    static String hash(AuditEventEntity e) {
        StringBuilder in = new StringBuilder();
        for (String field : new String[] {
            e.getPrevHash(),
            e.getAction(),
            e.getEventType(),
            e.getTarget(),
            e.getDetails(),
            e.getCreatedAt() == null ? null : e.getCreatedAt().toString(),
            e.getActor(),
            e.getPrincipalType()
        }) {
            if (field == null) {
                in.append("-1:\n");
            } else {
                in.append(field.length()).append(':').append(field).append('\n');
            }
        }
        try {
            return HexFormat.of()
                    .formatHex(MessageDigest.getInstance("SHA-256")
                            .digest(in.toString().getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException ex) {
            throw new IllegalStateException("SHA-256 is missing from this JVM", ex);
        }
    }
}
