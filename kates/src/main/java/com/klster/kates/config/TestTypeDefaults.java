package com.klster.kates.config;

import com.klster.kates.domain.TestType;
import jakarta.enterprise.context.ApplicationScoped;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.LinkedHashMap;
import java.util.Map;

/**
 * Provides per-test-type default configuration.
 * Each test type can override any global default via properties like:
 *   kates.tests.load.partitions=6
 *   kates.tests.stress.batch-size=131072
 *
 * If a per-type value is not set, the global default is used as literal fallback.
 */
@ApplicationScoped
public class TestTypeDefaults {

    // LOAD — baseline throughput, all globals
    @ConfigProperty(name = "kates.tests.load.replication-factor", defaultValue = "3")
    int loadReplicationFactor;
    @ConfigProperty(name = "kates.tests.load.partitions", defaultValue = "3")
    int loadPartitions;
    @ConfigProperty(name = "kates.tests.load.min-insync-replicas", defaultValue = "2")
    int loadMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.load.acks", defaultValue = "all")
    String loadAcks;
    @ConfigProperty(name = "kates.tests.load.batch-size", defaultValue = "65536")
    int loadBatchSize;
    @ConfigProperty(name = "kates.tests.load.linger-ms", defaultValue = "5")
    int loadLingerMs;
    @ConfigProperty(name = "kates.tests.load.compression-type", defaultValue = "lz4")
    String loadCompressionType;
    @ConfigProperty(name = "kates.tests.load.record-size", defaultValue = "1024")
    int loadRecordSize;
    @ConfigProperty(name = "kates.tests.load.num-records", defaultValue = "1000000")
    long loadNumRecords;
    @ConfigProperty(name = "kates.tests.load.throughput", defaultValue = "-1")
    int loadThroughput;
    @ConfigProperty(name = "kates.tests.load.duration-ms", defaultValue = "600000")
    long loadDurationMs;
    @ConfigProperty(name = "kates.tests.load.num-producers", defaultValue = "1")
    int loadNumProducers;
    @ConfigProperty(name = "kates.tests.load.num-consumers", defaultValue = "1")
    int loadNumConsumers;

    // STRESS — high concurrency, large batches
    @ConfigProperty(name = "kates.tests.stress.replication-factor", defaultValue = "3")
    int stressReplicationFactor;
    @ConfigProperty(name = "kates.tests.stress.partitions", defaultValue = "6")
    int stressPartitions;
    @ConfigProperty(name = "kates.tests.stress.min-insync-replicas", defaultValue = "2")
    int stressMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.stress.acks", defaultValue = "all")
    String stressAcks;
    @ConfigProperty(name = "kates.tests.stress.batch-size", defaultValue = "131072")
    int stressBatchSize;
    @ConfigProperty(name = "kates.tests.stress.linger-ms", defaultValue = "10")
    int stressLingerMs;
    @ConfigProperty(name = "kates.tests.stress.compression-type", defaultValue = "lz4")
    String stressCompressionType;
    @ConfigProperty(name = "kates.tests.stress.record-size", defaultValue = "1024")
    int stressRecordSize;
    @ConfigProperty(name = "kates.tests.stress.num-records", defaultValue = "5000000")
    long stressNumRecords;
    @ConfigProperty(name = "kates.tests.stress.throughput", defaultValue = "-1")
    int stressThroughput;
    @ConfigProperty(name = "kates.tests.stress.duration-ms", defaultValue = "900000")
    long stressDurationMs;
    @ConfigProperty(name = "kates.tests.stress.num-producers", defaultValue = "3")
    int stressNumProducers;
    @ConfigProperty(name = "kates.tests.stress.num-consumers", defaultValue = "1")
    int stressNumConsumers;

