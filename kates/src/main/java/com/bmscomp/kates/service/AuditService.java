package com.bmscomp.kates.service;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import io.quarkus.arc.Arc;
import io.quarkus.runtime.BlockingOperationControl;
import io.smallrye.mutiny.infrastructure.Infrastructure;

import com.bmscomp.kates.audit.Actor;
import com.bmscomp.kates.audit.Actors;
import com.bmscomp.kates.audit.AuditTrail;
import com.bmscomp.kates.persistence.AuditEventEntity;

@ApplicationScoped
public class AuditService {

    @Inject
    EntityManager em;

    private static final org.jboss.logging.Logger LOG = org.jboss.logging.Logger.getLogger(AuditService.class);

    @Inject
    Actors actors;

    @Inject
    Instance<AuditTrail> trail;

    /**
     * Records a change made by the current REST request's principal, and
     * tells the request's {@link AuditTrail} that it has its row.
     */
    @Transactional
    public void record(String action, String eventType, String target, String details) {
        if (Arc.container().requestContext().isActive()) {
            trail.get().done();
        }
        record(action, eventType, target, details, actors.current());
    }

    /** Records a change made by actor, which is null when no one can be named. */
    @Transactional
    public void record(String action, String eventType, String target, String details, Actor actor) {
        int maxRetries = 3;
        for (int attempt = 1; attempt <= maxRetries; attempt++) {
            try {
                AuditEventEntity event = new AuditEventEntity(action, eventType, target, details);
                if (actor != null) {
                    event.setActor(actor.name());
                    event.setPrincipalType(actor.type());
                }
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

    /**
     * Runs write where it may block: at once on a worker thread, or else on
     * the worker pool. A response filter or a gRPC call can end on the event
     * loop, where a database write is not allowed.
     */
    public static void blockingSafe(Runnable write) {
        if (BlockingOperationControl.isBlockingAllowed()) {
            write.run();
        } else {
            Infrastructure.getDefaultWorkerPool().execute(write);
        }
    }

    public List<Map<String, Object>> list(int limit, String eventType, String since, String actor) {
        var cb = em.getCriteriaBuilder();
        var cq = cb.createQuery(AuditEventEntity.class);
        var root = cq.from(AuditEventEntity.class);
        cq.orderBy(cb.desc(root.get("createdAt")));

        var predicates = new java.util.ArrayList<jakarta.persistence.criteria.Predicate>();

        if (eventType != null && !eventType.isEmpty()) {
            predicates.add(cb.equal(root.get("eventType"), eventType));
        }
        if (actor != null && !actor.isEmpty()) {
            predicates.add(cb.equal(root.get("actor"), actor));
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
        if (e.getActor() != null) {
            m.put("actor", e.getActor());
            m.put("principalType", e.getPrincipalType());
        }
        m.put("timestamp", e.getCreatedAt().toString());
        return m;
    }
}
