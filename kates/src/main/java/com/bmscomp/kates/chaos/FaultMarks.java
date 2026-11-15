package com.bmscomp.kates.chaos;

import java.time.Instant;
import java.util.Map;

/**
 * What every object a fault creates or changes carries, so that a later leader can tell a fault
 * of an older term, or one past its deadline, from the faults it runs itself.
 */
public final class FaultMarks {

    /** The Ratis term of the leader that wrote the object; {@code 0} with no quorum. */
    public static final String TERM = "kates.io/term";

    /** The instant after which any leader undoes the fault, whoever made it. */
    public static final String DEADLINE = "kates.io/deadline";

    private FaultMarks() {}

    public static Map<String, String> of(long term, Instant deadline) {
        return Map.of(TERM, Long.toString(term), DEADLINE, deadline.toString());
    }
}
