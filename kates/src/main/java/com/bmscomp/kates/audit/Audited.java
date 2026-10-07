package com.bmscomp.kates.audit;

import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * A REST endpoint every call of which leaves an audit row, refused and failed
 * calls included: what the call does ({@code CREATE}, {@code RUN}...) and to
 * what kind of thing. {@link AuditResponseFilter} writes the row unless the
 * handler wrote one of its own. Every endpoint that changes anything carries
 * this or {@link NotAudited}; a test holds them to it.
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.METHOD)
public @interface Audited {

    /** The action, at most 32 characters: CREATE, UPDATE, DELETE, RUN, CANCEL, START, STOP, PRODUCE or READ. */
    String action();

    /** The kind of thing acted on, at most 32 characters, such as test or webhook. */
    String type();
}
