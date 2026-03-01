package com.bmscomp.kates.trogdor.spec;

import java.util.HashMap;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class ProduceBenchSpec extends TrogdorSpec {

    private static final String CLASS_NAME = "org.apache.kafka.trogdor.workload.ProduceBenchSpec";

    private String bootstrapServers;
    private int targetMessagesPerSec;
    private long maxMessages;
    private Map<String, String> producerConf;
    private Map<String, TopicSpec> activeTopics;
    private int totalProducers;
    private KeyGeneratorSpec keyGenerator;
    private ValueGeneratorSpec valueGenerator;

    public ProduceBenchSpec(long durationMs) {
        super(CLASS_NAME, durationMs);
        this.producerConf = new HashMap<>();
        this.activeTopics = new HashMap<>();
        this.totalProducers = 1;
    }

    public static ProduceBenchSpec create(
            String bootstrapServers,
            String topicName,
            int partitions,
            int targetMessagesPerSec,
            long maxMessages,
            long durationMs,
            int recordSize) {

        ProduceBenchSpec spec = new ProduceBenchSpec(durationMs);
        spec.setBootstrapServers(bootstrapServers);
        spec.setTargetMessagesPerSec(targetMessagesPerSec);
        spec.setMaxMessages(maxMessages);

        TopicSpec topicSpec = new TopicSpec();
        topicSpec.setNumPartitions(partitions);
        topicSpec.setReplicationFactor((short) 3);
        spec.getActiveTopics().put(topicName + "[0-" + (partitions - 1) + "]", topicSpec);

        ValueGeneratorSpec valGen = new ValueGeneratorSpec();
        valGen.setSize(recordSize);
        spec.setValueGenerator(valGen);

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

    public Map<String, TopicSpec> getActiveTopics() {
        return activeTopics;
    }

    public void setActiveTopics(Map<String, TopicSpec> activeTopics) {
        this.activeTopics = activeTopics;
    }

    public int getTotalProducers() {
        return totalProducers;
    }

    public void setTotalProducers(int totalProducers) {
        this.totalProducers = totalProducers;
    }

    public KeyGeneratorSpec getKeyGenerator() {
        return keyGenerator;
    }

    public void setKeyGenerator(KeyGeneratorSpec keyGenerator) {
        this.keyGenerator = keyGenerator;
    }

    public ValueGeneratorSpec getValueGenerator() {
        return valueGenerator;
    }

    public void setValueGenerator(ValueGeneratorSpec valueGenerator) {
        this.valueGenerator = valueGenerator;
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class TopicSpec {
        private int numPartitions;
        private short replicationFactor;

        public int getNumPartitions() {
            return numPartitions;
        }

        public void setNumPartitions(int numPartitions) {
            this.numPartitions = numPartitions;
        }

        public short getReplicationFactor() {
            return replicationFactor;
        }

        public void setReplicationFactor(short replicationFactor) {
            this.replicationFactor = replicationFactor;
        }
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class KeyGeneratorSpec {
        private int size = 4;

        public int getSize() {
            return size;
        }

        public void setSize(int size) {
            this.size = size;
        }
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class ValueGeneratorSpec {
        private int size = 1024;

        public int getSize() {
            return size;
        }

        public void setSize(int size) {
            this.size = size;
        }
    }
}
