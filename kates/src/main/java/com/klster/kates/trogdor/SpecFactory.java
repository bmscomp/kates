package com.klster.kates.trogdor;

import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import com.klster.kates.trogdor.spec.ConsumeBenchSpec;
import com.klster.kates.trogdor.spec.ProduceBenchSpec;
import com.klster.kates.trogdor.spec.RoundTripWorkloadSpec;
import com.klster.kates.trogdor.spec.TrogdorSpec;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

@ApplicationScoped
public class SpecFactory {

    private final String bootstrapServers;

    @Inject
    public SpecFactory(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.bootstrapServers = bootstrapServers;
    }

    public List<TrogdorSpec> buildSpecs(TestType type, TestSpec spec, String runId) {
        return switch (type) {
            case LOAD -> buildLoadSpecs(spec, runId);
            case STRESS -> buildStressSpecs(spec, runId);
            case SPIKE -> buildSpikeSpecs(spec, runId);
            case ENDURANCE -> buildEnduranceSpecs(spec, runId);
            case VOLUME -> buildVolumeSpecs(spec, runId);
            case CAPACITY -> buildCapacitySpecs(spec, runId);
            case ROUND_TRIP -> buildRoundTripSpecs(spec, runId);
            case INTEGRITY -> buildLoadSpecs(spec, runId);
        };
    }

