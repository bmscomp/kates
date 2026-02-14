package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Top-level scenario envelope for multi-phase test definitions.
 * When submitted via {@code POST /api/tests}, the orchestrator executes
 * each {@link ScenarioPhase} sequentially, applying per-phase overrides
 * and evaluating SLA gates between phases.
 *
 * <p>Backward-compatible: if {@code scenario} is absent on
 * {@link CreateTestRequest}, the legacy flat {@code type + spec} path is used.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class TestScenario {

    private String name;
    private String description;
    private TestType type;
    private String backend;
    private TestSpec baseSpec;
    private List<ScenarioPhase> phases = new ArrayList<>();
    private SlaDefinition sla;
    private Map<String, String> labels = new LinkedHashMap<>();

    public TestScenario() {
    }

    public String getName() { return name; }
    public void setName(String name) { this.name = name; }

    public String getDescription() { return description; }
    public void setDescription(String description) { this.description = description; }

    public TestType getType() { return type; }
    public void setType(TestType type) { this.type = type; }

    public String getBackend() { return backend; }
    public void setBackend(String backend) { this.backend = backend; }

    public TestSpec getBaseSpec() { return baseSpec; }
    public void setBaseSpec(TestSpec baseSpec) { this.baseSpec = baseSpec; }

    public List<ScenarioPhase> getPhases() { return phases; }
    public void setPhases(List<ScenarioPhase> phases) { this.phases = phases; }

    public SlaDefinition getSla() { return sla; }
    public void setSla(SlaDefinition sla) { this.sla = sla; }

    public Map<String, String> getLabels() { return labels; }
    public void setLabels(Map<String, String> labels) { this.labels = labels; }

    /**
     * Resolves the effective spec for a given phase by merging
     * phase-level overrides onto the scenario base spec.
     */
    public TestSpec resolveSpecForPhase(ScenarioPhase phase) {
        TestSpec base = baseSpec != null ? baseSpec : new TestSpec();
        TestSpec phaseSpec = phase.getSpec();
        if (phaseSpec == null) {
            TestSpec copy = copySpec(base);
            if (phase.getTargetThroughput() != -1) {
                copy.setThroughput(phase.getTargetThroughput());
            }
            if (phase.getDurationMs() > 0) {
                copy.setDurationMs(phase.getDurationMs());
            }
            return copy;
        }

        TestSpec merged = copySpec(base);
        if (phaseSpec.getTopic() != null) merged.setTopic(phaseSpec.getTopic());
        if (phaseSpec.getReplicationFactor() != 3) merged.setReplicationFactor(phaseSpec.getReplicationFactor());
        if (phaseSpec.getPartitions() != 3) merged.setPartitions(phaseSpec.getPartitions());
        if (phaseSpec.getMinInsyncReplicas() != 2) merged.setMinInsyncReplicas(phaseSpec.getMinInsyncReplicas());
        if (!"all".equals(phaseSpec.getAcks())) merged.setAcks(phaseSpec.getAcks());
        if (phaseSpec.getBatchSize() != 65536) merged.setBatchSize(phaseSpec.getBatchSize());
        if (phaseSpec.getLingerMs() != 5) merged.setLingerMs(phaseSpec.getLingerMs());
        if (!"lz4".equals(phaseSpec.getCompressionType())) merged.setCompressionType(phaseSpec.getCompressionType());
        if (phaseSpec.getRecordSize() != 1024) merged.setRecordSize(phaseSpec.getRecordSize());
        if (phaseSpec.getNumRecords() != 1_000_000) merged.setNumRecords(phaseSpec.getNumRecords());
        if (phaseSpec.getThroughput() != -1) merged.setThroughput(phaseSpec.getThroughput());
        if (phaseSpec.getDurationMs() != 600_000) merged.setDurationMs(phaseSpec.getDurationMs());
        if (phaseSpec.getNumProducers() != 1) merged.setNumProducers(phaseSpec.getNumProducers());
        if (phaseSpec.getNumConsumers() != 1) merged.setNumConsumers(phaseSpec.getNumConsumers());
        if (phaseSpec.getConsumerGroup() != null) merged.setConsumerGroup(phaseSpec.getConsumerGroup());
        if (phaseSpec.getTargetThroughput() != -1) merged.setTargetThroughput(phaseSpec.getTargetThroughput());
        if (phaseSpec.getFetchMinBytes() != 1) merged.setFetchMinBytes(phaseSpec.getFetchMinBytes());
        if (phaseSpec.getFetchMaxWaitMs() != 500) merged.setFetchMaxWaitMs(phaseSpec.getFetchMaxWaitMs());

        if (phase.getTargetThroughput() != -1) {
            merged.setThroughput(phase.getTargetThroughput());
        }
        if (phase.getDurationMs() > 0) {
            merged.setDurationMs(phase.getDurationMs());
        }

        return merged;
    }

    private TestSpec copySpec(TestSpec src) {
        TestSpec copy = new TestSpec();
        copy.setTopic(src.getTopic());
        copy.setReplicationFactor(src.getReplicationFactor());
        copy.setPartitions(src.getPartitions());
        copy.setMinInsyncReplicas(src.getMinInsyncReplicas());
        copy.setAcks(src.getAcks());
        copy.setBatchSize(src.getBatchSize());
        copy.setLingerMs(src.getLingerMs());
        copy.setCompressionType(src.getCompressionType());
        copy.setRecordSize(src.getRecordSize());
        copy.setNumRecords(src.getNumRecords());
        copy.setThroughput(src.getThroughput());
        copy.setDurationMs(src.getDurationMs());
        copy.setNumProducers(src.getNumProducers());
        copy.setNumConsumers(src.getNumConsumers());
        copy.setConsumerGroup(src.getConsumerGroup());
        copy.setTargetThroughput(src.getTargetThroughput());
        copy.setFetchMinBytes(src.getFetchMinBytes());
        copy.setFetchMaxWaitMs(src.getFetchMaxWaitMs());
        return copy;
    }
}
