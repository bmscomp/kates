package com.bmscomp.kates.service;

import java.util.List;
import java.util.Optional;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.persistence.EntityMapper;
import com.bmscomp.kates.persistence.TestRunEntity;

@ApplicationScoped
public class TestRunRepository {

    @Inject
    EntityManager em;

    @Transactional
    public void save(TestRun run) {
        em.merge(EntityMapper.toEntity(run));
    }

    public Optional<TestRun> findById(String id) {
        var entity = em.find(TestRunEntity.class, id);
        return entity != null ? Optional.of(EntityMapper.toDomain(entity)) : Optional.empty();
    }

    public List<TestRun> findAll() {
        return em
                .createQuery("SELECT r FROM TestRunEntity r ORDER BY r.createdAt DESC", TestRunEntity.class)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    public List<TestRun> findByType(TestType type) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    @Transactional
    public void delete(String id) {
        var entity = em.find(TestRunEntity.class, id);
        if (entity != null) {
            em.remove(entity);
        }
    }

    public List<TestRun> findByLabel(String key, String value) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.labelsJson LIKE :pattern ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("pattern", "%" + "\"" + key + "\":\"" + value + "\"" + "%")
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    /**
     * JSONB-native label search using PostgreSQL containment operator.
     * Requires the GIN index from V11 migration. Falls back to LIKE-based
     * {@link #findByLabel} if running on H2 (tests).
     */
    @SuppressWarnings("unchecked")
    public List<TestRun> findByLabelJsonb(String key, String value) {
        try {
            String jsonPattern = "{\"" + key + "\":\"" + value + "\"}";
            return ((List<TestRunEntity>) em
                    .createNativeQuery(
                            "SELECT * FROM test_runs WHERE labels_json @> CAST(:pattern AS jsonb) ORDER BY created_at DESC",
                            TestRunEntity.class)
                    .setParameter("pattern", jsonPattern)
                    .getResultList())
                    .stream()
                    .map(EntityMapper::toDomain)
                    .collect(Collectors.toList());
        } catch (Exception e) {
            return findByLabel(key, value);
        }
    }

    public Optional<TestRun> findLatestByType(TestType type) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setMaxResults(1)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .findFirst();
    }

    public List<TestRun> findAllPaged(int page, int size) {
        return em
                .createQuery("SELECT r FROM TestRunEntity r ORDER BY r.createdAt DESC", TestRunEntity.class)
                .setFirstResult(page * size)
                .setMaxResults(size)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    public long countAll() {
        return em.createQuery("SELECT COUNT(r) FROM TestRunEntity r", Long.class)
                .getSingleResult();
    }

    public List<TestRun> findByTypePaged(TestType type, int page, int size) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setFirstResult(page * size)
                .setMaxResults(size)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    public long countByType(TestType type) {
        return em.createQuery("SELECT COUNT(r) FROM TestRunEntity r WHERE r.testType = :type", Long.class)
                .setParameter("type", type)
                .getSingleResult();
    }

    public List<TestRun> findByStatus(com.bmscomp.kates.domain.TestResult.TaskStatus status) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.status = :status ORDER BY r.createdAt DESC",
                        TestRunEntity.class)
                .setParameter("status", status)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }

    public List<TestRun> findByTypeAndDateRange(TestType type, java.time.Instant from, java.time.Instant to) {
        return em
                .createQuery(
                        "SELECT r FROM TestRunEntity r WHERE r.testType = :type AND r.createdAt >= :from AND r.createdAt <= :to ORDER BY r.createdAt ASC",
                        TestRunEntity.class)
                .setParameter("type", type)
                .setParameter("from", from)
                .setParameter("to", to)
                .getResultList()
                .stream()
                .map(EntityMapper::toDomain)
                .collect(Collectors.toList());
    }
}
