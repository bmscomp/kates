package com.klster.kates.engine;

import java.time.Instant;

/**
 * CDI event fired on test lifecycle transitions.
 * Consumed by SSE broadcaster and webhook delivery.
 */
public class TestLifecycleEvent {

    public enum EventKind {
        CREATED, RUNNING, DONE, FAILED, CANCELLED, STOPPING
    }

    private final String runId;
    private final String testType;
    private final EventKind kind;
    private final String timestamp;
    private final String detail;

    public TestLifecycleEvent(String runId, String testType, EventKind kind, String detail) {
        this.runId = runId;
        this.testType = testType;
        this.kind = kind;
        this.timestamp = Instant.now().toString();
        this.detail = detail;
    }

    public TestLifecycleEvent(String runId, String testType, EventKind kind) {
        this(runId, testType, kind, null);
    }

    public String getRunId() { return runId; }
    public String getTestType() { return testType; }
    public EventKind getKind() { return kind; }
    public String getTimestamp() { return timestamp; }
    public String getDetail() { return detail; }

    public String toJson() {
        var sb = new StringBuilder("{");
        sb.append("\"runId\":\"").append(runId).append("\"");
        sb.append(",\"testType\":\"").append(testType).append("\"");
        sb.append(",\"event\":\"").append(kind.name()).append("\"");
        sb.append(",\"timestamp\":\"").append(timestamp).append("\"");
        if (detail != null) {
            sb.append(",\"detail\":\"").append(detail.replace("\"", "\\\"")).append("\"");
        }
        sb.append("}");
        return sb.toString();
    }
}
