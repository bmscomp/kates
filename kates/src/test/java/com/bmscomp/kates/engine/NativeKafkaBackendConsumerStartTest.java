package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

import org.apache.kafka.clients.consumer.MockConsumer;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.clients.consumer.OffsetAndTimestamp;
import org.apache.kafka.clients.consumer.OffsetResetStrategy;
import org.apache.kafka.common.TopicPartition;
import org.junit.jupiter.api.Test;

/**
 * Where a native consumer starts a partition. A group with no committed offset
 * was left to earliest, so it read every record earlier runs had left on the
 * topic before its own, and the INTEGRITY default, one group every run shared,
 * resumed wherever another run had committed.
 */
class NativeKafkaBackendConsumerStartTest {

    private static final String TOPIC = "load-test";
    private static final TopicPartition P0 = new TopicPartition(TOPIC, 0);
    private static final TopicPartition P1 = new TopicPartition(TOPIC, 1);
    private static final TopicPartition P2 = new TopicPartition(TOPIC, 2);
    private static final long SUBMITTED_AT_MS = 1_700_000_000_000L;

    /**
     * MockConsumer has no offsetsForTimes. This one answers from {@link #found},
     * keeps the timestamps it was asked for, and first runs {@link #onLookup},
     * which stands for what the producer writes while the lookup is in flight.
     */
    private static final class Broker extends MockConsumer<byte[], byte[]> {
        final Map<TopicPartition, Long> found = new HashMap<>();
        final Map<TopicPartition, Long> asked = new HashMap<>();
        Runnable onLookup = () -> {};

        Broker() {
            // EARLIEST, so a partition the listener left alone would read from 0.
            super(OffsetResetStrategy.EARLIEST);
            updateBeginningOffsets(Map.of(P0, 0L, P1, 0L, P2, 0L));
        }

        @Override
        public synchronized Map<TopicPartition, OffsetAndTimestamp> offsetsForTimes(Map<TopicPartition, Long> times) {
            onLookup.run();
            asked.putAll(times);
            // The real client maps a partition with nothing that late to null.
            Map<TopicPartition, OffsetAndTimestamp> result = new HashMap<>();
            times.forEach((tp, ts) ->
                    result.put(tp, found.containsKey(tp) ? new OffsetAndTimestamp(found.get(tp), ts) : null));
            return result;
        }

        void subscribeSinceSubmit() {
            subscribe(List.of(TOPIC), NativeKafkaBackend.sinceSubmit(this, SUBMITTED_AT_MS));
        }
    }

    @Test
    void aPartitionWithNoCommittedOffsetStartsAtTheFirstRecordSinceSubmit() {
        Broker consumer = new Broker();
        consumer.updateEndOffsets(Map.of(P0, 8L));
        consumer.found.put(P0, 5L);

        consumer.subscribeSinceSubmit();
        consumer.rebalance(List.of(P0));

        assertEquals(5, consumer.position(P0));
        assertEquals(Map.of(P0, SUBMITTED_AT_MS - 1000), consumer.asked, "a second before the task was submitted");
    }

    @Test
    void aPartitionWithNothingSinceStartsAtTheEndReadBeforeTheLookup() {
        Broker consumer = new Broker();
        consumer.updateEndOffsets(Map.of(P1, 3L));
        consumer.onLookup = () -> consumer.updateEndOffsets(Map.of(P1, 9L));

        consumer.subscribeSinceSubmit();
        consumer.rebalance(List.of(P1));

        assertEquals(3, consumer.position(P1), "the records written during the lookup are still read");
    }

    @Test
    void aCommittedPartitionResumesFromIt() {
        Broker consumer = new Broker();
        consumer.updateEndOffsets(Map.of(P2, 9L));
        consumer.found.put(P2, 4L);

        consumer.subscribeSinceSubmit();
        // After subscribe: MockConsumer forgets every commit when it subscribes.
        consumer.commitSync(Map.of(P2, new OffsetAndMetadata(7)));
        consumer.rebalance(List.of(P2));

        assertEquals(7, consumer.position(P2));
        assertFalse(consumer.asked.containsKey(P2), "a committed partition is not looked up");
    }
}
