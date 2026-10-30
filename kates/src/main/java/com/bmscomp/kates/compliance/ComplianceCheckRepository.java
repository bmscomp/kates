package com.bmscomp.kates.compliance;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import java.util.Optional;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.persistence.TypedQuery;
import jakarta.transaction.Transactional;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;

import com.bmscomp.kates.audit.Actor;

/** Stores each compliance check's report, and reads them back newest first. */
@ApplicationScoped
public class ComplianceCheckRepository {

    @Inject
    EntityManager em;

    @Inject
    ObjectMapper objectMapper;

    /**
     * Stores report, checked against the reference body, as run by actor
     * (null when no one can be named), and returns the new row.
     */
    @Transactional
    public ComplianceCheckEntity store(ComplianceReport report, byte[] body, Actor actor) {
        String json;
        try {
            json = objectMapper.writeValueAsString(report);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("cannot serialize the compliance report", e);
        }
        ComplianceCheckEntity entity = new ComplianceCheckEntity(
                UUID.randomUUID().toString(),
                report.checkedAt(),
                report.reference(),
                sha256(body),
                report.target() == null ? null : report.target().clusterId(),
                report.verdict().name(),
                actor == null ? null : actor.name(),
                actor == null ? null : actor.type(),
                json);
        em.persist(entity);
        return entity;
    }

    /**
     * The SHA-256 of the report as stored. The check's audit row records it
     * when the check runs, and the audit log is a hash chain, so a report
     * edited in the database no longer matches its row there.
     */
    public static String reportSha256(ComplianceCheckEntity entity) {
        return sha256(entity.getReportJson().getBytes(StandardCharsets.UTF_8));
    }

    public Optional<ComplianceCheckEntity> find(String id) {
        return Optional.ofNullable(em.find(ComplianceCheckEntity.class, id));
    }

    /** The checks run in [since, until), either bound null for none, newest first. */
    public List<ComplianceCheckEntity> list(Instant since, Instant until, int first, int max) {
        return window(ComplianceCheckEntity.class, "c", since, until)
                .setFirstResult(first)
                .setMaxResults(max)
                .getResultList();
    }

    public long count(Instant since, Instant until) {
        return window(Long.class, "count(c)", since, until).getSingleResult();
    }

    private <T> TypedQuery<T> window(Class<T> type, String select, Instant since, Instant until) {
        // Bounds are added only when set: Postgres can't type a null parameter in ":since is null".
        String where = " where 1 = 1" + (since == null ? "" : " and c.checkedAt >= :since")
                + (until == null ? "" : " and c.checkedAt < :until");
        String order = type == Long.class ? "" : " order by c.checkedAt desc, c.id";
        TypedQuery<T> q = em.createQuery("select " + select + " from ComplianceCheckEntity c" + where + order, type);
        if (since != null) {
            q.setParameter("since", since);
        }
        if (until != null) {
            q.setParameter("until", until);
        }
        return q;
    }

    static String sha256(byte[] body) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(body));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is missing from this JVM", e);
        }
    }
}
