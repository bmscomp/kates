package com.klster.kates.trogdor.spec;

import java.util.HashMap;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class ConsumeBenchSpec extends TrogdorSpec {

    private static final String CLASS_NAME = "org.apache.kafka.trogdor.workload.ConsumeBenchSpec";

    private String bootstrapServers;
    private long maxMessages;
    private Map<String, String> consumerConf;
    private Map<String, ProduceBenchSpec.TopicSpec> activeTopics;
    private String consumerGroup;
    private int threadsPerWorker;

    public ConsumeBenchSpec(long durationMs) {
        super(CLASS_NAME, durationMs);
        this.consumerConf = new HashMap<>();
        this.activeTopics = new HashMap<>();
        this.threadsPerWorker = 1;
    }

    public static ConsumeBenchSpec create(
            String bootstrapServers,
            String topicName,
            int partitions,
            long maxMessages,
            long durationMs,
            String consumerGroup) {

        ConsumeBenchSpec spec = new ConsumeBenchSpec(durationMs);
        spec.setBootstrapServers(bootstrapServers);
        spec.setMaxMessages(maxMessages);
        spec.setConsumerGroup(consumerGroup);

        ProduceBenchSpec.TopicSpec topicSpec = new ProduceBenchSpec.TopicSpec();
        topicSpec.setNumPartitions(partitions);
        topicSpec.setReplicationFactor((short) 3);
        spec.getActiveTopics().put(topicName + "[0-" + (partitions - 1) + "]", topicSpec);

        return spec;
    }

    public String getBootstrapServers() {
        return bootstrapServers;
    }

    public void setBootstrapServers(String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    public long getMaxMessages() {
        return maxMessages;
    }

    public void setMaxMessages(long maxMessages) {
        this.maxMessages = maxMessages;
    }

    public Map<String, String> getConsumerConf() {
        return consumerConf;
    }

    public void setConsumerConf(Map<String, String> consumerConf) {
        this.consumerConf = consumerConf;
    }

    public Map<String, ProduceBenchSpec.TopicSpec> getActiveTopics() {
        return activeTopics;
    }

    public void setActiveTopics(Map<String, ProduceBenchSpec.TopicSpec> activeTopics) {
        this.activeTopics = activeTopics;
    }

    public String getConsumerGroup() {
        return consumerGroup;
    }

    public void setConsumerGroup(String consumerGroup) {
        this.consumerGroup = consumerGroup;
    }

    public int getThreadsPerWorker() {
        return threadsPerWorker;
    }

    public void setThreadsPerWorker(int threadsPerWorker) {
        this.threadsPerWorker = threadsPerWorker;
    }
}
