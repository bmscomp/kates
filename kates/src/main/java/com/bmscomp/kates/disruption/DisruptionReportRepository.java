package com.bmscomp.kates.disruption;

import java.time.Instant;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.persistence.LockModeType;
import jakarta.transaction.Transactional;

/**
 * Repository for persisting and querying disruption reports using JPA EntityManager.
 */
@ApplicationScoped
public class DisruptionReportRepository {

    @Inject
    EntityManager em;

    /**
     * Stores a report: inserts a new id, and replaces the report stored under
     * an existing one.
     *
     * <p>A plan started through the launcher is saved twice under one id: a
     * RUNNING placeholder when it starts and its outcome when it ends. This
     * used to persist both, and the id is assigned rather than generated, so
     * Hibernate took the second for a new row and the insert failed on the
     * primary key: the report stayed RUNNING with no steps, and the CLI polled
     * it until it gave up. Template and scheduled runs save a new id once and
     * still insert.
     *
     * <p>The row keeps its {@code createdAt}, which for a launched plan is when
     * it started; the list is ordered by it.
     */
    @Transactional
    public void save(DisruptionReportEntity entity) {
        DisruptionReportEntity existing = em.find(DisruptionReportEntity.class, entity.getId());
        if (existing == null) {
            em.persist(entity);
        } else {
            copyReport(entity, existing);
        }
    }

    /**
     * Replaces the stored report only while its status is still
     * {@code expectedCurrent}, and says whether it did. The row lock makes the
     * check and the write atomic, so a plan that finishes meanwhile keeps its
     * outcome.
     */
    @Transactional
    public boolean saveIfStatus(DisruptionReportEntity entity, String expectedCurrent) {
        DisruptionReportEntity existing =
                em.find(DisruptionReportEntity.class, entity.getId(), LockModeType.PESSIMISTIC_WRITE);
        if (existing == null || !expectedCurrent.equals(existing.getStatus())) {
            return false;
        }
        copyReport(entity, existing);
        return true;
    }

    private static void copyReport(DisruptionReportEntity from, DisruptionReportEntity to) {
        to.setPlanName(from.getPlanName());
        to.setStatus(from.getStatus());
        to.setSlaGrade(from.getSlaGrade());
        to.setReportJson(from.getReportJson());
        to.setSummaryJson(from.getSummaryJson());
    }

    public DisruptionReportEntity findById(String id) {
        return em.find(DisruptionReportEntity.class, id);
    }

    public List<DisruptionReportEntity> listRecent(int limit) {
        return em.createQuery("FROM DisruptionReportEntity ORDER BY createdAt DESC", DisruptionReportEntity.class)
                .setMaxResults(limit)
                .getResultList();
    }

    public List<DisruptionReportEntity> findByPlanName(String planName) {
        return em.createQuery(
                        "FROM DisruptionReportEntity WHERE planName = :name ORDER BY createdAt DESC",
                        DisruptionReportEntity.class)
                .setParameter("name", planName)
                .getResultList();
    }

    /**
     * Transactional, unlike the other finders, because the orphan reconciler
     * calls it from a thread of its own, which has no request context: without
     * a transaction the EntityManager refuses the query.
     */
    @Transactional
    public List<DisruptionReportEntity> findByStatusCreatedBefore(String status, Instant createdBefore) {
        return em.createQuery(
                        "FROM DisruptionReportEntity WHERE status = :status AND createdAt < :before"
                                + " ORDER BY createdAt DESC",
                        DisruptionReportEntity.class)
                .setParameter("status", status)
                .setParameter("before", createdBefore)
                .getResultList();
    }
}
