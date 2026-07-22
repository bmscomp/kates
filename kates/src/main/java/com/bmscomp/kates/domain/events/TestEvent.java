package com.bmscomp.kates.domain.events;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonProperty;

import com.bmscomp.kates.domain.TestResult.TaskStatus;

public class TestEvent {
    private String testId;
    private String testType;
    private TaskStatus status;
    private String message;
    private long timestamp;

    public TestEvent() {}

    @JsonCreator
    public TestEvent(
            @JsonProperty("testId") String testId,
            @JsonProperty("testType") String testType,
            @JsonProperty("status") TaskStatus status,
            @JsonProperty("message") String message,
            @JsonProperty("timestamp") long timestamp) {
        this.testId = testId;
        this.testType = testType;
        this.status = status;
        this.message = message;
        this.timestamp = timestamp;
    }

    public String getTestId() {
        return testId;
    }

    public String getTestType() {
        return testType;
    }

    public TaskStatus getStatus() {
        return status;
    }

    public String getMessage() {
        return message;
    }

    public long getTimestamp() {
        return timestamp;
    }
}
