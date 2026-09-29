package com.bmscomp.kates.trogdor.spec;

import java.util.HashMap;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Trogdor's {@code ProduceBenchSpec}. The coordinator reads it with unknown
 * properties refused, so every field here has to be one Trogdor's class
 * declares; {@code totalProducers} was not, and every produce task was
 * refused at creation.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class ProduceBenchSpec extends TrogdorSpec {

    public static final String CLASS_NAME = "org.apache.kafka.trogdor.workload.ProduceBenchSpec";

    /** The agent that runs the task; Trogdor's default, "", names none. */
    private String producerNode;

    private String bootstrapServers;
    private int targetMessagesPerSec;
    private long maxMessages;
    private Map<String, String> producerConf;
    private Map<String, TopicSpec> activeTopics;
    private KeyGeneratorSpec keyGenerator;
    private ValueGeneratorSpec valueGenerator;

    public ProduceBenchSpec(long durationMs) {
        super(CLASS_NAME, durationMs);
        this.producerConf = new HashMap<>();
        this.activeTopics = new HashMap<>();
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
        spec.getActiveTopics().put(topicName, topicSpec);

        ValueGeneratorSpec valGen = new ValueGeneratorSpec();
        valGen.setSize(recordSize);
        spec.setValueGenerator(valGen);

        return spec;
    }

    public String getProducerNode() {
        return producerNode;
    }

    public void setProducerNode(String producerNode) {
        this.producerNode = producerNode;
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

    /**
     * One entry of {@code activeTopics}, keyed by the topic's name. Trogdor
     * expands a range in the key into that many topics ({@code t[0-2]} is
     * t0, t1 and t2), so the partitions are {@code numPartitions}, never a
     * range: the key {@code t[0-2]} used to create three topics of three
     * partitions each, none of them the run's.
     */
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

    /**
     * Trogdor's PayloadGenerator is chosen by its {@code type}, and one without
     * a type is refused. This is its "sequential" generator, Trogdor's default
     * key.
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class KeyGeneratorSpec {
        private int size = 4;

        public String getType() {
            return "sequential";
        }

        public int getSize() {
            return size;
        }

        public void setSize(int size) {
            this.size = size;
        }
    }

    /**
     * A value of {@code size} zero bytes: Trogdor's "constant" generator. It
     * needs its {@code type} for the same reason as the key's; without one the
     * coordinator refused the task.
     */
    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class ValueGeneratorSpec {
        private int size = 1024;

        public String getType() {
            return "constant";
        }

        public int getSize() {
            return size;
        }

        public void setSize(int size) {
            this.size = size;
        }
    }
}
