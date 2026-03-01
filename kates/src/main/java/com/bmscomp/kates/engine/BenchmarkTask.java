package com.bmscomp.kates.engine;

import java.util.Map;

/**
 * Backend-agnostic descriptor for a benchmark workload.
 * Each phase of a scenario produces one BenchmarkTask.
 */
public class BenchmarkTask {

    public enum WorkloadType {
        PRODUCE,
        CONSUME,
        ROUND_TRIP,
        INTEGRITY
    }

    private final String taskId;
    private final WorkloadType workloadType;
    private final String topic;
    private final int partitions;
    private final int targetMessagesPerSec;
    private final long maxMessages;
    private final long durationMs;
    private final int recordSize;
    private final int concurrency;
    private final String consumerGroup;
    private final Map<String, String> producerConfig;
    private final Map<String, String> consumerConfig;
    private final boolean enableIdempotence;
    private final boolean enableTransactions;
    private final boolean enableCrc;

    private BenchmarkTask(Builder builder) {
        this.taskId = builder.taskId;
        this.workloadType = builder.workloadType;
        this.topic = builder.topic;
        this.partitions = builder.partitions;
        this.targetMessagesPerSec = builder.targetMessagesPerSec;
        this.maxMessages = builder.maxMessages;
        this.durationMs = builder.durationMs;
        this.recordSize = builder.recordSize;
        this.concurrency = builder.concurrency;
        this.consumerGroup = builder.consumerGroup;
        this.producerConfig = Map.copyOf(builder.producerConfig);
        this.consumerConfig = Map.copyOf(builder.consumerConfig);
        this.enableIdempotence = builder.enableIdempotence;
        this.enableTransactions = builder.enableTransactions;
        this.enableCrc = builder.enableCrc;
    }

    public String getTaskId() {
        return taskId;
    }

    public WorkloadType getWorkloadType() {
        return workloadType;
    }

    public String getTopic() {
        return topic;
    }

    public int getPartitions() {
        return partitions;
    }

    public int getTargetMessagesPerSec() {
        return targetMessagesPerSec;
    }

    public long getMaxMessages() {
        return maxMessages;
    }

    public long getDurationMs() {
        return durationMs;
    }

    public int getRecordSize() {
        return recordSize;
    }

    public int getConcurrency() {
        return concurrency;
    }

    public String getConsumerGroup() {
        return consumerGroup;
    }

    public Map<String, String> getProducerConfig() {
        return producerConfig;
    }

    public Map<String, String> getConsumerConfig() {
        return consumerConfig;
    }

    public boolean isEnableIdempotence() {
        return enableIdempotence;
    }

    public boolean isEnableTransactions() {
        return enableTransactions;
    }

    public boolean isEnableCrc() {
        return enableCrc;
    }

    public static Builder builder(String taskId, WorkloadType type) {
        return new Builder(taskId, type);
    }

    public static class Builder {
        private final String taskId;
        private final WorkloadType workloadType;
        private String topic = "benchmark-test";
        private int partitions = 3;
        private int targetMessagesPerSec = -1;
        private long maxMessages = 1_000_000;
        private long durationMs = 600_000;
        private int recordSize = 1024;
        private int concurrency = 1;
        private String consumerGroup = "benchmark-group";
        private Map<String, String> producerConfig = Map.of();
        private Map<String, String> consumerConfig = Map.of();
        private boolean enableIdempotence = false;
        private boolean enableTransactions = false;
        private boolean enableCrc = true;

        private Builder(String taskId, WorkloadType workloadType) {
            this.taskId = taskId;
            this.workloadType = workloadType;
        }

        public Builder topic(String topic) {
            this.topic = topic;
            return this;
        }

        public Builder partitions(int p) {
            this.partitions = p;
            return this;
        }

        public Builder targetMessagesPerSec(int t) {
            this.targetMessagesPerSec = t;
            return this;
        }

        public Builder maxMessages(long m) {
            this.maxMessages = m;
            return this;
        }

        public Builder durationMs(long d) {
            this.durationMs = d;
            return this;
        }

        public Builder recordSize(int s) {
            this.recordSize = s;
            return this;
        }

        public Builder concurrency(int c) {
            this.concurrency = c;
            return this;
        }

        public Builder consumerGroup(String g) {
            this.consumerGroup = g;
            return this;
        }

        public Builder producerConfig(Map<String, String> c) {
            this.producerConfig = c;
            return this;
        }

        public Builder consumerConfig(Map<String, String> c) {
            this.consumerConfig = c;
            return this;
        }

        public Builder enableIdempotence(boolean b) {
            this.enableIdempotence = b;
            return this;
        }

        public Builder enableTransactions(boolean b) {
            this.enableTransactions = b;
            return this;
        }

        public Builder enableCrc(boolean b) {
            this.enableCrc = b;
            return this;
        }

        public BenchmarkTask build() {
            return new BenchmarkTask(this);
        }
    }
}
