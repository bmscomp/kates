package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.List;

import java.lang.reflect.Field;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.webhook.WebhookService;

class TestOrchestratorTest {

    private TestOrchestrator orchestrator;
    private TestTypeDefaults typeDefaults;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void setup() throws Exception {
        typeDefaults = new TestTypeDefaults();
        populateTypeDefaults(typeDefaults);
        orchestrator = new TestOrchestrator(
                mock(TopicService.class),
                mock(TestRunRepository.class),
                mock(Instance.class),
                typeDefaults,
                mock(BenchmarkMetrics.class),
                mock(KatesMetrics.class),
                mock(WebhookService.class),
                mock(Event.class),
                "native",
                "localhost:9092");
    }

    /**
     * Populates TestTypeDefaults fields to match the @ConfigProperty defaultValue annotations,
     * since CDI injection is not active in plain JUnit tests.
     */
    private void populateTypeDefaults(TestTypeDefaults td) throws Exception {
        setDefaults(td, "load", 3, 3, 2, "all", 65536, 5, "lz4", 1024, 1000000L, -1, 600000L, 1, 1);
        setDefaults(td, "stress", 3, 6, 2, "all", 131072, 10, "lz4", 1024, 5000000L, -1, 900000L, 3, 1);
        setDefaults(td, "spike", 3, 3, 2, "1", 131072, 0, "none", 1024, 2000000L, -1, 300000L, 1, 1);
        setDefaults(td, "endurance", 3, 3, 2, "all", 65536, 5, "lz4", 1024, 10000000L, 5000, 3600000L, 1, 1);
        setDefaults(td, "volume", 3, 6, 2, "all", 262144, 50, "lz4", 10240, 2000000L, -1, 600000L, 1, 1);
        setDefaults(td, "capacity", 3, 12, 2, "all", 131072, 10, "lz4", 1024, 10000000L, -1, 1200000L, 5, 1);
        setDefaults(td, "roundTrip", 3, 3, 2, "all", 16384, 0, "none", 1024, 500000L, 10000, 600000L, 1, 1);
    }

    private void setDefaults(TestTypeDefaults td, String prefix,
            int rf, int partitions, int isr, String acks,
            int batchSize, int lingerMs, String compression,
            int recordSize, long numRecords, int throughput,
            long durationMs, int numProducers, int numConsumers) throws Exception {
        setField(td, prefix + "ReplicationFactor", rf);
        setField(td, prefix + "Partitions", partitions);
        setField(td, prefix + "MinInsyncReplicas", isr);
        setField(td, prefix + "Acks", acks);
        setField(td, prefix + "BatchSize", batchSize);
        setField(td, prefix + "LingerMs", lingerMs);
        setField(td, prefix + "CompressionType", compression);
        setField(td, prefix + "RecordSize", recordSize);
        setField(td, prefix + "NumRecords", numRecords);
        setField(td, prefix + "Throughput", throughput);
        setField(td, prefix + "DurationMs", durationMs);
        setField(td, prefix + "NumProducers", numProducers);
        setField(td, prefix + "NumConsumers", numConsumers);
    }

    private void setField(Object target, String name, Object value) throws Exception {
        Field field = target.getClass().getDeclaredField(name);
        field.setAccessible(true);
        field.set(target, value);
    }

    private TestSpec specWith(int records, int producers) {
        TestSpec spec = new TestSpec();
        spec.setNumRecords(records);
        spec.setNumProducers(producers);
        spec.setNumConsumers(1);
        spec.setPartitions(3);
        spec.setAcks("all");
        spec.setBatchSize(65536);
        spec.setLingerMs(5);
        spec.setCompressionType("lz4");
        spec.setRecordSize(1024);
        spec.setThroughput(-1);
        spec.setDurationMs(60000);
        return spec;
    }

    @Nested
    class BuildTasksLoad {

        @Test
        void createsOneProducerAndOneConsumer() {
            TestSpec spec = specWith(100000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals(2, tasks.size(), "LOAD should create exactly 2 tasks (produce + consume)");
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
            assertEquals(BenchmarkTask.WorkloadType.CONSUME, tasks.get(1).getWorkloadType());
        }

        @Test
        void taskIdsContainRunId() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, specWith(1000, 1), "abc");

