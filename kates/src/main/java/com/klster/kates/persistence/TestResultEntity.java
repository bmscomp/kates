package com.klster.kates.persistence;

import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestType;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.FetchType;
import jakarta.persistence.GeneratedValue;
import jakarta.persistence.GenerationType;
import jakarta.persistence.Id;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.Table;

@Entity
@Table(name = "test_results")
public class TestResultEntity {

    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    private Long id;

    @Column(name = "task_id", length = 128)
    private String taskId;

    @Enumerated(EnumType.STRING)
    @Column(name = "test_type", length = 32)
    private TestType testType;

    @Enumerated(EnumType.STRING)
    @Column(length = 16)
    private TestResult.TaskStatus status;

    @Column(name = "records_sent")
    private long recordsSent;

    @Column(name = "throughput_rec_per_sec")
    private double throughputRecordsPerSec;

    @Column(name = "throughput_mb_per_sec")
    private double throughputMBPerSec;

    @Column(name = "avg_latency_ms")
    private double avgLatencyMs;

    @Column(name = "p50_latency_ms")
    private double p50LatencyMs;

    @Column(name = "p95_latency_ms")
    private double p95LatencyMs;

    @Column(name = "p99_latency_ms")
    private double p99LatencyMs;

    @Column(name = "max_latency_ms")
    private double maxLatencyMs;

    @Column(name = "start_time", length = 64)
    private String startTime;

    @Column(name = "end_time", length = 64)
    private String endTime;

    @Column(columnDefinition = "TEXT")
    private String error;

    @Column(name = "phase_name", length = 128)
    private String phaseName;

    @ManyToOne(fetch = FetchType.LAZY)
    @JoinColumn(name = "test_run_id", nullable = false)
    private TestRunEntity testRun;

    public TestResultEntity() {
    }

    public Long getId() { return id; }
    public void setId(Long id) { this.id = id; }

    public String getTaskId() { return taskId; }
    public void setTaskId(String taskId) { this.taskId = taskId; }

    public TestType getTestType() { return testType; }
    public void setTestType(TestType testType) { this.testType = testType; }

    public TestResult.TaskStatus getStatus() { return status; }
    public void setStatus(TestResult.TaskStatus status) { this.status = status; }

    public long getRecordsSent() { return recordsSent; }
    public void setRecordsSent(long recordsSent) { this.recordsSent = recordsSent; }

    public double getThroughputRecordsPerSec() { return throughputRecordsPerSec; }
    public void setThroughputRecordsPerSec(double v) { this.throughputRecordsPerSec = v; }

    public double getThroughputMBPerSec() { return throughputMBPerSec; }
    public void setThroughputMBPerSec(double v) { this.throughputMBPerSec = v; }

    public double getAvgLatencyMs() { return avgLatencyMs; }
    public void setAvgLatencyMs(double v) { this.avgLatencyMs = v; }

    public double getP50LatencyMs() { return p50LatencyMs; }
    public void setP50LatencyMs(double v) { this.p50LatencyMs = v; }

    public double getP95LatencyMs() { return p95LatencyMs; }
    public void setP95LatencyMs(double v) { this.p95LatencyMs = v; }

    public double getP99LatencyMs() { return p99LatencyMs; }
    public void setP99LatencyMs(double v) { this.p99LatencyMs = v; }

    public double getMaxLatencyMs() { return maxLatencyMs; }
    public void setMaxLatencyMs(double v) { this.maxLatencyMs = v; }

    public String getStartTime() { return startTime; }
    public void setStartTime(String startTime) { this.startTime = startTime; }

    public String getEndTime() { return endTime; }
    public void setEndTime(String endTime) { this.endTime = endTime; }

    public String getError() { return error; }
    public void setError(String error) { this.error = error; }

    public String getPhaseName() { return phaseName; }
    public void setPhaseName(String phaseName) { this.phaseName = phaseName; }

    public TestRunEntity getTestRun() { return testRun; }
    public void setTestRun(TestRunEntity testRun) { this.testRun = testRun; }
}
