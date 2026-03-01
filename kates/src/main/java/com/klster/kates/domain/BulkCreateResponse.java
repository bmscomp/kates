package com.klster.kates.domain;

import java.util.List;

public record BulkCreateResponse(
    long created,
    List<TestRunSummary> runs
) {
    public record TestRunSummary(
        String id,
        String status,
        String error
    ) {
        public static TestRunSummary success(String id, String status) {
            return new TestRunSummary(id, status, null);
        }
        
        public static TestRunSummary failure(String error) {
            return new TestRunSummary(null, null, error);
        }
    }
}