    // SPIKE — burst traffic, low-latency
    @ConfigProperty(name = "kates.tests.spike.replication-factor", defaultValue = "3")
    int spikeReplicationFactor;
    @ConfigProperty(name = "kates.tests.spike.partitions", defaultValue = "3")
    int spikePartitions;
    @ConfigProperty(name = "kates.tests.spike.min-insync-replicas", defaultValue = "2")
    int spikeMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.spike.acks", defaultValue = "1")
    String spikeAcks;
    @ConfigProperty(name = "kates.tests.spike.batch-size", defaultValue = "131072")
    int spikeBatchSize;
    @ConfigProperty(name = "kates.tests.spike.linger-ms", defaultValue = "0")
    int spikeLingerMs;
    @ConfigProperty(name = "kates.tests.spike.compression-type", defaultValue = "none")
    String spikeCompressionType;
    @ConfigProperty(name = "kates.tests.spike.record-size", defaultValue = "1024")
    int spikeRecordSize;
    @ConfigProperty(name = "kates.tests.spike.num-records", defaultValue = "2000000")
    long spikeNumRecords;
    @ConfigProperty(name = "kates.tests.spike.throughput", defaultValue = "-1")
    int spikeThroughput;
    @ConfigProperty(name = "kates.tests.spike.duration-ms", defaultValue = "300000")
    long spikeDurationMs;
    @ConfigProperty(name = "kates.tests.spike.num-producers", defaultValue = "1")
    int spikeNumProducers;
    @ConfigProperty(name = "kates.tests.spike.num-consumers", defaultValue = "1")
    int spikeNumConsumers;

    // ENDURANCE — long-running, rate-limited
    @ConfigProperty(name = "kates.tests.endurance.replication-factor", defaultValue = "3")
    int enduranceReplicationFactor;
    @ConfigProperty(name = "kates.tests.endurance.partitions", defaultValue = "3")
    int endurancePartitions;
    @ConfigProperty(name = "kates.tests.endurance.min-insync-replicas", defaultValue = "2")
    int enduranceMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.endurance.acks", defaultValue = "all")
    String enduranceAcks;
    @ConfigProperty(name = "kates.tests.endurance.batch-size", defaultValue = "65536")
    int enduranceBatchSize;
    @ConfigProperty(name = "kates.tests.endurance.linger-ms", defaultValue = "5")
    int enduranceLingerMs;
    @ConfigProperty(name = "kates.tests.endurance.compression-type", defaultValue = "lz4")
    String enduranceCompressionType;
    @ConfigProperty(name = "kates.tests.endurance.record-size", defaultValue = "1024")
    int enduranceRecordSize;
    @ConfigProperty(name = "kates.tests.endurance.num-records", defaultValue = "10000000")
    long enduranceNumRecords;
    @ConfigProperty(name = "kates.tests.endurance.throughput", defaultValue = "5000")
    int enduranceThroughput;
    @ConfigProperty(name = "kates.tests.endurance.duration-ms", defaultValue = "3600000")
    long enduranceDurationMs;
    @ConfigProperty(name = "kates.tests.endurance.num-producers", defaultValue = "1")
    int enduranceNumProducers;
    @ConfigProperty(name = "kates.tests.endurance.num-consumers", defaultValue = "1")
    int enduranceNumConsumers;

    // VOLUME — large records, high batch size
    @ConfigProperty(name = "kates.tests.volume.replication-factor", defaultValue = "3")
    int volumeReplicationFactor;
    @ConfigProperty(name = "kates.tests.volume.partitions", defaultValue = "6")
    int volumePartitions;
    @ConfigProperty(name = "kates.tests.volume.min-insync-replicas", defaultValue = "2")
    int volumeMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.volume.acks", defaultValue = "all")
    String volumeAcks;
    @ConfigProperty(name = "kates.tests.volume.batch-size", defaultValue = "262144")
    int volumeBatchSize;
    @ConfigProperty(name = "kates.tests.volume.linger-ms", defaultValue = "50")
    int volumeLingerMs;
    @ConfigProperty(name = "kates.tests.volume.compression-type", defaultValue = "lz4")
    String volumeCompressionType;
    @ConfigProperty(name = "kates.tests.volume.record-size", defaultValue = "10240")
    int volumeRecordSize;
    @ConfigProperty(name = "kates.tests.volume.num-records", defaultValue = "2000000")
    long volumeNumRecords;
    @ConfigProperty(name = "kates.tests.volume.throughput", defaultValue = "-1")
    int volumeThroughput;
    @ConfigProperty(name = "kates.tests.volume.duration-ms", defaultValue = "600000")
    long volumeDurationMs;
    @ConfigProperty(name = "kates.tests.volume.num-producers", defaultValue = "1")
    int volumeNumProducers;
    @ConfigProperty(name = "kates.tests.volume.num-consumers", defaultValue = "1")
    int volumeNumConsumers;

