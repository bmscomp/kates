package com.klster.kates.trogdor.spec;

import java.util.HashMap;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class RoundTripWorkloadSpec extends TrogdorSpec {

    private static final String CLASS_NAME = "org.apache.kafka.trogdor.workload.RoundTripWorkloadSpec";

    private String bootstrapServers;
    private int targetMessagesPerSec;
    private long maxMessages;
    private Map<String, String> producerConf;
    private Map<String, String> consumerConf;
    private Map<String, ProduceBenchSpec.TopicSpec> activeTopics;
    private int valueSize;

    public RoundTripWorkloadSpec(long durationMs) {
        super(CLASS_NAME, durationMs);
        this.producerConf = new HashMap<>();
        this.consumerConf = new HashMap<>();
        this.activeTopics = new HashMap<>();
    }

    public static RoundTripWorkloadSpec create(
            String bootstrapServers,
            String topicName,
            int partitions,
            int targetMessagesPerSec,
            long maxMessages,
            long durationMs,
            int valueSize) {

        RoundTripWorkloadSpec spec = new RoundTripWorkloadSpec(durationMs);
        spec.setBootstrapServers(bootstrapServers);
        spec.setTargetMessagesPerSec(targetMessagesPerSec);
        spec.setMaxMessages(maxMessages);
        spec.setValueSize(valueSize);

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

    public int getTargetMessagesPerSec() {
        return targetMessagesPerSec;
    }

    public void setTargetMessagesPerSec(int targetMessagesPerSec) {
        this.targetMessagesPerSec = targetMessagesPerSec;
    }

    public long getMaxMessages() {
        return maxMessages;
    }

    public void setMaxMessages(long maxMessages) {
        this.maxMessages = maxMessages;
    }

    public Map<String, String> getProducerConf() {
        return producerConf;
    }

    public void setProducerConf(Map<String, String> producerConf) {
        this.producerConf = producerConf;
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

    public int getValueSize() {
        return valueSize;
    }

    public void setValueSize(int valueSize) {
        this.valueSize = valueSize;
    }
}
