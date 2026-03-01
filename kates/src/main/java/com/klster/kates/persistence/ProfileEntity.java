package com.klster.kates.persistence;

import java.time.Instant;
import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.GeneratedValue;
import jakarta.persistence.GenerationType;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "profiles")
public class ProfileEntity {

    @Id
    @GeneratedValue(strategy = GenerationType.IDENTITY)
    private Long id;

    @Column(nullable = false, unique = true, length = 128)
    private String name;

    @Column(name = "test_type", nullable = false, length = 32)
    private String testType;

    @Column(name = "run_id", length = 64)
    private String runId;

    private Double throughput;

    @Column(name = "p50_ms")
    private Double p50Ms;

    @Column(name = "p95_ms")
    private Double p95Ms;

    @Column(name = "p99_ms")
    private Double p99Ms;

    @Column(name = "avg_ms")
    private Double avgMs;

    @Column(name = "error_rate")
    private Double errorRate;

    private Double records;
    private Integer brokers;
    private Integer partitions;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt = Instant.now();

    public ProfileEntity() {}

    public ProfileEntity(String name, String testType, String runId) {
        this.name = name;
        this.testType = testType;
        this.runId = runId;
    }

    public Long getId() {
        return id;
    }

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getTestType() {
        return testType;
    }

    public void setTestType(String testType) {
        this.testType = testType;
    }

    public String getRunId() {
        return runId;
    }

    public void setRunId(String runId) {
        this.runId = runId;
    }

    public Double getThroughput() {
        return throughput;
    }

    public void setThroughput(Double throughput) {
        this.throughput = throughput;
    }

    public Double getP50Ms() {
        return p50Ms;
    }

    public void setP50Ms(Double p50Ms) {
        this.p50Ms = p50Ms;
    }

    public Double getP95Ms() {
        return p95Ms;
    }

    public void setP95Ms(Double p95Ms) {
        this.p95Ms = p95Ms;
    }

    public Double getP99Ms() {
        return p99Ms;
    }

    public void setP99Ms(Double p99Ms) {
        this.p99Ms = p99Ms;
    }

    public Double getAvgMs() {
        return avgMs;
    }

    public void setAvgMs(Double avgMs) {
        this.avgMs = avgMs;
    }

    public Double getErrorRate() {
        return errorRate;
    }

    public void setErrorRate(Double errorRate) {
        this.errorRate = errorRate;
    }

    public Double getRecords() {
        return records;
    }

    public void setRecords(Double records) {
        this.records = records;
    }

    public Integer getBrokers() {
        return brokers;
    }

    public void setBrokers(Integer brokers) {
        this.brokers = brokers;
    }

    public Integer getPartitions() {
        return partitions;
    }

    public void setPartitions(Integer partitions) {
        this.partitions = partitions;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }
}
