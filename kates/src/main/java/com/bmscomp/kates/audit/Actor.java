package com.bmscomp.kates.audit;

import com.bmscomp.kates.security.KatesPrincipal;

/**
 * Who an audit row says acted: a principal's name and whether a person
 * (human), an agent or the Kates API itself (system) acted.
 */
public record Actor(String name, String type) {

    /** The Kates API's schedulers, which start runs and faults with no request behind them. */
    public static final Actor SCHEDULER = new Actor("system:scheduler", "system");

    public static Actor of(KatesPrincipal principal) {
        return new Actor(principal.name(), principal.type().wireName());
    }
}
