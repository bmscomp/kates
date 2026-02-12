package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestSpec {

    private String topic;
    private int numRecords = 1_000_000;
    private int recordSize = 1024;
    private int throughput = -1;
    private String acks = "all";
    private int batchSize = 65536;
    private int lingerMs = 5;
    private String compressionType = "lz4";
    private int numProducers = 1;
    private int numConsumers = 1;
    private long durationMs = 600_000;
    private int replicationFactor = 3;
    private int partitions = 3;
    private int minInsyncReplicas = 2;

    public TestSpec() {
    }

    public String getTopic() {
        return topic;
    }

    public void setTopic(String topic) {
        this.topic = topic;
    }

    public int getNumRecords() {
        return numRecords;
    }

    public void setNumRecords(int numRecords) {
        this.numRecords = numRecords;
    }

    public int getRecordSize() {
        return recordSize;
    }

    public void setRecordSize(int recordSize) {
        this.recordSize = recordSize;
    }

    public int getThroughput() {
        return throughput;
    }

    public void setThroughput(int throughput) {
        this.throughput = throughput;
    }

    public String getAcks() {
        return acks;
    }

    public void setAcks(String acks) {
        this.acks = acks;
    }

    public int getBatchSize() {
        return batchSize;
    }

    public void setBatchSize(int batchSize) {
        this.batchSize = batchSize;
    }

    public int getLingerMs() {
        return lingerMs;
    }

    public void setLingerMs(int lingerMs) {
        this.lingerMs = lingerMs;
    }

    public String getCompressionType() {
        return compressionType;
    }

    public void setCompressionType(String compressionType) {
        this.compressionType = compressionType;
    }

    public int getNumProducers() {
        return numProducers;
    }

    public void setNumProducers(int numProducers) {
        this.numProducers = numProducers;
    }

    public int getNumConsumers() {
        return numConsumers;
    }

    public void setNumConsumers(int numConsumers) {
        this.numConsumers = numConsumers;
    }

    public long getDurationMs() {
        return durationMs;
    }

    public void setDurationMs(long durationMs) {
        this.durationMs = durationMs;
    }

    public int getReplicationFactor() {
        return replicationFactor;
    }

    public void setReplicationFactor(int replicationFactor) {
        this.replicationFactor = replicationFactor;
    }

    public int getPartitions() {
        return partitions;
    }

    public void setPartitions(int partitions) {
        this.partitions = partitions;
    }

    public int getMinInsyncReplicas() {
        return minInsyncReplicas;
    }

    public void setMinInsyncReplicas(int minInsyncReplicas) {
        this.minInsyncReplicas = minInsyncReplicas;
    }
}
