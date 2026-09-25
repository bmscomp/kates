package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.trogdor.TrogdorClient;
import com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec;
import com.bmscomp.kates.trogdor.spec.ProduceBenchSpec;
import com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec;

/**
 * What a task's client settings become in the Trogdor spec. They were left out
 * entirely, so a run on this backend used the Kafka client's defaults whatever
 * its request said.
 */
class TrogdorBackendTest {

    private final TrogdorBackend backend = new TrogdorBackend(mock(TrogdorClient.class));

    private static final Map<String, String> PRODUCER = Map.of(
            "bootstrap.servers", "broker:9092",
            "acks", "all",
            "compression.type", "zstd",
            "enable.idempotence", "false");

    @Test
    void aProduceTaskCarriesItsProducerSettings() {
        BenchmarkTask task = BenchmarkTask.builder("t-produce", BenchmarkTask.WorkloadType.PRODUCE)
                .producerConfig(PRODUCER)
                .targetMessagesPerSec(750)
                .build();

        ProduceBenchSpec spec = (ProduceBenchSpec) backend.toTrogdorSpec(task);

        assertEquals("broker:9092", spec.getBootstrapServers());
        assertEquals(750, spec.getTargetMessagesPerSec());
        assertEquals(
                Map.of("acks", "all", "compression.type", "zstd", "enable.idempotence", "false"),
                spec.getProducerConf(),
                "the bootstrap servers have their own field");
    }

    @Test
    void aConsumeTaskCarriesItsGroupAndFetchSettings() {
        BenchmarkTask task = BenchmarkTask.builder("t-consume", BenchmarkTask.WorkloadType.CONSUME)
                .consumerGroup("perf-cg")
                .consumerConfig(Map.of("fetch.min.bytes", "65536", "fetch.max.wait.ms", "100"))
                .build();

        ConsumeBenchSpec spec = (ConsumeBenchSpec) backend.toTrogdorSpec(task);

        assertEquals("perf-cg", spec.getConsumerGroup());
        assertEquals(Map.of("fetch.min.bytes", "65536", "fetch.max.wait.ms", "100"), spec.getConsumerConf());
    }

    @Test
    void aRoundTripTaskCarriesBoth() {
        BenchmarkTask task = BenchmarkTask.builder("t-rt", BenchmarkTask.WorkloadType.ROUND_TRIP)
                .producerConfig(PRODUCER)
                .build();

        RoundTripWorkloadSpec spec = (RoundTripWorkloadSpec) backend.toTrogdorSpec(task);

        assertEquals("false", spec.getProducerConf().get("enable.idempotence"));
        assertFalse(spec.getProducerConf().containsKey("bootstrap.servers"));
        assertTrue(spec.getConsumerConf().isEmpty());
    }
}