    private List<TrogdorSpec> buildLoadSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "load-test");
        int effectiveThroughput = effectiveThroughput(spec);
        int perProducerRecords = spec.getNumRecords() / spec.getNumProducers();
        int perProducerThroughput = effectiveThroughput > 0
                ? effectiveThroughput / spec.getNumProducers()
                : -1;

        for (int i = 0; i < spec.getNumProducers(); i++) {
            ProduceBenchSpec produce = ProduceBenchSpec.create(
                    bootstrapServers, topic, spec.getPartitions(),
                    perProducerThroughput, perProducerRecords,
                    spec.getDurationMs(), spec.getRecordSize());
            applyProducerConf(produce, spec);
            specs.add(produce);
        }

        String group = consumerGroupName(spec, "load-group-" + runId);
        for (int i = 0; i < spec.getNumConsumers(); i++) {
            ConsumeBenchSpec consume = ConsumeBenchSpec.create(
                    bootstrapServers, topic, spec.getPartitions(),
                    perProducerRecords, spec.getDurationMs(), group);
            applyConsumerConf(consume, spec);
            specs.add(consume);
        }

        return specs;
    }

    private List<TrogdorSpec> buildStressSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "stress-test");
        int[] rampSteps = {10_000, 25_000, 50_000, 100_000, -1};

        for (int step : rampSteps) {
            ProduceBenchSpec produce = ProduceBenchSpec.create(
                    bootstrapServers, topic, spec.getPartitions(),
                    step, 500_000,
                    spec.getDurationMs() / rampSteps.length,
                    spec.getRecordSize());
            applyProducerConf(produce, spec);
            produce.getProducerConf().put("batch.size", "131072");
            produce.getProducerConf().put("linger.ms", "10");
            specs.add(produce);
        }

        return specs;
    }

    private List<TrogdorSpec> buildSpikeSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "spike-test");

        // Phase 1: baseline (60s at 1K msg/sec)
        ProduceBenchSpec baseline = ProduceBenchSpec.create(
                bootstrapServers, topic, spec.getPartitions(),
                1_000, 60_000, 60_000, spec.getRecordSize());
        applyProducerConf(baseline, spec);
        specs.add(baseline);

        // Phase 2: spike (120s at unlimited, 3 concurrent producers)
        for (int i = 0; i < 3; i++) {
            ProduceBenchSpec burst = ProduceBenchSpec.create(
                    bootstrapServers, topic, spec.getPartitions(),
                    -1, 500_000, 120_000, spec.getRecordSize());
            applyProducerConf(burst, spec);
            burst.getProducerConf().put("batch.size", "131072");
            burst.getProducerConf().put("linger.ms", "10");
            specs.add(burst);
        }

        // Phase 3: recovery baseline (60s at 1K msg/sec)
        ProduceBenchSpec recovery = ProduceBenchSpec.create(
                bootstrapServers, topic, spec.getPartitions(),
                1_000, 60_000, 60_000, spec.getRecordSize());
        applyProducerConf(recovery, spec);
        specs.add(recovery);

        return specs;
    }

    private List<TrogdorSpec> buildEnduranceSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "endurance-test");

        long duration = Math.max(spec.getDurationMs(), 3_600_000);
        int throughput = effectiveThroughput(spec) > 0 ? effectiveThroughput(spec) : 5_000;
        long maxMessages = throughput * (duration / 1000);

        ProduceBenchSpec produce = ProduceBenchSpec.create(
                bootstrapServers, topic, spec.getPartitions(),
                throughput, maxMessages, duration, spec.getRecordSize());
        applyProducerConf(produce, spec);
        specs.add(produce);

        String group = consumerGroupName(spec, "endurance-group-" + runId);
        ConsumeBenchSpec consume = ConsumeBenchSpec.create(
                bootstrapServers, topic, spec.getPartitions(),
                maxMessages, duration, group);
        applyConsumerConf(consume, spec);
        specs.add(consume);

        return specs;
    }

    private List<TrogdorSpec> buildVolumeSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "volume-test");

        // Large messages: 50K × 100KB
        ProduceBenchSpec largeMsg = ProduceBenchSpec.create(
                bootstrapServers, topic + "-large", spec.getPartitions(),
                -1, 50_000, spec.getDurationMs(), 102_400);
        applyProducerConf(largeMsg, spec);
        largeMsg.getProducerConf().put("max.request.size", "1048576");
        largeMsg.getProducerConf().put("batch.size", "131072");
        specs.add(largeMsg);

        // High count: 5M × 1KB
        ProduceBenchSpec highCount = ProduceBenchSpec.create(
                bootstrapServers, topic + "-count", spec.getPartitions(),
                -1, 5_000_000, spec.getDurationMs(), 1024);
        applyProducerConf(highCount, spec);
        highCount.getProducerConf().put("batch.size", "131072");
        highCount.getProducerConf().put("linger.ms", "10");
        specs.add(highCount);

        return specs;
    }

    private List<TrogdorSpec> buildCapacitySpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "capacity-test");
        int[] probeSteps = {5_000, 10_000, 20_000, 40_000, 80_000, -1};

        for (int step : probeSteps) {
            ProduceBenchSpec produce = ProduceBenchSpec.create(
                    bootstrapServers, topic, spec.getPartitions(),
                    step, 200_000,
                    spec.getDurationMs() / probeSteps.length,
                    spec.getRecordSize());
            applyProducerConf(produce, spec);
            specs.add(produce);
        }

        return specs;
    }

    private List<TrogdorSpec> buildRoundTripSpecs(TestSpec spec, String runId) {
        List<TrogdorSpec> specs = new ArrayList<>();
        String topic = topicName(spec, "roundtrip-test");

        int throughput = effectiveThroughput(spec) > 0 ? effectiveThroughput(spec) : 1_000;

        RoundTripWorkloadSpec rt = RoundTripWorkloadSpec.create(
                bootstrapServers, topic, spec.getPartitions(),
                throughput, spec.getNumRecords(), spec.getDurationMs(),
                spec.getRecordSize());
        rt.getProducerConf().put("acks", spec.getAcks());
        specs.add(rt);

        return specs;
    }

    private void applyProducerConf(ProduceBenchSpec produce, TestSpec spec) {
        Map<String, String> conf = produce.getProducerConf();
        conf.put("acks", spec.getAcks());
        conf.put("batch.size", String.valueOf(spec.getBatchSize()));
        conf.put("linger.ms", String.valueOf(spec.getLingerMs()));
        conf.put("compression.type", spec.getCompressionType());
    }

    private void applyConsumerConf(ConsumeBenchSpec consume, TestSpec spec) {
        Map<String, String> conf = consume.getConsumerConf();
        if (spec.getFetchMinBytes() > 1) {
            conf.put("fetch.min.bytes", String.valueOf(spec.getFetchMinBytes()));
        }
        if (spec.getFetchMaxWaitMs() != 500) {
            conf.put("fetch.max.wait.ms", String.valueOf(spec.getFetchMaxWaitMs()));
        }
    }

    private String consumerGroupName(TestSpec spec, String defaultName) {
        return spec.getConsumerGroup() != null ? spec.getConsumerGroup() : defaultName;
    }

    private int effectiveThroughput(TestSpec spec) {
        if (spec.getTargetThroughput() > 0) return spec.getTargetThroughput();
        return spec.getThroughput();
    }

    private String topicName(TestSpec spec, String defaultName) {
        return spec.getTopic() != null ? spec.getTopic() : defaultName;
    }
}
