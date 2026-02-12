package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestResult {

    private String taskId;
    private TestType testType;
    private TaskStatus status;
    private long recordsSent;
    private double throughputRecordsPerSec;
    private double throughputMBPerSec;
    private double avgLatencyMs;
    private double p50LatencyMs;
    private double p95LatencyMs;
    private double p99LatencyMs;
    private double maxLatencyMs;
    private String startTime;
    private String endTime;
    private String error;

    public enum TaskStatus {
        PENDING, RUNNING, STOPPING, DONE, FAILED
    }

    public TestResult() {
    }

    public String getTaskId() {
        return taskId;
    }

    public void setTaskId(String taskId) {
        this.taskId = taskId;
    }

    public TestType getTestType() {
        return testType;
    }

    public void setTestType(TestType testType) {
        this.testType = testType;
    }

    public TaskStatus getStatus() {
        return status;
    }

    public void setStatus(TaskStatus status) {
        this.status = status;
    }

    public long getRecordsSent() {
        return recordsSent;
    }

    public void setRecordsSent(long recordsSent) {
        this.recordsSent = recordsSent;
    }

    public double getThroughputRecordsPerSec() {
        return throughputRecordsPerSec;
    }

    public void setThroughputRecordsPerSec(double throughputRecordsPerSec) {
        this.throughputRecordsPerSec = throughputRecordsPerSec;
    }

    public double getThroughputMBPerSec() {
        return throughputMBPerSec;
    }

    public void setThroughputMBPerSec(double throughputMBPerSec) {
        this.throughputMBPerSec = throughputMBPerSec;
    }

    public double getAvgLatencyMs() {
        return avgLatencyMs;
    }

    public void setAvgLatencyMs(double avgLatencyMs) {
        this.avgLatencyMs = avgLatencyMs;
    }

    public double getP50LatencyMs() {
        return p50LatencyMs;
    }

    public void setP50LatencyMs(double p50LatencyMs) {
        this.p50LatencyMs = p50LatencyMs;
    }

    public double getP95LatencyMs() {
        return p95LatencyMs;
    }

    public void setP95LatencyMs(double p95LatencyMs) {
        this.p95LatencyMs = p95LatencyMs;
    }

    public double getP99LatencyMs() {
        return p99LatencyMs;
    }

    public void setP99LatencyMs(double p99LatencyMs) {
        this.p99LatencyMs = p99LatencyMs;
    }

    public double getMaxLatencyMs() {
        return maxLatencyMs;
    }

    public void setMaxLatencyMs(double maxLatencyMs) {
        this.maxLatencyMs = maxLatencyMs;
    }

    public String getStartTime() {
        return startTime;
    }

    public void setStartTime(String startTime) {
        this.startTime = startTime;
    }

    public String getEndTime() {
        return endTime;
    }

    public void setEndTime(String endTime) {
        this.endTime = endTime;
    }

    public String getError() {
        return error;
    }

    public void setError(String error) {
        this.error = error;
    }
}
