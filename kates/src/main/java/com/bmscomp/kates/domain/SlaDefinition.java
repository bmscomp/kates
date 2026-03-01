package com.bmscomp.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Declarative pass/fail criteria for a test scenario or individual phase.
 * After execution, each metric is compared against the threshold; any breach
 * results in an {@link SlaViolation}.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class SlaDefinition {

    private Double maxP99LatencyMs;
    private Double maxP999LatencyMs;
    private Double maxAvgLatencyMs;
    private Double minThroughputRecPerSec;
    private Double maxErrorRate;
    private Long minRecordsProcessed;
    private Double maxDataLossPercent;
    private Long maxRtoMs;
    private Long maxRpoMs;

    public SlaDefinition() {}

    public Double getMaxP99LatencyMs() {
        return maxP99LatencyMs;
    }

    public void setMaxP99LatencyMs(Double maxP99LatencyMs) {
        this.maxP99LatencyMs = maxP99LatencyMs;
    }

    public Double getMaxP999LatencyMs() {
        return maxP999LatencyMs;
    }

    public void setMaxP999LatencyMs(Double maxP999LatencyMs) {
        this.maxP999LatencyMs = maxP999LatencyMs;
    }

    public Double getMaxAvgLatencyMs() {
        return maxAvgLatencyMs;
    }

    public void setMaxAvgLatencyMs(Double maxAvgLatencyMs) {
        this.maxAvgLatencyMs = maxAvgLatencyMs;
    }

    public Double getMinThroughputRecPerSec() {
        return minThroughputRecPerSec;
    }

    public void setMinThroughputRecPerSec(Double minThroughputRecPerSec) {
        this.minThroughputRecPerSec = minThroughputRecPerSec;
    }

    public Double getMaxErrorRate() {
        return maxErrorRate;
    }

    public void setMaxErrorRate(Double maxErrorRate) {
        this.maxErrorRate = maxErrorRate;
    }

    public Long getMinRecordsProcessed() {
        return minRecordsProcessed;
    }

    public void setMinRecordsProcessed(Long minRecordsProcessed) {
        this.minRecordsProcessed = minRecordsProcessed;
    }

    public Double getMaxDataLossPercent() {
        return maxDataLossPercent;
    }

    public void setMaxDataLossPercent(Double maxDataLossPercent) {
        this.maxDataLossPercent = maxDataLossPercent;
    }

    public Long getMaxRtoMs() {
        return maxRtoMs;
    }

    public void setMaxRtoMs(Long maxRtoMs) {
        this.maxRtoMs = maxRtoMs;
    }

    public Long getMaxRpoMs() {
        return maxRpoMs;
    }

    public void setMaxRpoMs(Long maxRpoMs) {
        this.maxRpoMs = maxRpoMs;
    }

    /**
     * Returns true if any threshold is defined.
     */
    public boolean hasConstraints() {
        return maxP99LatencyMs != null
                || maxP999LatencyMs != null
                || maxAvgLatencyMs != null
                || minThroughputRecPerSec != null
                || maxErrorRate != null
                || minRecordsProcessed != null
                || maxDataLossPercent != null
                || maxRtoMs != null
                || maxRpoMs != null;
    }
}
