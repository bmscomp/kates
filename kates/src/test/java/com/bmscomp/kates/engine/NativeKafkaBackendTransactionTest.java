package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.TimeUnit;

import org.junit.jupiter.api.Test;

/**
 * When a transactional producer commits. It used to commit every 100 records
 * and at no other time, so at the lowest rate a request can set, 1 record/s, a
 * transaction stayed open for 100 s: past the Kafka client's 60 s
 * transaction.timeout.ms, after which the coordinator aborts it and the next
 * commit fails the task.
 */
class NativeKafkaBackendTransactionTest {

    private static final long OPENED = 1_000_000_000L;

    @Test
    void aFullBatchIsCommitted() {
        assertFalse(NativeKafkaBackend.transactionDue(NativeKafkaBackend.TX_BATCH_RECORDS - 1, OPENED, OPENED));
        assertTrue(NativeKafkaBackend.transactionDue(NativeKafkaBackend.TX_BATCH_RECORDS, OPENED, OPENED));
    }

    @Test
    void aSlowTransactionIsCommittedBeforeTheClientTimesItOut() {
        long tenSeconds = TimeUnit.SECONDS.toNanos(10);

        assertFalse(NativeKafkaBackend.transactionDue(3, OPENED, OPENED + tenSeconds - 1));
        assertTrue(NativeKafkaBackend.transactionDue(3, OPENED, OPENED + tenSeconds));
        assertTrue(
                NativeKafkaBackend.TX_MAX_OPEN_NANOS < TimeUnit.SECONDS.toNanos(60),
                "well inside the client's default transaction.timeout.ms");
    }

    @Test
    void atOneRecordASecondNoTransactionOutlivesTheTimeout() {
        // The loop checks before each send; at 1 record/s the check that
        // commits comes at most a second after the transaction is due.
        long now = OPENED;
        long opened = OPENED;
        long inTransaction = 0;
        long longest = 0;
        for (int second = 0; second < 300; second++) {
            if (NativeKafkaBackend.transactionDue(inTransaction, opened, now)) {
                longest = Math.max(longest, now - opened);
                opened = now;
                inTransaction = 0;
            }
            inTransaction++;
            now += TimeUnit.SECONDS.toNanos(1);
        }
        assertTrue(longest <= NativeKafkaBackend.TX_MAX_OPEN_NANOS + TimeUnit.SECONDS.toNanos(1), "longest " + longest);
    }
}
