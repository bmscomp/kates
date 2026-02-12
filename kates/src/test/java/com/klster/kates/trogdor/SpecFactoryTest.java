package com.klster.kates.trogdor;

import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import com.klster.kates.trogdor.spec.ConsumeBenchSpec;
import com.klster.kates.trogdor.spec.ProduceBenchSpec;
import com.klster.kates.trogdor.spec.RoundTripWorkloadSpec;
import com.klster.kates.trogdor.spec.TrogdorSpec;
import io.quarkus.test.junit.QuarkusTest;
import jakarta.inject.Inject;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

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
        long producerCount = specs.stream().filter(s -> s instanceof ProduceBenchSpec).count();
        long consumerCount = specs.stream().filter(s -> s instanceof ConsumeBenchSpec).count();
        assertEquals(3, producerCount);
        assertEquals(2, consumerCount);
    }

    @Test
    void stressTestProducesRampSteps() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.STRESS, spec, "stress-run");

        assertEquals(5, specs.size(), "5 ramp steps: 10K, 25K, 50K, 100K, unlimited");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void spikeTestProducesThreePhases() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.SPIKE, spec, "spike-run");

        assertEquals(5, specs.size(), "baseline + 3 burst producers + recovery");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
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
    void volumeTestProducesTwoSpecs() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.VOLUME, spec, "volume-run");

        assertEquals(2, specs.size(), "large messages + high count");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
    }

    @Test
    void capacityTestProducesProbeSteps() {
        TestSpec spec = new TestSpec();
        List<TrogdorSpec> specs = specFactory.buildSpecs(TestType.CAPACITY, spec, "capacity-run");

        assertEquals(6, specs.size(), "6 probe steps: 5K, 10K, 20K, 40K, 80K, unlimited");
        assertTrue(specs.stream().allMatch(s -> s instanceof ProduceBenchSpec));
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
