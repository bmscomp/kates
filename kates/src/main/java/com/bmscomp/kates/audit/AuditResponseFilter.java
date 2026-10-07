package com.bmscomp.kates.audit;

import java.lang.reflect.Method;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerResponseContext;
import jakarta.ws.rs.container.ContainerResponseFilter;
import jakarta.ws.rs.container.ResourceInfo;
import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.ext.Provider;

import com.bmscomp.kates.service.AuditService;

/**
 * Writes the audit row of every call to an {@link Audited} endpoint whose
 * handler did not write its own: who called, what the endpoint does, what it
 * acted on, and the HTTP status. The target is the id the answer carries, as
 * a disruption's report id, so the row ties a principal to what it started;
 * else the request path. So a call that was refused (403) or failed leaves a
 * row too, naming who tried. A call without a valid key names no one and
 * leaves none.
 */
@Provider
public class AuditResponseFilter implements ContainerResponseFilter {

    static final int TARGET_LENGTH = 128;

    @Context
    ResourceInfo resourceInfo;

    @Inject
    AuditTrail trail;

    @Inject
    Actors actors;

    @Inject
    AuditService auditService;

    @Override
    public void filter(ContainerRequestContext request, ContainerResponseContext response) {
        Method method = resourceInfo.getResourceMethod();
        Audited audited = method == null ? null : method.getAnnotation(Audited.class);
        if (audited == null || trail.isDone()) {
            return;
        }
        Actor actor = actors.current();
        if (actor == null) {
            return;
        }
        String id = idOf(response.getEntity());
        String what = id != null ? id : request.getUriInfo().getPath();
        String target = what.length() <= TARGET_LENGTH ? what : what.substring(0, TARGET_LENGTH);
        String details = "HTTP " + response.getStatus();
        AuditService.blockingSafe(() -> auditService.record(audited.action(), audited.type(), target, details, actor));
    }

    /** The id an answer carries as an "id" entry, or null. */
    static String idOf(Object entity) {
        return entity instanceof Map<?, ?> map && map.get("id") instanceof String id && !id.isBlank() ? id : null;
    }
}
