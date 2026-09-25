package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.lang.reflect.Field;
import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.inject.Instance;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

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
                // Real, not mocked: it is stateless and a mock would return a
                // null verdict, which the poll path dereferences.
                new SlaEvaluator(),
                mock(Event.class),
                "native",
                "localhost:9092",
                3);
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

    private void setDefaults(
            TestTypeDefaults td,
            String prefix,
            int rf,
            int partitions,
            int isr,
            String acks,
            int batchSize,
            int lingerMs,
            String compression,
            int recordSize,
            long numRecords,
            int throughput,
            long durationMs,
            int numProducers,
            int numConsumers)
            throws Exception {
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

            long uniqueIds =
                    tasks.stream().map(BenchmarkTask::getTaskId).distinct().count();
            assertEquals(3, uniqueIds, "Each stress producer must have a unique task ID");
        }

        @Test
        void eachProducerGetsFullRecordCount() {
            TestSpec spec = specWith(500000, 4);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals(
                        500000, task.getMaxMessages(), "Each STRESS producer gets the full record count from spec");
            }
        }

        @Test
        void topicNameIsStressTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, specWith(1000, 2), "run-1");

            for (BenchmarkTask task : tasks) {
                assertEquals("stress-test", task.getTopic());
            }
        }
    }

    @Nested
    class BuildTasksSpike {

        @Test
        void createsSingleBurstProducer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals(1, tasks.size());
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
        }

        @Test
        void unlimitedThroughput() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals(
                    -1,
                    tasks.get(0).getTargetMessagesPerSec(),
                    "SPIKE burst producer should have unlimited throughput");
        }

        @Test
        void topicNameIsSpikeTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.SPIKE, specWith(1000, 1), "run-1");

            assertEquals("spike-test", tasks.get(0).getTopic());
        }
    }

    @Nested
    class BuildTasksEndurance {

        @Test
        void createsProducerAndConsumer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.ENDURANCE, specWith(1000, 1), "run-1");

            assertEquals(2, tasks.size(), "ENDURANCE should create produce + consume");
            assertEquals(BenchmarkTask.WorkloadType.PRODUCE, tasks.get(0).getWorkloadType());
            assertEquals(BenchmarkTask.WorkloadType.CONSUME, tasks.get(1).getWorkloadType());
        }

        @Test
        void topicNameIsEnduranceTest() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.ENDURANCE, specWith(1000, 1), "run-1");

            assertEquals("endurance-test", tasks.get(0).getTopic());
        }
    }

    @Nested
    class BuildTasksVolume {

        @Test
        void createsSingleProducer() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.VOLUME, specWith(1000, 1), "run-1");

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
                assertEquals(-1, task.getTargetMessagesPerSec(), "CAPACITY producers should have unlimited throughput");
            }
        }
    }

    @Nested
    class BuildTasksRoundTrip {

        @Test
        void createsSingleRoundTripTask() {
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.ROUND_TRIP, specWith(1000, 1), "run-1");

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
        @EnumSource(
                value = TestType.class,
                names = {"TUNE_REPLICATION", "TUNE_ACKS", "TUNE_BATCHING", "TUNE_COMPRESSION", "TUNE_PARTITIONS"})
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
                assertTrue(
                        task.getMaxMessages() > 0 || task.getDurationMs() > 0,
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
                assertEquals(300000, task.getDurationMs(), "All STRESS producers must have the same duration");
                assertEquals(500000, task.getMaxMessages(), "STRESS gives each producer the full record count");
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

            assertEquals(3600000, tasks.get(0).getDurationMs(), "ENDURANCE default duration should be 1 hour");
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

            assertTrue(tasks.isEmpty(), "STRESS with 0 producers should create an empty task list");
        }

        @Test
        void capacityWithZeroProducersCreatesEmptyList() {
            TestSpec spec = specWith(1000, 0);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.CAPACITY, spec, "run-1");

            assertTrue(tasks.isEmpty(), "CAPACITY with 0 producers should create an empty task list");
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

            assertFalse(task.isEnableIdempotence(), "Idempotence should default to false when not explicitly set");
            assertFalse(task.isEnableTransactions(), "Transactions should default to false when not explicitly set");
        }

        @Test
        void veryLargeProducerCountCreatesCorrectNumberOfTasks() {
            TestSpec spec = specWith(1000, 16);
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.STRESS, spec, "run-1");

            assertEquals(16, tasks.size(), "Should handle large producer counts");
            long uniqueIds =
                    tasks.stream().map(BenchmarkTask::getTaskId).distinct().count();
            assertEquals(16, uniqueIds, "All 16 task IDs must be unique");
        }
    }

    /**
     * Every field a request may set reaches the merged spec and the tasks built
     * from it. applyTypeDefaults used to copy fourteen fields and drop the other
     * seven, so a requested rate, consumer group, fetch setting or integrity
     * option was accepted, echoed at its default and never used.
     */
    @Nested
    class RequestedFieldsReachTheRun {

        private TestSpec requested() {
            return new TestSpec();
        }

        @Test
        void targetThroughputFillsTheRateWhenThroughputIsUnset() {
            TestSpec req = requested();
            req.setTargetThroughput(2000);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, req);

            assertEquals(2000, merged.getThroughput(), "the producer honours throughput, so the alias must fill it");
            assertEquals(2000, merged.getTargetThroughput(), "the requested alias is kept as it was sent");
            assertEquals(
                    2000,
                    orchestrator
                            .buildTasks(TestType.LOAD, merged, "run-1")
                            .get(0)
                            .getTargetMessagesPerSec());
        }

        @Test
        void targetThroughputReplacesTheTypeDefaultRate() {
            TestSpec req = requested();
            req.setTargetThroughput(1000);

            assertEquals(
                    1000,
                    orchestrator.applyTypeDefaults(TestType.ENDURANCE, req).getThroughput());
        }

        @Test
        void throughputWinsWhenBothAreSet() {
            TestSpec req = requested();
            req.setThroughput(300);
            req.setTargetThroughput(2000);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, req);

            assertEquals(300, merged.getThroughput());
            assertEquals(2000, merged.getTargetThroughput());
        }

        @Test
        void consumerGroupAndFetchSettingsAreCarried() {
            TestSpec req = requested();
            req.setConsumerGroup("perf-cg");
            req.setFetchMinBytes(1_048_576);
            req.setFetchMaxWaitMs(250);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, req);

            assertEquals("perf-cg", merged.getConsumerGroup());
            assertEquals(1_048_576, merged.getFetchMinBytes());
            assertEquals(250, merged.getFetchMaxWaitMs());
        }

        @Test
        void integrityOptionsAreCarried() {
            TestSpec req = requested();
            req.setEnableIdempotence(true);
            req.setEnableTransactions(true);
            req.setEnableCrc(false);

            TestSpec merged = orchestrator.applyTypeDefaults(TestType.INTEGRITY, req);

            assertTrue(merged.isEnableIdempotence());
            assertTrue(merged.isEnableTransactions());
            assertFalse(merged.isEnableCrc());
            BenchmarkTask task =
                    orchestrator.buildTasks(TestType.INTEGRITY, merged, "run-1").get(0);
            assertTrue(task.isEnableIdempotence());
            assertTrue(task.isEnableTransactions());
            assertFalse(task.isEnableCrc());
        }

        @Test
        void loadConsumerUsesTheRequestedGroupAndFetchSettings() {
            TestSpec req = requested();
            req.setConsumerGroup("perf-cg");
            req.setFetchMinBytes(65_536);
            req.setFetchMaxWaitMs(100);

            BenchmarkTask consumer = orchestrator
                    .buildTasks(TestType.LOAD, orchestrator.applyTypeDefaults(TestType.LOAD, req), "run-1")
                    .get(1);

            assertEquals("perf-cg", consumer.getConsumerGroup());
            assertEquals("65536", consumer.getConsumerConfig().get("fetch.min.bytes"));
            assertEquals("100", consumer.getConsumerConfig().get("fetch.max.wait.ms"));
        }

        @Test
        void integrityConsumerGetsTheFetchSettings() {
            TestSpec req = requested();
            req.setFetchMinBytes(2048);

            BenchmarkTask task = orchestrator
                    .buildTasks(TestType.INTEGRITY, orchestrator.applyTypeDefaults(TestType.INTEGRITY, req), "run-1")
                    .get(0);

            assertEquals("2048", task.getConsumerConfig().get("fetch.min.bytes"));
        }

        @Test
        void loadProducerUsesTheRequestedIdempotence() {
            TestSpec req = requested();
            req.setEnableIdempotence(true);

            BenchmarkTask producer = orchestrator
                    .buildTasks(TestType.LOAD, orchestrator.applyTypeDefaults(TestType.LOAD, req), "run-1")
                    .get(0);

            assertTrue(producer.isEnableIdempotence());
            assertEquals("true", producer.getProducerConfig().get("enable.idempotence"));
        }

        @Test
        void anExplicitFalseTurnsTheClientsIdempotenceOff() {
            TestSpec req = requested();
            req.setEnableIdempotence(false);

            BenchmarkTask producer = orchestrator
                    .buildTasks(TestType.LOAD, orchestrator.applyTypeDefaults(TestType.LOAD, req), "run-1")
                    .get(0);

            assertFalse(producer.isEnableIdempotence());
            assertEquals(
                    "false",
                    producer.getProducerConfig().get("enable.idempotence"),
                    "left out, the client would turn idempotence on by itself with acks=all");
        }

        @Test
        void unrequestedSettingsLeaveTheClientDefaults() {
            TestSpec merged = orchestrator.applyTypeDefaults(TestType.LOAD, requested());
            List<BenchmarkTask> tasks = orchestrator.buildTasks(TestType.LOAD, merged, "run-1");

            assertFalse(tasks.get(0).getProducerConfig().containsKey("enable.idempotence"));
            assertTrue(tasks.get(1).getConsumerConfig().isEmpty());
            assertEquals("run-1-consume-0-group", tasks.get(1).getConsumerGroup());
            assertFalse(merged.hasConsumerGroup());
            assertFalse(merged.hasEnableIdempotence());
            assertFalse(merged.hasEnableTransactions());
            assertFalse(merged.hasEnableCrc());
        }

        @Test
        void transactionsReachTheProducerAndTheConsumerReadsCommitted() {
            TestSpec req = requested();
            req.setEnableTransactions(true);

            List<BenchmarkTask> tasks = orchestrator.buildTasks(
                    TestType.ENDURANCE, orchestrator.applyTypeDefaults(TestType.ENDURANCE, req), "run-1");

            assertTrue(tasks.get(0).isEnableTransactions());
            assertEquals("read_committed", tasks.get(1).getConsumerConfig().get("isolation.level"));
        }

        @ParameterizedTest
        @EnumSource(
                value = TestType.class,
                names = {
                    "STRESS",
                    "SPIKE",
                    "VOLUME",
                    "CAPACITY",
                    "ROUND_TRIP",
                    "TUNE_REPLICATION",
                    "TUNE_ACKS",
                    "TUNE_BATCHING",
                    "TUNE_COMPRESSION",
                    "TUNE_PARTITIONS"
                })
        void everyProducerCarriesTheProducerOptions(TestType type) {
            TestSpec req = requested();
            req.setAcks("all");
            req.setEnableIdempotence(true);
            req.setEnableTransactions(true);

            List<BenchmarkTask> tasks =
                    orchestrator.buildTasks(type, orchestrator.applyTypeDefaults(type, req), "run-1");

            assertFalse(tasks.isEmpty());
            for (BenchmarkTask task : tasks) {
                assertTrue(task.isEnableIdempotence(), type + " " + task.getTaskId());
                assertTrue(task.isEnableTransactions(), type + " " + task.getTaskId());
                assertEquals("true", task.getProducerConfig().get("enable.idempotence"));
            }
        }

        @Test
        void integrityConsumerGroupKeepsItsDefaultWhenUnset() {
            BenchmarkTask task = orchestrator
                    .buildTasks(
                            TestType.INTEGRITY,
                            orchestrator.applyTypeDefaults(TestType.INTEGRITY, requested()),
                            "run-1")
                    .get(0);

            assertEquals("integrity-cg", task.getConsumerGroup(), "the backend appends -integrity to it");
            assertTrue(task.isEnableCrc(), "CRC checks stay on unless a request turns them off");
        }
    }

    /**
     * A field a run could not honour is refused, by name, instead of being
     * accepted and ignored.
     */
    @Nested
    class InapplicableFields {

        private Map<String, String> check(TestType type, String backend, TestSpec requested) {
            return orchestrator.inapplicableFields(
                    type, backend, requested, orchestrator.applyTypeDefaults(type, requested));
        }

        private TestSpec everyField() {
            TestSpec req = new TestSpec();
            req.setAcks("all");
            req.setTargetThroughput(1000);
            req.setConsumerGroup("perf-cg");
            req.setFetchMinBytes(1024);
            req.setFetchMaxWaitMs(100);
            req.setEnableIdempotence(true);
            req.setEnableTransactions(true);
            return req;
        }

        @ParameterizedTest
        @EnumSource(
                value = TestType.class,
                names = {"LOAD", "ENDURANCE"})
        void aTypeWithAProducerAndAConsumerTakesThemAll(TestType type) {
            assertEquals(Map.of(), check(type, "native", everyField()));
        }

        @Test
        void integrityTakesThemAllAndTheCrcOption() {
            TestSpec req = everyField();
            req.setEnableCrc(false);

            assertEquals(Map.of(), check(TestType.INTEGRITY, "native", req));
        }

        @Test
        void noSpecAndAnEmptySpecAreFine() {
            assertEquals(Map.of(), check(TestType.SPIKE, "native", null));
            assertEquals(Map.of(), check(TestType.INTEGRATION_CDC, "native", new TestSpec()));
        }

        @ParameterizedTest
        @EnumSource(
                value = TestType.class,
                names = {"STRESS", "SPIKE", "VOLUME", "CAPACITY", "ROUND_TRIP", "TUNE_ACKS", "INTEGRATION_CDC"})
        void consumerSettingsNeedAConsumer(TestType type) {
            TestSpec req = new TestSpec();
            req.setConsumerGroup("perf-cg");
            req.setFetchMinBytes(1024);
            req.setFetchMaxWaitMs(100);

            Map<String, String> errors = check(type, "native", req);

            assertEquals(Set.of("consumerGroup", "fetchMinBytes", "fetchMaxWaitMs"), errors.keySet());
            assertTrue(errors.get("consumerGroup").contains("starts no consumer"), errors.toString());
        }

        @ParameterizedTest
        @EnumSource(
                value = TestType.class,
                names = {"SPIKE", "CAPACITY"})
        void anUnthrottledTypeRefusesARate(TestType type) {
            TestSpec req = new TestSpec();
            req.setThroughput(500);
            req.setTargetThroughput(500);

            Map<String, String> errors = check(type, "native", req);

            assertEquals(Set.of("throughput", "targetThroughput"), errors.keySet());
            assertTrue(errors.get("targetThroughput").contains("unthrottled"));
        }

        @Test
        void anUnthrottledTypeTakesTheUnlimitedRateItRunsAt() {
            TestSpec req = new TestSpec();
            req.setTargetThroughput(-1);

            assertEquals(Map.of(), check(TestType.CAPACITY, "native", req));
        }

        @Test
        void cdcRefusesEveryProducerOptionThatAsksForSomething() {
            TestSpec req = new TestSpec();
            req.setThroughput(500);
            req.setTargetThroughput(500);
            req.setEnableIdempotence(true);
            req.setEnableTransactions(true);

            assertEquals(
                    Set.of("throughput", "targetThroughput", "enableIdempotence", "enableTransactions"),
                    check(TestType.INTEGRATION_CDC, "native", req).keySet());
        }

        /**
         * The cases kates mcp's draft_scenario runs through its own copy of
         * these rules (TestMCPDraftScenarioRulesMatchTheBackend reads the same
         * file), so that a rule changed here and not there fails a test.
         */
        @org.junit.jupiter.api.TestFactory
        java.util.stream.Stream<org.junit.jupiter.api.DynamicTest> theCasesDraftScenarioSharesHoldHere()
                throws Exception {
            var json = new com.fasterxml.jackson.databind.ObjectMapper();
            var root = json.readTree(getClass().getResourceAsStream("/spec-applicability.json"));
            return java.util.stream.StreamSupport.stream(root.get("cases").spliterator(), false)
                    .map(c -> org.junit.jupiter.api.DynamicTest.dynamicTest(
                            c.get("name").asText(), () -> {
                                TestType type = TestType.valueOf(c.get("type").asText());
                                String backend =
                                        c.has("backend") ? c.get("backend").asText() : "native";
                                TestSpec spec = json.treeToValue(c.get("spec"), TestSpec.class);
                                Set<String> refused = new java.util.HashSet<>();
                                c.get("refused").forEach(n -> refused.add(n.asText()));

                                assertEquals(refused, check(type, backend, spec).keySet());
                            }));
        }

        @Test
        void cdcTakesTheValuesThatAskForNothing() {
            // No producer runs, so an unlimited rate and an option turned off
            // are what the run does anyway; the merged spec a CDC run shows
            // holds its type's rate, -1, and must be valid input again.
            TestSpec req = new TestSpec();
            req.setThroughput(-1);
            req.setTargetThroughput(-1);
            req.setEnableIdempotence(false);
            req.setEnableTransactions(false);
            req.setEnableCrc(false);

            assertEquals(Map.of(), check(TestType.INTEGRATION_CDC, "native", req));
        }

        @Test
        void crcChecksNeedAnIntegrityRun() {
            TestSpec on = new TestSpec();
            on.setEnableCrc(true);
            TestSpec off = new TestSpec();
            off.setEnableCrc(false);

            assertEquals(
                    Set.of("enableCrc"),
                    check(TestType.ROUND_TRIP, "native", on).keySet());
            assertEquals(Map.of(), check(TestType.ROUND_TRIP, "native", off), "no CRC check is what a LOAD run does");
        }

        @Test
        void idempotenceNeedsAcksAllAndSaysWhenTheAcksIsTheTypeDefault() {
            TestSpec req = new TestSpec();
            req.setEnableIdempotence(true);

            Map<String, String> errors = check(TestType.SPIKE, "native", req);

            assertEquals(Set.of("enableIdempotence"), errors.keySet());
            assertTrue(errors.get("enableIdempotence").contains("acks is 1 (the type's default)"), errors.toString());

            req.setAcks("all");
            assertEquals(Map.of(), check(TestType.SPIKE, "native", req));
        }

        @Test
        void transactionsNeedAcksAllAndIdempotence() {
            TestSpec acksOne = new TestSpec();
            acksOne.setAcks("1");
            acksOne.setEnableTransactions(true);
            TestSpec notIdempotent = new TestSpec();
            notIdempotent.setEnableTransactions(true);
            notIdempotent.setEnableIdempotence(false);

            assertTrue(check(TestType.LOAD, "native", acksOne)
                    .get("enableTransactions")
                    .contains("acks=all"));
            assertTrue(check(TestType.LOAD, "native", notIdempotent)
                    .get("enableTransactions")
                    .contains("always idempotent"));
        }

        @Test
        void trogdorCannotRunTransactionsButTakesTheRest() {
            TestSpec req = everyField();

            assertEquals(
                    Set.of("enableTransactions"),
                    check(TestType.LOAD, "trogdor", req).keySet());
            req.setEnableTransactions(false);
            assertEquals(Map.of(), check(TestType.LOAD, "trogdor", req));
        }

        @Test
        void executeTestRefusesBeforeTakingAPermit() {
            CreateTestRequest request = new CreateTestRequest();
            request.setType(TestType.STRESS);
            TestSpec req = new TestSpec();
            req.setConsumerGroup("perf-cg");
            request.setSpec(req);

            // More refusals than there are permits: none of them may take one.
            for (int i = 0; i < 5; i++) {
                Exception failure =
                        orchestrator.executeTest(request).asFailure().orElseThrow();
                InvalidTestSpecException invalid = assertInstanceOf(InvalidTestSpecException.class, failure);
                assertEquals(Set.of("consumerGroup"), invalid.getFieldErrors().keySet());
                assertTrue(invalid.getMessage().startsWith("spec.consumerGroup: "), invalid.getMessage());
            }
        }
    }

    /**
     * A spec the API serves or stores is valid input again. TestSpec used to
     * serialize through its getters, which answer a default for every field
     * nobody set, so a GET's spec and a schedule's stored request carried all
     * of them: enableCrc true, the fetch settings, enableIdempotence false.
     * Sent back (a replay, a schedule firing), those read as requested, and
     * the checks refused every type but INTEGRITY, whose producer they turned
     * non-idempotent.
     */
    @Nested
    class ServedAndStoredSpecsAreValidInput {

        private final com.fasterxml.jackson.databind.ObjectMapper json =
                new com.fasterxml.jackson.databind.ObjectMapper();

        @ParameterizedTest
        @EnumSource(TestType.class)
        void aScheduleRunsTheRequestItStored(TestType type) throws Exception {
            // As ScheduleResource stores the request and TestScheduler reads it.
            CreateTestRequest posted = json.readValue(
                    "{\"type\":\"" + type + "\",\"spec\":{\"numRecords\":1000}}", CreateTestRequest.class);
            CreateTestRequest fired = json.readValue(json.writeValueAsString(posted), CreateTestRequest.class);

            TestSpec spec = fired.getSpec();
            assertEquals(
                    Map.of(),
                    orchestrator.inapplicableFields(type, "native", spec, orchestrator.applyTypeDefaults(type, spec)));
            assertEquals(Map.of("numRecords", 1000), spec.explicitFields(), "only what the request set");
        }

        @ParameterizedTest
        @EnumSource(TestType.class)
        void theSpecARunShowsCanBeSentAgain(TestType type) throws Exception {
            TestSpec requested = new TestSpec();
            requested.setNumRecords(1000);
            String served = json.writeValueAsString(orchestrator.applyTypeDefaults(type, requested));

            TestSpec again = json.readValue(served, TestSpec.class);

            assertEquals(
                    Map.of(),
                    orchestrator.inapplicableFields(type, "native", again, orchestrator.applyTypeDefaults(type, again)),
                    served);
        }

        @Test
        void replayingAnIntegrityRunLeavesIdempotenceToTheClient() throws Exception {
            TestSpec requested = new TestSpec();
            requested.setNumRecords(1000);
            String served = json.writeValueAsString(orchestrator.applyTypeDefaults(TestType.INTEGRITY, requested));

            TestSpec again = json.readValue(served, TestSpec.class);
            BenchmarkTask task = orchestrator
                    .buildTasks(TestType.INTEGRITY, orchestrator.applyTypeDefaults(TestType.INTEGRITY, again), "r")
                    .get(0);

            assertFalse(task.getProducerConfig().containsKey("enable.idempotence"), served);
        }
    }

    /**
     * A scenario's base and phase specs are checked like a plain request's, and
     * what they set reaches the phases. The phases start only producers, so the
     * consumer settings and CRC checks were stored as the run's spec and never
     * used; the rate a scenario set with targetThroughput was stored as the
     * run's throughput while every phase ran at the base spec's throughput.
     */
    @Nested
    class ScenarioRequests {

        private final List<BenchmarkTask> submitted = new java.util.ArrayList<>();

        @SuppressWarnings("unchecked")
        private TestOrchestrator withBackend(String name) {
            BenchmarkBackend backend = mock(BenchmarkBackend.class);
            when(backend.name()).thenReturn(name);
            when(backend.submit(any())).thenAnswer(invocation -> {
                BenchmarkTask task = invocation.getArgument(0);
                submitted.add(task);
                return new BenchmarkHandle(name, task.getTaskId());
            });
            Instance<BenchmarkBackend> backends = mock(Instance.class);
            when(backends.stream()).thenAnswer(invocation -> java.util.stream.Stream.of(backend));
            return new TestOrchestrator(
                    mock(TopicService.class),
                    mock(TestRunRepository.class),
                    backends,
                    typeDefaults,
                    mock(BenchmarkMetrics.class),
                    mock(KatesMetrics.class),
                    new SlaEvaluator(),
                    mock(Event.class),
                    name,
                    "localhost:9092",
                    3);
        }

        private CreateTestRequest scenario(TestSpec base, com.bmscomp.kates.domain.ScenarioPhase... phases) {
            com.bmscomp.kates.domain.TestScenario scenario = new com.bmscomp.kates.domain.TestScenario();
            scenario.setName("s");
            scenario.setType(TestType.LOAD);
            scenario.setBaseSpec(base);
            scenario.setPhases(List.of(phases));
            CreateTestRequest request = new CreateTestRequest();
            request.setType(TestType.LOAD);
            request.setScenario(scenario);
            return request;
        }

        private com.bmscomp.kates.domain.ScenarioPhase phase(
                String name, com.bmscomp.kates.domain.ScenarioPhase.PhaseType type) {
            return new com.bmscomp.kates.domain.ScenarioPhase(name, type, 0, -1);
        }

        @Test
        void consumerSettingsAndCrcChecksAreRefusedByName() {
            TestSpec base = new TestSpec();
            base.setConsumerGroup("perf-cg");
            base.setFetchMinBytes(1024);
            base.setEnableCrc(true);
            com.bmscomp.kates.domain.ScenarioPhase steady =
                    phase("steady", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.STEADY);
            TestSpec phaseSpec = new TestSpec();
            phaseSpec.setFetchMaxWaitMs(100);
            steady.setSpec(phaseSpec);

            Exception failure = withBackend("native")
                    .executeTest(scenario(base, steady))
                    .asFailure()
                    .orElseThrow();

            InvalidTestSpecException invalid = assertInstanceOf(InvalidTestSpecException.class, failure);
            assertEquals(
                    Set.of(
                            "baseSpec.consumerGroup",
                            "baseSpec.fetchMinBytes",
                            "baseSpec.enableCrc",
                            "phases[0].spec.fetchMaxWaitMs"),
                    invalid.getFieldErrors().keySet());
            assertTrue(invalid.getMessage().contains("scenario.baseSpec.consumerGroup: "), invalid.getMessage());
            assertTrue(invalid.getFieldErrors().get("baseSpec.consumerGroup").contains("start no consumer"));
            assertTrue(submitted.isEmpty());
        }

        @Test
        void targetThroughputSetsThePhaseRate() {
            TestSpec base = new TestSpec();
            base.setTargetThroughput(5000);
            com.bmscomp.kates.domain.ScenarioPhase slow =
                    phase("slow", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.STEADY);
            TestSpec slowSpec = new TestSpec();
            slowSpec.setTargetThroughput(700);
            slow.setSpec(slowSpec);

            assertTrue(withBackend("native")
                    .executeTest(scenario(
                            base, phase("steady", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.STEADY), slow))
                    .isSuccess());

            assertEquals(2, submitted.size());
            assertEquals(5000, submitted.get(0).getTargetMessagesPerSec());
            assertEquals(700, submitted.get(1).getTargetMessagesPerSec());
        }

        @Test
        void theProducerOptionsReachEveryPhase() {
            TestSpec base = new TestSpec();
            base.setAcks("all");
            base.setThroughput(1000);
            base.setEnableIdempotence(true);
            base.setEnableTransactions(true);
            com.bmscomp.kates.domain.ScenarioPhase ramp =
                    phase("ramp", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.RAMP);
            ramp.setRampSteps(2);

            assertTrue(withBackend("native")
                    .executeTest(scenario(
                            base,
                            phase("steady", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.STEADY),
                            ramp,
                            phase("burst", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.SPIKE)))
                    .isSuccess());

            assertEquals(4, submitted.size());
            for (BenchmarkTask task : submitted) {
                assertTrue(task.isEnableIdempotence(), task.getTaskId());
                assertTrue(task.isEnableTransactions(), task.getTaskId());
                assertEquals("true", task.getProducerConfig().get("enable.idempotence"), task.getTaskId());
            }
        }

        @Test
        void transactionsOnTrogdorAreRefused() {
            TestSpec base = new TestSpec();
            base.setEnableTransactions(true);

            Exception failure = withBackend("trogdor")
                    .executeTest(
                            scenario(base, phase("steady", com.bmscomp.kates.domain.ScenarioPhase.PhaseType.STEADY)))
                    .asFailure()
                    .orElseThrow();

            assertEquals(
                    Set.of("baseSpec.enableTransactions"),
                    assertInstanceOf(InvalidTestSpecException.class, failure)
                            .getFieldErrors()
                            .keySet());
        }
    }
}
