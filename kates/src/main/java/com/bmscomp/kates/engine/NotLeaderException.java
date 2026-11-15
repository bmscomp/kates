package com.bmscomp.kates.engine;

/**
 * Thrown when a test cannot start because this Kates member does not lead its quorum, or has not
 * confirmed that it still does. Answered as a drain is, 503 with Retry-After: the same request goes
 * through once it reaches the leader.
 */
public class NotLeaderException extends DrainingException {

    public NotLeaderException(String leaderId) {
        super(message(leaderId));
    }

    public static String message(String leaderId) {
        return leaderId == null
                ? "This Kates member does not lead its quorum, and no leader is known yet: retry later"
                : "This Kates member does not lead its quorum: " + leaderId + " does, so send it the request";
    }
}
