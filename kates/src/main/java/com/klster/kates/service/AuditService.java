package com.klster.kates.service;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.klster.kates.persistence.AuditEventEntity;

@ApplicationScoped
public class AuditService {

    @Inject
    EntityManager em;

    private static final org.jboss.logging.Logger LOG = org.jboss.logging.Logger.getLogger(AuditService.class);

    @Transactional
    public void record(String action, String eventType, String target, String details) {
        int maxRetries = 3;
        for (int attempt = 1; attempt <= maxRetries; attempt++) {
            try {
                AuditEventEntity event = new AuditEventEntity(action, eventType, target, details);
                em.persist(event);
                return;
            } catch (Exception e) {
                if (attempt == maxRetries) {
                    LOG.warnf(
                            "Audit event dropped after %d retries: action=%s type=%s target=%s — %s",
                            maxRetries, action, eventType, target, e.getMessage());
                } else {
                    try {
                        Thread.sleep(200L * attempt);
                    } catch (InterruptedException ie) {
                        Thread.currentThread().interrupt();
                        return;
                    }
                }
            }
        }
    }

    @Transactional
    public void record(String action, String eventType, String target) {
        record(action, eventType, target, null);
    }

    public List<Map<String, Object>> list(int limit, String eventType, String since) {
        var cb = em.getCriteriaBuilder();
        var cq = cb.createQuery(AuditEventEntity.class);
        var root = cq.from(AuditEventEntity.class);
        cq.orderBy(cb.desc(root.get("createdAt")));

        var predicates = new java.util.ArrayList<jakarta.persistence.criteria.Predicate>();

        if (eventType != null && !eventType.isEmpty()) {
            predicates.add(cb.equal(root.get("eventType"), eventType));
        }
        if (since != null && !since.isEmpty()) {
            try {
                Instant sinceInstant = Instant.parse(since);
                predicates.add(cb.greaterThanOrEqualTo(root.get("createdAt"), sinceInstant));
            } catch (Exception ignored) {
            }
        }

        if (!predicates.isEmpty()) {
            cq.where(predicates.toArray(new jakarta.persistence.criteria.Predicate[0]));
        }

        return em.createQuery(cq).setMaxResults(Math.min(limit, 500)).getResultList().stream()
                .map(this::toMap)
                .collect(java.util.stream.Collectors.toList());
    }

    private Map<String, Object> toMap(AuditEventEntity e) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("id", e.getId());
        m.put("action", e.getAction());
        m.put("eventType", e.getEventType());
        m.put("target", e.getTarget());
        m.put("details", e.getDetails());
        m.put("timestamp", e.getCreatedAt().toString());
        return m;
    }
}
