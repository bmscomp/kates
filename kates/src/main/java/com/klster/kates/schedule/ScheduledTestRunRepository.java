package com.klster.kates.schedule;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import java.util.List;
import java.util.Optional;

/**
 * JPA repository for {@link ScheduledTestRun} persistence.
 */
@ApplicationScoped
public class ScheduledTestRunRepository {

    @Inject
    EntityManager em;

    @Transactional
    public void save(ScheduledTestRun schedule) {
        ScheduledTestRun existing = em.find(ScheduledTestRun.class, schedule.getId());
        if (existing != null) {
            existing.setName(schedule.getName());
            existing.setCronExpression(schedule.getCronExpression());
            existing.setEnabled(schedule.isEnabled());
            existing.setRequestJson(schedule.getRequestJson());
            existing.setLastRunId(schedule.getLastRunId());
            existing.setLastRunAt(schedule.getLastRunAt());
            em.merge(existing);
        } else {
            em.persist(schedule);
        }
    }

    public Optional<ScheduledTestRun> findById(String id) {
        ScheduledTestRun entity = em.find(ScheduledTestRun.class, id);
        return Optional.ofNullable(entity);
    }

    public List<ScheduledTestRun> findAll() {
        return em.createQuery("SELECT s FROM ScheduledTestRun s ORDER BY s.createdAt DESC",
                ScheduledTestRun.class).getResultList();
    }

    public List<ScheduledTestRun> findAllEnabled() {
        return em.createQuery(
                "SELECT s FROM ScheduledTestRun s WHERE s.enabled = true ORDER BY s.createdAt DESC",
                ScheduledTestRun.class).getResultList();
    }

    @Transactional
    public void delete(String id) {
        ScheduledTestRun entity = em.find(ScheduledTestRun.class, id);
        if (entity != null) {
            em.remove(entity);
        }
    }

    @Transactional
    public void updateLastRun(String scheduleId, String runId) {
        ScheduledTestRun entity = em.find(ScheduledTestRun.class, scheduleId);
        if (entity != null) {
            entity.setLastRunId(runId);
            entity.setLastRunAt(java.time.Instant.now());
            em.merge(entity);
        }
    }
}
