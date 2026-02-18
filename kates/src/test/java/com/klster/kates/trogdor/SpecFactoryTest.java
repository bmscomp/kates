package com.klster.kates.trogdor;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import jakarta.inject.Inject;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import com.klster.kates.trogdor.spec.ConsumeBenchSpec;
import com.klster.kates.trogdor.spec.ProduceBenchSpec;
import com.klster.kates.trogdor.spec.RoundTripWorkloadSpec;
import com.klster.kates.trogdor.spec.TrogdorSpec;

@QuarkusTest
class SpecFactoryTest {

    @Inject
    SpecFactory specFactory;

    @Test
    void loadTestProducesProducerAndConsumerSpecs() {
        TestSpec spec = new TestSpec();
        spec.setNumProducers(3);
        spec.setNumConsumers(2);
        spec.setNumRecords(300_000);
        spec.setThroughput(15_000);

        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "test-run");

        assertEquals(5, specs.size(), "3 producers + 2 consumers");
        long producerCount =
                specs.stream().filter(s -> s instanceof ProduceBenchSpec).count();
        long consumerCount =
                specs.stream().filter(s -> s instanceof ConsumeBenchSpec).count();
        assertEquals(3, producerCount);
        assertEquals(2, consumerCount);
    }

    @Test
    void loadTestSplitsRecordsAcrossProducers() {
        TestSpec spec = new TestSpec();
        spec.setNumProducers(4);
        spec.setNumConsumers(0);
        spec.setNumRecords(400_000);

        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "split-run");

        ProduceBenchSpec first = (ProduceBenchSpec) specs.get(0);
        assertEquals(100_000, first.getMaxMessages());
    }

    @Test
    void loadTestSplitsThroughputAcrossProducers() {
        TestSpec spec = new TestSpec();
        spec.setNumProducers(5);
        spec.setNumConsumers(0);
        spec.setThroughput(50_000);
        spec.setNumRecords(500_000);

        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "tp-run");

        ProduceBenchSpec first = (ProduceBenchSpec) specs.get(0);
        assertEquals(10_000, first.getTargetMessagesPerSec());
    }

    @Test
    void loadTestConsumerGroupContainsRunId() {
        TestSpec spec = new TestSpec();
        spec.setNumProducers(1);
        spec.setNumConsumers(1);

        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "abc123");
        ConsumeBenchSpec consumer = (ConsumeBenchSpec) specs.get(1);
        assertTrue(consumer.getConsumerGroup().contains("abc123"));
    }

    @Test
    void stressTestProducesRampSteps() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.STRESS, spec, "stress-run");

        assertEquals(5, specs.size(), "5 ramp steps: 10K, 25K, 50K, 100K, unlimited");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void stressTestHasCorrectThroughputValues() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.STRESS, spec, "stress-values");

        int[] expected = {10_000, 25_000, 50_000, 100_000, -1};
        for (int i = 0; i < specs.size(); i++) {
            ProduceBenchSpec p = (ProduceBenchSpec) specs.get(i);
            assertEquals(
                    expected[i], p.getTargetMessagesPerSec(), "Step " + i + " should have throughput " + expected[i]);
        }
    }

    @Test
    void stressTestSplitsDurationEvenly() {
        TestSpec spec = new TestSpec();
        spec.setDurationMs(500_000);
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.STRESS, spec, "dur-run");

        for (TrogdorSpec s : specs) {
            assertEquals(100_000, s.getDurationMs());
        }
    }

    @Test
    void spikeTestProducesThreePhases() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.SPIKE, spec, "spike-run");

        assertEquals(5, specs.size(), "baseline + 3 burst producers + recovery");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void spikeTestBaselineAt1KMsgPerSec() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.SPIKE, spec, "spike-baseline");

        ProduceBenchSpec baseline = (ProduceBenchSpec) specs.get(0);
        assertEquals(1_000, baseline.getTargetMessagesPerSec());
    }

    @Test
    void spikeTestBurstAtUnlimited() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.SPIKE, spec, "spike-burst");

        for (int i = 1; i <= 3; i++) {
            ProduceBenchSpec burst = (ProduceBenchSpec) specs.get(i);
            assertEquals(-1, burst.getTargetMessagesPerSec(), "Burst producer " + i + " should be unlimited");
        }
    }

    @Test
    void spikeTestRecoveryAt1KMsgPerSec() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.SPIKE, spec, "spike-recovery");

        ProduceBenchSpec recovery = (ProduceBenchSpec) specs.get(4);
        assertEquals(1_000, recovery.getTargetMessagesPerSec());
    }

    @Test
    void enduranceTestProducesProducerAndConsumer() {
        TestSpec spec = new TestSpec();
        spec.setDurationMs(7_200_000);
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.ENDURANCE, spec, "endurance-run");

        assertEquals(2, specs.size());
        assertInstanceOf(ProduceBenchSpec.class, specs.get(0));
        assertInstanceOf(ConsumeBenchSpec.class, specs.get(1));
    }

    @Test
    void enduranceTestEnforcesMinimumOneHourDuration() {
        TestSpec spec = new TestSpec();
        spec.setDurationMs(60_000);
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.ENDURANCE, spec, "min-dur");

        assertEquals(3_600_000, specs.get(0).getDurationMs(), "Duration should be at least 1 hour");
    }

    @Test
    void enduranceTestMaxMessagesMatchesThroughputTimesDuration() {
        TestSpec spec = new TestSpec();
        spec.setDurationMs(7_200_000);
        spec.setThroughput(10_000);
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.ENDURANCE, spec, "calc-run");

        ProduceBenchSpec produce = (ProduceBenchSpec) specs.get(0);
        long expectedMessages = 10_000L * (7_200_000L / 1000);
        assertEquals(expectedMessages, produce.getMaxMessages());
    }

    @Test
    void volumeTestProducesTwoSpecs() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.VOLUME, spec, "volume-run");

        assertEquals(2, specs.size(), "large messages + high count");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void volumeTestLargeMessagesUses100KBRecordSize() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.VOLUME, spec, "vol-size");

        ProduceBenchSpec largeMsg = (ProduceBenchSpec) specs.get(0);
        assertEquals(102_400, largeMsg.getValueGenerator().getSize());
        assertEquals("1048576", largeMsg.getProducerConf().get("max.request.size"));
    }

    @Test
    void volumeTestHighCountUses5MMessages() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.VOLUME, spec, "vol-count");

        ProduceBenchSpec highCount = (ProduceBenchSpec) specs.get(1);
        assertEquals(5_000_000, highCount.getMaxMessages());
    }

    @Test
    void capacityTestProducesProbeSteps() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.CAPACITY, spec, "capacity-run");

        assertEquals(6, specs.size(), "6 probe steps: 5K, 10K, 20K, 40K, 80K, unlimited");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void capacityTestHasCorrectProbeValues() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.CAPACITY, spec, "cap-values");

        int[] expected = {5_000, 10_000, 20_000, 40_000, 80_000, -1};
        for (int i = 0; i < specs.size(); i++) {
            ProduceBenchSpec p = (ProduceBenchSpec) specs.get(i);
            assertEquals(
                    expected[i],
                    p.getTargetMessagesPerSec(),
                    "Probe step " + i + " should have throughput " + expected[i]);
        }
    }

    @Test
    void roundTripTestProducesRoundTripSpec() {
        TestSpec spec = new TestSpec();
        spec.setThroughput(1_000);
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.ROUND_TRIP, spec, "rt-run");

        assertEquals(1, specs.size());
        assertInstanceOf(RoundTripWorkloadSpec.class, specs.get(0));
    }

    @Test
    void roundTripTestPropagatesAcks() {
        TestSpec spec = new TestSpec();
        spec.setAcks("1");
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.ROUND_TRIP, spec, "rt-acks");

        RoundTripWorkloadSpec rt = (RoundTripWorkloadSpec) specs.get(0);
        assertEquals("1", rt.getProducerConf().get("acks"));
    }

    @Test
    void allTypesPopulateBootstrapServers() {
        TestSpec spec = new TestSpec();
        for (TestType type : TestType.values()) {
            List<TrogdorSpec> specs = specFactory.buildSpecs(type, spec, "bs-test");
            TrogdorSpec first = specs.get(0);

            if (first instanceof ProduceBenchSpec p) {
                assertNotNull(p.getBootstrapServers(), type + " should set bootstrapServers");
            } else if (first instanceof ConsumeBenchSpec c) {
                assertNotNull(c.getBootstrapServers(), type + " should set bootstrapServers");
            } else if (first instanceof RoundTripWorkloadSpec r) {
                assertNotNull(r.getBootstrapServers(), type + " should set bootstrapServers");
            }
        }
    }

    @Test
    void customTopicNameIsUsed() {
        TestSpec spec = new TestSpec();
        spec.setTopic("my-custom-topic");
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "topic-test");

        ProduceBenchSpec produce = (ProduceBenchSpec) specs.get(0);
        assertTrue(produce.getActiveTopics().containsKey("my-custom-topic[0-2]"));
    }

    @Test
    void defaultTopicNameFallsBackToTypeName() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "fallback-test");

        ProduceBenchSpec produce = (ProduceBenchSpec) specs.get(0);
        assertTrue(produce.getActiveTopics().containsKey("load-test[0-2]"));
    }

    @Test
    void produceBenchSpecHasCorrectJsonClass() {
        TestSpec spec = new TestSpec();
        spec.setTopic("custom-topic");
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "class-test");

        ProduceBenchSpec produce = (ProduceBenchSpec) specs.get(0);
        assertEquals("org.apache.kafka.trogdor.workload.ProduceBenchSpec", produce.getSpecClass());
    }

    @Test
    void producerConfIsPopulated() {
        TestSpec spec = new TestSpec();
        spec.setAcks("all");
        spec.setBatchSize(131072);
        spec.setLingerMs(10);
        spec.setCompressionType("lz4");

        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.LOAD, spec, "conf-test");

        ProduceBenchSpec produce = (ProduceBenchSpec) specs.get(0);
        assertEquals("all", produce.getProducerConf().get("acks"));
        assertEquals("131072", produce.getProducerConf().get("batch.size"));
        assertEquals("10", produce.getProducerConf().get("linger.ms"));
        assertEquals("lz4", produce.getProducerConf().get("compression.type"));
    }
}
