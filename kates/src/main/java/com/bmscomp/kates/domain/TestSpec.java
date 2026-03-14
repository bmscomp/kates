package com.bmscomp.kates.domain;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestSpec {

    private String topic;
    private Integer numRecords;
    private Integer recordSize;
    private Integer throughput;
    private String acks;
    private Integer batchSize;
    private Integer lingerMs;
    private String compressionType;
    private Integer numProducers;
    private Integer numConsumers;
    private Long durationMs;
    private Integer replicationFactor;
    private Integer partitions;
    private Integer minInsyncReplicas;
    private String consumerGroup;
    private Integer targetThroughput;
    private Integer fetchMinBytes;
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