    // CAPACITY — max parallelism
    @ConfigProperty(name = "kates.tests.capacity.replication-factor", defaultValue = "3")
    int capacityReplicationFactor;
    @ConfigProperty(name = "kates.tests.capacity.partitions", defaultValue = "12")
    int capacityPartitions;
    @ConfigProperty(name = "kates.tests.capacity.min-insync-replicas", defaultValue = "2")
    int capacityMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.capacity.acks", defaultValue = "all")
    String capacityAcks;
    @ConfigProperty(name = "kates.tests.capacity.batch-size", defaultValue = "131072")
    int capacityBatchSize;
    @ConfigProperty(name = "kates.tests.capacity.linger-ms", defaultValue = "10")
    int capacityLingerMs;
    @ConfigProperty(name = "kates.tests.capacity.compression-type", defaultValue = "lz4")
    String capacityCompressionType;
    @ConfigProperty(name = "kates.tests.capacity.record-size", defaultValue = "1024")
    int capacityRecordSize;
    @ConfigProperty(name = "kates.tests.capacity.num-records", defaultValue = "10000000")
    long capacityNumRecords;
    @ConfigProperty(name = "kates.tests.capacity.throughput", defaultValue = "-1")
    int capacityThroughput;
    @ConfigProperty(name = "kates.tests.capacity.duration-ms", defaultValue = "1200000")
    long capacityDurationMs;
    @ConfigProperty(name = "kates.tests.capacity.num-producers", defaultValue = "5")
    int capacityNumProducers;
    @ConfigProperty(name = "kates.tests.capacity.num-consumers", defaultValue = "1")
    int capacityNumConsumers;

    // ROUND_TRIP — latency-focused
    @ConfigProperty(name = "kates.tests.roundtrip.replication-factor", defaultValue = "3")
    int roundTripReplicationFactor;
    @ConfigProperty(name = "kates.tests.roundtrip.partitions", defaultValue = "3")
    int roundTripPartitions;
    @ConfigProperty(name = "kates.tests.roundtrip.min-insync-replicas", defaultValue = "2")
    int roundTripMinInsyncReplicas;
    @ConfigProperty(name = "kates.tests.roundtrip.acks", defaultValue = "all")
    String roundTripAcks;
    @ConfigProperty(name = "kates.tests.roundtrip.batch-size", defaultValue = "16384")
    int roundTripBatchSize;
    @ConfigProperty(name = "kates.tests.roundtrip.linger-ms", defaultValue = "0")
    int roundTripLingerMs;
    @ConfigProperty(name = "kates.tests.roundtrip.compression-type", defaultValue = "none")
    String roundTripCompressionType;
    @ConfigProperty(name = "kates.tests.roundtrip.record-size", defaultValue = "1024")
    int roundTripRecordSize;
    @ConfigProperty(name = "kates.tests.roundtrip.num-records", defaultValue = "500000")
    long roundTripNumRecords;
    @ConfigProperty(name = "kates.tests.roundtrip.throughput", defaultValue = "10000")
    int roundTripThroughput;
    @ConfigProperty(name = "kates.tests.roundtrip.duration-ms", defaultValue = "600000")
    long roundTripDurationMs;
    @ConfigProperty(name = "kates.tests.roundtrip.num-producers", defaultValue = "1")
    int roundTripNumProducers;
    @ConfigProperty(name = "kates.tests.roundtrip.num-consumers", defaultValue = "1")
    int roundTripNumConsumers;

