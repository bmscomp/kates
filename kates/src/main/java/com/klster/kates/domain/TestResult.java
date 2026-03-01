package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestResult {

    private final String taskId;
    private final TestType testType;
    private final TaskStatus status;
    private final long recordsSent;
    private final double throughputRecordsPerSec;
    private final double throughputMBPerSec;
    private final double avgLatencyMs;
    private final double p50LatencyMs;
    private final double p95LatencyMs;
    private final double p99LatencyMs;
    private final double maxLatencyMs;
    private final String startTime;
    private final String endTime;
    private final String error;
    private final String phaseName;
    private final IntegrityResult integrity;

    public enum TaskStatus {
        PENDING,
        RUNNING,
        STOPPING,
        DONE,
        FAILED
    }

    public TestResult() {
        this(null, null, TaskStatus.PENDING, 0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, null, null, null, null, null);
    }

    private TestResult(String taskId, TestType testType, TaskStatus status, long recordsSent, double throughputRecordsPerSec, double throughputMBPerSec, double avgLatencyMs, double p50LatencyMs, double p95LatencyMs, double p99LatencyMs, double maxLatencyMs, String startTime, String endTime, String error, String phaseName, IntegrityResult integrity) {
        this.taskId = taskId;
        this.testType = testType;
        this.status = status;
        this.recordsSent = recordsSent;
        this.throughputRecordsPerSec = throughputRecordsPerSec;
        this.throughputMBPerSec = throughputMBPerSec;
        this.avgLatencyMs = avgLatencyMs;
        this.p50LatencyMs = p50LatencyMs;
        this.p95LatencyMs = p95LatencyMs;
        this.p99LatencyMs = p99LatencyMs;
        this.maxLatencyMs = maxLatencyMs;
        this.startTime = startTime;
        this.endTime = endTime;
        this.error = error;
        this.phaseName = phaseName;
        this.integrity = integrity;
    }

    public TestResult withTaskId(String taskId) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withTestType(TestType testType) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withStatus(TaskStatus status) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withRecordsSent(long recordsSent) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withThroughputRecordsPerSec(double throughputRecordsPerSec) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withThroughputMBPerSec(double throughputMBPerSec) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withAvgLatencyMs(double avgLatencyMs) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withP50LatencyMs(double p50LatencyMs) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withP95LatencyMs(double p95LatencyMs) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withP99LatencyMs(double p99LatencyMs) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withMaxLatencyMs(double maxLatencyMs) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withStartTime(String startTime) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withEndTime(String endTime) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withError(String error) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withPhaseName(String phaseName) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }
    public TestResult withIntegrity(IntegrityResult integrity) { return new TestResult(taskId, testType, status, recordsSent, throughputRecordsPerSec, throughputMBPerSec, avgLatencyMs, p50LatencyMs, p95LatencyMs, p99LatencyMs, maxLatencyMs, startTime, endTime, error, phaseName, integrity); }

    public String getTaskId() { return taskId; }
    public TestType getTestType() { return testType; }
    public TaskStatus getStatus() { return status; }
    public long getRecordsSent() { return recordsSent; }
    public double getThroughputRecordsPerSec() { return throughputRecordsPerSec; }
    public double getThroughputMBPerSec() { return throughputMBPerSec; }
    public double getAvgLatencyMs() { return avgLatencyMs; }
    public double getP50LatencyMs() { return p50LatencyMs; }
    public double getP95LatencyMs() { return p95LatencyMs; }
    public double getP99LatencyMs() { return p99LatencyMs; }
    public double getMaxLatencyMs() { return maxLatencyMs; }
    public String getStartTime() { return startTime; }
    public String getEndTime() { return endTime; }
    public String getError() { return error; }
    public String getPhaseName() { return phaseName; }
    public IntegrityResult getIntegrity() { return integrity; }
}
