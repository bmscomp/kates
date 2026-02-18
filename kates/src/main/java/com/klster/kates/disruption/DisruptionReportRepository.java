package com.klster.kates.disruption;

import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

/**
 * Repository for persisting and querying disruption reports using JPA EntityManager.
 */
@ApplicationScoped
public class DisruptionReportRepository {

    @Inject
    EntityManager em;

    @Transactional
    public void save(DisruptionReportEntity entity) {
        em.persist(entity);
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

    public List<DisruptionReportEntity> findByStatus(String status) {
        return em.createQuery(
                        "FROM DisruptionReportEntity WHERE status = :status ORDER BY createdAt DESC",
                        DisruptionReportEntity.class)
                .setParameter("status", status)
                .getResultList();
    }
}
