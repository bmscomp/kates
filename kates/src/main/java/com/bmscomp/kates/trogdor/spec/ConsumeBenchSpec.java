package com.bmscomp.kates.trogdor.spec;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class ConsumeBenchSpec extends TrogdorSpec {

    public static final String CLASS_NAME = "org.apache.kafka.trogdor.workload.ConsumeBenchSpec";

    /** The agent that runs the task; Trogdor's default, "", names none. */
    private String consumerNode;

    private String bootstrapServers;
    private long maxMessages;
    private Map<String, String> consumerConf;

    /**
     * Topic names, a list in Trogdor, where produce and round-trip specs have a
     * map. A bare name subscribes through {@code consumerGroup}, so several
     * consumers of one run share the partitions as the native backend's do; a
     * {@code topic:partition} entry would assign that partition to every one
     * of them. This used to be the produce spec's map, which the coordinator
     * refused.
     */
    private List<String> activeTopics;

    private String consumerGroup;
    private int threadsPerWorker;

    public ConsumeBenchSpec(long durationMs) {
        super(CLASS_NAME, durationMs);
        this.consumerConf = new HashMap<>();
        this.activeTopics = new ArrayList<>();
        this.threadsPerWorker = 1;
    }

    public static ConsumeBenchSpec create(
            String bootstrapServers, String topicName, long maxMessages, long durationMs, String consumerGroup) {

        ConsumeBenchSpec spec = new ConsumeBenchSpec(durationMs);
        spec.setBootstrapServers(bootstrapServers);
        spec.setMaxMessages(maxMessages);
        spec.setConsumerGroup(consumerGroup);
        spec.getActiveTopics().add(topicName);

        return spec;
    }

    public String getConsumerNode() {
        return consumerNode;
    }

    public void setConsumerNode(String consumerNode) {
        this.consumerNode = consumerNode;
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

    public List<String> getActiveTopics() {
        return activeTopics;
    }

    public void setActiveTopics(List<String> activeTopics) {
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