    /**
     * Returns the effective configuration for a given test type.
     * Values are resolved from kates.tests.{type}.* with literal fallback defaults.
     */
    public TypeConfig forType(TestType type) {
        return switch (type) {
            case LOAD -> new TypeConfig(loadReplicationFactor, loadPartitions, loadMinInsyncReplicas,
                    loadAcks, loadBatchSize, loadLingerMs, loadCompressionType, loadRecordSize,
                    loadNumRecords, loadThroughput, loadDurationMs, loadNumProducers, loadNumConsumers);
            case STRESS -> new TypeConfig(stressReplicationFactor, stressPartitions, stressMinInsyncReplicas,
                    stressAcks, stressBatchSize, stressLingerMs, stressCompressionType, stressRecordSize,
                    stressNumRecords, stressThroughput, stressDurationMs, stressNumProducers, stressNumConsumers);
            case SPIKE -> new TypeConfig(spikeReplicationFactor, spikePartitions, spikeMinInsyncReplicas,
                    spikeAcks, spikeBatchSize, spikeLingerMs, spikeCompressionType, spikeRecordSize,
                    spikeNumRecords, spikeThroughput, spikeDurationMs, spikeNumProducers, spikeNumConsumers);
            case ENDURANCE -> new TypeConfig(enduranceReplicationFactor, endurancePartitions, enduranceMinInsyncReplicas,
                    enduranceAcks, enduranceBatchSize, enduranceLingerMs, enduranceCompressionType, enduranceRecordSize,
                    enduranceNumRecords, enduranceThroughput, enduranceDurationMs, enduranceNumProducers, enduranceNumConsumers);
            case VOLUME -> new TypeConfig(volumeReplicationFactor, volumePartitions, volumeMinInsyncReplicas,
                    volumeAcks, volumeBatchSize, volumeLingerMs, volumeCompressionType, volumeRecordSize,
                    volumeNumRecords, volumeThroughput, volumeDurationMs, volumeNumProducers, volumeNumConsumers);
            case CAPACITY -> new TypeConfig(capacityReplicationFactor, capacityPartitions, capacityMinInsyncReplicas,
                    capacityAcks, capacityBatchSize, capacityLingerMs, capacityCompressionType, capacityRecordSize,
                    capacityNumRecords, capacityThroughput, capacityDurationMs, capacityNumProducers, capacityNumConsumers);
            case ROUND_TRIP -> new TypeConfig(roundTripReplicationFactor, roundTripPartitions, roundTripMinInsyncReplicas,
                    roundTripAcks, roundTripBatchSize, roundTripLingerMs, roundTripCompressionType, roundTripRecordSize,
                    roundTripNumRecords, roundTripThroughput, roundTripDurationMs, roundTripNumProducers, roundTripNumConsumers);
            case INTEGRITY -> new TypeConfig(loadReplicationFactor, loadPartitions, loadMinInsyncReplicas,
                    "all", loadBatchSize, loadLingerMs, loadCompressionType, loadRecordSize,
                    loadNumRecords, loadThroughput, loadDurationMs, loadNumProducers, loadNumConsumers);
        };
    }

    /**
     * Returns the effective configs for all test types (for the health endpoint).
     */
    public Map<String, TypeConfig> allConfigs() {
        Map<String, TypeConfig> configs = new LinkedHashMap<>();
        for (TestType type : TestType.values()) {
            String key = switch (type) {
                case ROUND_TRIP -> "roundtrip";
                default -> type.name().toLowerCase();
            };
            configs.put(key, forType(type));
        }
        return configs;
    }

    public record TypeConfig(
            int replicationFactor,
            int partitions,
            int minInsyncReplicas,
            String acks,
            int batchSize,
            int lingerMs,
            String compressionType,
            int recordSize,
            long numRecords,
            int throughput,
            long durationMs,
            int numProducers,
            int numConsumers
    ) {
        public Map<String, Object> toMap() {
            Map<String, Object> map = new LinkedHashMap<>();
            map.put("replicationFactor", replicationFactor);
            map.put("partitions", partitions);
            map.put("minInsyncReplicas", minInsyncReplicas);
            map.put("acks", acks);
            map.put("batchSize", batchSize);
            map.put("lingerMs", lingerMs);
            map.put("compressionType", compressionType);
            map.put("recordSize", recordSize);
            map.put("numRecords", numRecords);
            map.put("throughput", throughput);
            map.put("durationMs", durationMs);
            map.put("numProducers", numProducers);
            map.put("numConsumers", numConsumers);
            return map;
        }
    }
}
