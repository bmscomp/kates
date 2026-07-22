package com.bmscomp.kates.domain;

import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.Pattern;
import jakarta.validation.constraints.Size;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestSpec {

    // Nullable on purpose: null means "use the configured default for the test
    // type" (TestTypeDefaults). Constraints only fire on explicitly set values.
    @Pattern(regexp = "[a-zA-Z0-9._-]{1,249}", message = "topic must be a legal Kafka topic name")
    private String topic;

    @Min(1)
    @Max(1_000_000_000)
    private Integer numRecords;

    @Min(1)
    @Max(104_857_600) // 100 MiB
    private Integer recordSize;

    @Min(value = -1, message = "throughput must be -1 (unlimited) or positive")
    private Integer throughput;

    @Pattern(regexp = "all|-1|0|1", message = "acks must be one of: all, -1, 0, 1")
    private String acks;

    @Min(0)
    @Max(134_217_728) // 128 MiB
    private Integer batchSize;

    @Min(0)
    @Max(300_000)
    private Integer lingerMs;

    @Pattern(
            regexp = "none|gzip|snappy|lz4|zstd",
            message = "compressionType must be one of: none, gzip, snappy, lz4, zstd")
    private String compressionType;

    @Min(1)
    @Max(100)
    private Integer numProducers;

    @Min(0)
    @Max(100)
    private Integer numConsumers;

    @Min(1_000)
    @Max(86_400_000) // 24h
    private Long durationMs;

    @Min(1)
    @Max(10)
    private Integer replicationFactor;

    @Min(1)
    @Max(10_000)
    private Integer partitions;

    @Min(1)
    @Max(10)
    private Integer minInsyncReplicas;

    @Size(max = 255)
    private String consumerGroup;

    @Min(value = -1, message = "targetThroughput must be -1 (unlimited) or positive")
    private Integer targetThroughput;

    @Min(1)
    private Integer fetchMinBytes;

    @Min(0)
    @Max(300_000)
    private Integer fetchMaxWaitMs;

    private boolean enableIdempotence = false;
    private boolean enableTransactions = false;
    private boolean enableCrc = true;

    public TestSpec() {}

    public String getTopic() {
        return topic;
    }

    public void setTopic(String topic) {
        this.topic = topic;
    }

    public int getNumRecords() {
        return numRecords != null ? numRecords : 1_000_000;
    }

    public void setNumRecords(int numRecords) {
        this.numRecords = numRecords;
    }

    @JsonIgnore
    public boolean hasNumRecords() {
        return numRecords != null;
    }

    public int getRecordSize() {
        return recordSize != null ? recordSize : 1024;
    }

    public void setRecordSize(int recordSize) {
        this.recordSize = recordSize;
    }

    @JsonIgnore
    public boolean hasRecordSize() {
        return recordSize != null;
    }

    public int getThroughput() {
        return throughput != null ? throughput : -1;
    }

    public void setThroughput(int throughput) {
        this.throughput = throughput;
    }

    @JsonIgnore
    public boolean hasThroughput() {
        return throughput != null;
    }

    public String getAcks() {
        return acks != null ? acks : "all";
    }

    public void setAcks(String acks) {
        this.acks = acks;
    }

    @JsonIgnore
    public boolean hasAcks() {
        return acks != null;
    }

    public int getBatchSize() {
        return batchSize != null ? batchSize : 65536;
    }

    public void setBatchSize(int batchSize) {
        this.batchSize = batchSize;
    }

    @JsonIgnore
    public boolean hasBatchSize() {
        return batchSize != null;
    }

    public int getLingerMs() {
        return lingerMs != null ? lingerMs : 5;
    }

    public void setLingerMs(int lingerMs) {
        this.lingerMs = lingerMs;
    }

    @JsonIgnore
    public boolean hasLingerMs() {
        return lingerMs != null;
    }

    public String getCompressionType() {
        return compressionType != null ? compressionType : "lz4";
    }

    public void setCompressionType(String compressionType) {
        this.compressionType = compressionType;
    }

    @JsonIgnore
    public boolean hasCompressionType() {
        return compressionType != null;
    }

    public int getNumProducers() {
        return numProducers != null ? numProducers : 1;
    }

    public void setNumProducers(int numProducers) {
        this.numProducers = numProducers;
    }

    @JsonIgnore
    public boolean hasNumProducers() {
        return numProducers != null;
    }

    public int getNumConsumers() {
        return numConsumers != null ? numConsumers : 1;
    }

    public void setNumConsumers(int numConsumers) {
        this.numConsumers = numConsumers;
    }

    @JsonIgnore
    public boolean hasNumConsumers() {
        return numConsumers != null;
    }

    public long getDurationMs() {
        return durationMs != null ? durationMs : 600_000L;
    }

    public void setDurationMs(long durationMs) {
        this.durationMs = durationMs;
    }

    @JsonIgnore
    public boolean hasDurationMs() {
        return durationMs != null;
    }

    public int getReplicationFactor() {
        return replicationFactor != null ? replicationFactor : 3;
    }

    public void setReplicationFactor(int replicationFactor) {
        this.replicationFactor = replicationFactor;
    }

    @JsonIgnore
    public boolean hasReplicationFactor() {
        return replicationFactor != null;
    }

    public int getPartitions() {
        return partitions != null ? partitions : 3;
    }

    public void setPartitions(int partitions) {
        this.partitions = partitions;
    }

    @JsonIgnore
    public boolean hasPartitions() {
        return partitions != null;
    }

    public int getMinInsyncReplicas() {
        return minInsyncReplicas != null ? minInsyncReplicas : 2;
    }

    public void setMinInsyncReplicas(int minInsyncReplicas) {
        this.minInsyncReplicas = minInsyncReplicas;
    }

    @JsonIgnore
    public boolean hasMinInsyncReplicas() {
        return minInsyncReplicas != null;
    }

    public String getConsumerGroup() {
        return consumerGroup;
    }

    public void setConsumerGroup(String consumerGroup) {
        this.consumerGroup = consumerGroup;
    }

    public int getTargetThroughput() {
        return targetThroughput != null ? targetThroughput : -1;
    }

    public void setTargetThroughput(int targetThroughput) {
        this.targetThroughput = targetThroughput;
    }

    @JsonIgnore
    public boolean hasTargetThroughput() {
        return targetThroughput != null;
    }

    public int getFetchMinBytes() {
        return fetchMinBytes != null ? fetchMinBytes : 1;
    }

    public void setFetchMinBytes(int fetchMinBytes) {
        this.fetchMinBytes = fetchMinBytes;
    }

    @JsonIgnore
    public boolean hasFetchMinBytes() {
        return fetchMinBytes != null;
    }

    public int getFetchMaxWaitMs() {
        return fetchMaxWaitMs != null ? fetchMaxWaitMs : 500;
    }

    public void setFetchMaxWaitMs(int fetchMaxWaitMs) {
        this.fetchMaxWaitMs = fetchMaxWaitMs;
    }

    @JsonIgnore
    public boolean hasFetchMaxWaitMs() {
        return fetchMaxWaitMs != null;
    }

    public boolean isEnableIdempotence() {
        return enableIdempotence;
    }

    public void setEnableIdempotence(boolean enableIdempotence) {
        this.enableIdempotence = enableIdempotence;
    }

    public boolean isEnableTransactions() {
        return enableTransactions;
    }

    public void setEnableTransactions(boolean enableTransactions) {
        this.enableTransactions = enableTransactions;
    }

    public boolean isEnableCrc() {
        return enableCrc;
    }

    public void setEnableCrc(boolean enableCrc) {
        this.enableCrc = enableCrc;
    }
}
