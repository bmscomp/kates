package com.bmscomp.kates.domain;

import java.util.List;

/**
 * The answer to DELETE /api/tests. matched is what matched before the call,
 * remaining what still matches after it, so a caller that deletes a limit's
 * worth per call stops when remaining is 0.
 */
public record PruneResponse(
        String createdBefore, List<String> statuses, boolean dryRun, long matched, int deleted, long remaining) {}