            assertTrue(tasks.get(0).getTaskId().startsWith("abc-"));
            assertTrue(tasks.get(1).getTaskId().startsWith("abc-"));
        }

        @Test
        void defaultTopicNameIsTypeBasedWhenNotSet() {
            TestSpec spec = specWith(1000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals("load-test", tasks.get(0).getTopic());
            assertEquals("load-test", tasks.get(1).getTopic());
        }

        @Test
        void customTopicIsRespected() {
            TestSpec spec = specWith(1000, 1);
            spec.setTopic("my-custom-topic");
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals("my-custom-topic", tasks.get(0).getTopic());
            assertEquals("my-custom-topic", tasks.get(1).getTopic());
        }

        @Test
        void producerConfigContainsBootstrapServers() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, specWith(1000, 1), "run-1");
            BenchmarkTask producer = tasks.get(0);

            assertEquals("localhost:9092", producer.getProducerConfig().get("bootstrap.servers"));
        }

        @Test
        void producerConfigContainsSpecValues() {
            TestSpec spec = specWith(1000, 1);
            spec.setCompressionType("zstd");
            spec.setAcks("1");
            spec.setBatchSize(16384);
            spec.setLingerMs(0);

            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");
            BenchmarkTask producer = tasks.get(0);

            assertEquals("zstd", producer.getProducerConfig().get("compression.type"));
            assertEquals("1", producer.getProducerConfig().get("acks"));
            assertEquals("16384", producer.getProducerConfig().get("batch.size"));
            assertEquals("0", producer.getProducerConfig().get("linger.ms"));
        }

        @Test
        void maxMessagesMatchesSpec() {
            TestSpec spec = specWith(500000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals(500000, tasks.get(0).getMaxMessages());
            assertEquals(500000, tasks.get(1).getMaxMessages());
        }

        @Test
        void recordSizeMatchesSpec() {
            TestSpec spec = specWith(1000, 1);
            spec.setRecordSize(2048);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals(2048, tasks.get(0).getRecordSize());
        }
    }

    @Nested
    class BuildTasksStress {

        @Test
        void createsNProducersFromSpec() {
            TestSpec spec = specWith(1000000, 4);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertEquals(4, tasks.size(), "STRESS should create exactly numProducers tasks");
            for (BenchmarkTask task : tasks) {
                assertEquals(BenchmarkTask.WorkloadType.PRODUCE, task.getWorkloadType());
            }
        }

        @Test
        void singleProducerStressIsValid() {
            TestSpec spec = specWith(100000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
        }

        @Test
        void taskIdsAreUnique() {
            TestSpec spec = specWith(1000, 3);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            long uniqueIds = tasks.stream().map(BenchmarkTask::getTaskId).distinct().count();
            assertEquals(3, uniqueIds, "Each stress producer must have a unique task ID");
        }

        @Test
        void eachProducerGetsFullRecordCount() {
            TestSpec spec = specWith(500000, 4);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals(500000, task.getMaxMessages(),
                        "Each STRESS producer gets the full record count from spec");
            }
        }

        @Test
        void topicNameIsStressTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.STRESS, specWith(1000, 2), "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals("stress-test", task.getTopic());
            }
        }
    }

    @Nested
    class BuildTasksSpike {

        @Test
        void createsSingleBurstProducer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
        }

        @Test
        void unlimitedThroughput() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals(-1, tasks.get(0).getTargetMessagesPerSec(),
                    "SPIKE burst producer should have unlimited throughput");
        }

        @Test
        void topicNameIsSpikeTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals("spike-test", tasks.get(0).getTopic());
        }
    }

    @Nested
    class BuildTasksEndurance {

        @Test
        void createsProducerAndConsumer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.ENDURANCE, specWith(1000, 1), "run-1");

            assertEquals(2, tasks.size(), "ENDURANCE should create produce + consume");
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
            assertEquals(BenchmarkTask.WorkloadType.CONSUME, tasks.get(1).getWorkloadType());
        }

        @Test
        void topicNameIsEnduranceTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.ENDURANCE, specWith(1000, 1), "run-1");

            assertEquals("endurance-test", tasks.get(0).getTopic());
        }
    }

    @Nested
    class BuildTasksVolume {

        @Test
        void createsSingleProducer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.VOLUME, specWith(1000, 1), "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
        }
    }

    @Nested
    class BuildTasksCapacity {

        @Test
        void createsNProducers() {
            TestSpec spec = specWith(1000000, 5);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.CAPACITY, spec, "run-1");

            assertEquals(5, tasks.size(), "CAPACITY should create numProducers tasks");
            for (BenchmarkTask task : tasks) {
                assertEquals(BenchmarkTask.WorkloadType.PRODUCE, task.getWorkloadType());
            }
        }

        @Test
        void unlimitedThroughput() {
            TestSpec spec = specWith(1000, 3);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.CAPACITY, spec, "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals(-1, task.getTargetMessagesPerSec(),
                        "CAPACITY producers should have unlimited throughput");
            }
        }
    }

    @Nested
    class BuildTasksRoundTrip {

        @Test
        void createsSingleRoundTripTask() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.ROUND_TRIP, specWith(1000, 1), "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.ROUND_TRIP, tasks.get(0).getWorkloadType());
        }

        @Test
        void respectsThroughputSetting() {
            TestSpec spec = specWith(1000, 1);
            spec.setThroughput(5000);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.ROUND_TRIP, spec, "run-1");

            assertEquals(5000, tasks.get(0).getTargetMessagesPerSec());
        }
    }

    @Nested
    class BuildTasksIntegrity {

        @Test
        void createsIntegrityWorkload() {
            TestSpec spec = specWith(1000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.INTEGRITY, spec, "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.INTEGRITY, tasks.get(0).getWorkloadType());
        }

        @Test
        void propagatesIdempotenceAndTransactions() {
            TestSpec spec = specWith(1000, 1);
            spec.setEnableIdempotence(true);
            spec.setEnableTransactions(true);
            spec.setEnableCrc(true);

            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.INTEGRITY, spec, "run-1");
            BenchmarkTask task = tasks.get(0);

            assertTrue(task.isEnableIdempotence(), "INTEGRITY must propagate enableIdempotence");
            assertTrue(task.isEnableTransactions(), "INTEGRITY must propagate enableTransactions");
            assertTrue(task.isEnableCrc(), "INTEGRITY must propagate enableCrc");
        }

        @Test
        void defaultConsumerGroupIsIntegrityCg() {
            TestSpec spec = specWith(1000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.INTEGRITY, spec, "run-1");

            assertEquals("integrity-cg", tasks.get(0).getConsumerGroup());
        }

        @Test
        void customConsumerGroupOverridesDefault() {
            TestSpec spec = specWith(1000, 1);
            spec.setConsumerGroup("my-cg");
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.INTEGRITY, spec, "run-1");

            assertEquals("my-cg", tasks.get(0).getConsumerGroup());
        }
    }

    @Nested
    class BuildTasksTune {

        @ParameterizedTest
        @EnumSource(value = TestType.class, names = {
                "TUNE_REPLICATION", "TUNE_ACKS", "TUNE_BATCHING", "TUNE_COMPRESSION", "TUNE_PARTITIONS"
        })
        void tuneTypesCreateSingleProducer(TestType tuneType) {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(tuneType, specWith(1000, 1), "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
        }
    }

    @Nested
    class BuildTasksAllTypes {

        @ParameterizedTest
        @EnumSource(TestType.class)
        void allTypesReturnNonEmptyTaskList(TestType type) {
            TestSpec spec = specWith(1000, 2);
            spec.setEnableIdempotence(true);
            spec.setEnableTransactions(true);
            spec.setEnableCrc(true);

            List<BenchmarkTask> tasks = orchestrator.buildTasks(type, spec, "run-1");

            assertFalse(tasks.isEmpty(), type + " should produce at least one task");
            for (BenchmarkTask task : tasks) {
                assertNotNull(task.getTaskId(), type + ": task ID must not be null");
                assertNotNull(task.getWorkloadType(), type + ": workload type must not be null");
                assertNotNull(task.getTopic(), type + ": topic must not be null");
                assertTrue(task.getMaxMessages() > 0 || task.getDurationMs() > 0,
                        type + ": task must have records or duration");
            }
        }
    }

    @Nested
    class ApplyTypeDefaults {

        @Test
        void loadDefaultsAppliedWhenNoUserSpec() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, null);

            assertEquals(3, merged.getReplicationFactor());
            assertEquals(3, merged.getPartitions());
            assertEquals(2, merged.getMinInsyncReplicas());
            assertEquals("all", merged.getAcks());
            assertEquals(65536, merged.getBatchSize());
            assertEquals(5, merged.getLingerMs());
            assertEquals("lz4", merged.getCompressionType());
            assertEquals(1024, merged.getRecordSize());
            assertEquals(1000000, merged.getNumRecords());
            assertEquals(-1, merged.getThroughput());
            assertEquals(600000, merged.getDurationMs());
            assertEquals(1, merged.getNumProducers());
            assertEquals(1, merged.getNumConsumers());
        }

        @Test
        void stressDefaultsHaveLargerBatchAndMoreProducers() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.STRESS, null);

            assertEquals(131072, merged.getBatchSize(), "STRESS should use 128KB batches");
            assertEquals(3, merged.getNumProducers(), "STRESS should default to 3 producers");
            assertEquals(6, merged.getPartitions(), "STRESS should default to 6 partitions");
        }

        @Test
        void spikeDefaultsUseAcksOne() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.SPIKE, null);

            assertEquals("1", merged.getAcks(), "SPIKE should use acks=1 for low latency");
            assertEquals("none", merged.getCompressionType(), "SPIKE should not compress");
        }

        @Test
        void enduranceDefaultsHaveRateLimitedThroughput() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.ENDURANCE, null);

            assertEquals(5000, merged.getThroughput(), "ENDURANCE should rate-limit to 5000 msg/s");
            assertEquals(3600000, merged.getDurationMs(), "ENDURANCE should run for 1 hour");
        }

        @Test
        void volumeDefaultsUseLargeRecords() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.VOLUME, null);

            assertEquals(10240, merged.getRecordSize(), "VOLUME should use 10KB records");
            assertEquals(262144, merged.getBatchSize(), "VOLUME should use 256KB batches");
        }

        @Test
        void capacityDefaultsHaveHighParallelism() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.CAPACITY, null);

            assertEquals(12, merged.getPartitions(), "CAPACITY should use 12 partitions");
            assertEquals(5, merged.getNumProducers(), "CAPACITY should default to 5 producers");
        }

        @Test
        void roundTripDefaultsUseNoCompression() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.ROUND_TRIP, null);

            assertEquals("none", merged.getCompressionType(), "ROUND_TRIP should not compress");
            assertEquals(10000, merged.getThroughput(), "ROUND_TRIP should rate-limit to 10k msg/s");
        }

        @Test
        void userOverridesTakePriority() {
            TestSpec userSpec = new TestSpec();
            userSpec.setPartitions(24);
            userSpec.setCompressionType("zstd");
            userSpec.setNumProducers(8);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.STRESS, userSpec);

            assertEquals(24, merged.getPartitions(), "User partition override should take priority");
            assertEquals("zstd", merged.getCompressionType(), "User compression override should win");
            assertEquals(8, merged.getNumProducers(), "User producers override should win");
            assertEquals(131072, merged.getBatchSize(), "Non-overridden fields keep type defaults");
        }

        @Test
        void userOverridesDoNotLoseTypeDefaults() {
            TestSpec userSpec = new TestSpec();
            userSpec.setNumRecords(999);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.STRESS, userSpec);

            assertEquals(999, merged.getNumRecords(), "User numRecords override applied");
            assertEquals(3, merged.getNumProducers(), "Stress default producers preserved");
            assertEquals(6, merged.getPartitions(), "Stress default partitions preserved");
            assertEquals(131072, merged.getBatchSize(), "Stress default batchSize preserved");
        }

        @Test
        void nullUserSpecUsesDefaults() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, null);

            assertNotNull(merged, "Null user spec should return defaults, not null");
            assertEquals("all", merged.getAcks());
            assertEquals(1, merged.getNumProducers());
        }

        @Test
        void customTopicPreservedFromUserSpec() {
            TestSpec userSpec = new TestSpec();
            userSpec.setTopic("performance-topic");

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, userSpec);

            assertEquals("performance-topic", merged.getTopic());
        }
    }

    @Nested
    class DurationAndRecordsPropagation {

        @Test
        void loadTasksPropagateMaxMessagesAndDuration() {
            TestSpec spec = specWith(250000, 1);
            spec.setDurationMs(120000);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals(250000, task.getMaxMessages());
                assertEquals(120000, task.getDurationMs());
            }
        }

        @Test
        void stressDurationPropagatedToAllProducers() {
            TestSpec spec = specWith(500000, 4);
            spec.setDurationMs(300000);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertEquals(4, tasks.size());
            for (BenchmarkTask task : tasks) {
                assertEquals(300000, task.getDurationMs(),
                        "All STRESS producers must have the same duration");
                assertEquals(500000, task.getMaxMessages(),
                        "STRESS gives each producer the full record count");
            }
        }

        @Test
        void spikeDurationAndRecordsPropagated() {
            TestSpec spec = specWith(2000000, 1);
            spec.setDurationMs(60000);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.SPIKE, spec, "run-1");

            assertEquals(2000000, tasks.get(0).getMaxMessages());
            assertEquals(60000, tasks.get(0).getDurationMs());
        }

        @Test
        void enduranceDurationMatches1Hour() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.ENDURANCE, null);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.ENDURANCE, merged, "run-1");

            assertEquals(3600000, tasks.get(0).getDurationMs(),
                    "ENDURANCE default duration should be 1 hour");
        }
    }

    @Nested
    class EdgeCases {

        @Test
        void zeroRecordsProducesTaskWithZeroMaxMessages() {
            TestSpec spec = specWith(0, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertFalse(tasks.isEmpty(), "Should still create tasks even with 0 records");
            assertEquals(0, tasks.get(0).getMaxMessages());
        }

        @Test
        void zeroDurationProducesTaskWithZeroDuration() {
            TestSpec spec = specWith(1000, 1);
            spec.setDurationMs(0);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, spec, "run-1");

            assertEquals(0, tasks.get(0).getDurationMs());
        }

        @Test
        void stressWithZeroProducersCreatesEmptyList() {
            TestSpec spec = specWith(1000, 0);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertTrue(tasks.isEmpty(),
                    "STRESS with 0 producers should create an empty task list");
        }

        @Test
        void capacityWithZeroProducersCreatesEmptyList() {
            TestSpec spec = specWith(1000, 0);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.CAPACITY, spec, "run-1");

            assertTrue(tasks.isEmpty(),
                    "CAPACITY with 0 producers should create an empty task list");
        }

        @Test
        void defaultSpecWithNoUserFieldsWorks() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, new TestSpec());
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, merged, "run-1");

            assertEquals(2, tasks.size(), "Default LOAD should still produce 2 tasks");
            assertEquals(1000000, tasks.get(0).getMaxMessages(), "Should use LOAD default records");
        }

        @Test
        void integrityWithoutIdempotenceFlagsDefaultsToFalse() {
            TestSpec spec = specWith(1000, 1);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.INTEGRITY, spec, "run-1");
            BenchmarkTask task = tasks.get(0);

            assertFalse(task.isEnableIdempotence(),
                    "Idempotence should default to false when not explicitly set");
            assertFalse(task.isEnableTransactions(),
                    "Transactions should default to false when not explicitly set");
        }

        @Test
        void veryLargeProducerCountCreatesCorrectNumberOfTasks() {
            TestSpec spec = specWith(1000, 16);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertEquals(16, tasks.size(), "Should handle large producer counts");
            long uniqueIds = tasks.stream().map(BenchmarkTask::getTaskId).distinct().count();
            assertEquals(16, uniqueIds, "All 16 task IDs must be unique");
        }
    }
}
