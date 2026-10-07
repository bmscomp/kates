package com.bmscomp.kates.audit;

import jakarta.enterprise.context.RequestScoped;

/**
 * Whether the current request still needs an audit row: not once its handler
 * wrote one of its own, with the run id it created, nor for a call that
 * turned out to change nothing (a disruption dry run).
 */
@RequestScoped
public class AuditTrail {

    private boolean done;

    /** The request's audit row is written, or none is needed. */
    public void done() {
        done = true;
    }

    public boolean isDone() {
        return done;
    }
}
