package com.klster.kates.engine;

import com.klster.kates.domain.IntegrityResult;
import com.klster.kates.domain.LostRange;

import java.time.Duration;
import java.util.ArrayList;
import java.util.BitSet;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;
import java.util.logging.Logger;

/**
 * Post-benchmark verifier that reconciles produced records against consumed records
 * to compute data loss, RPO, and both producer-side and consumer-side RTO.
 *
 * <p>Usage:
 * <pre>
 *   var verifier = new DataIntegrityVerifier(ackTracker);
 *   verifier.recordConsumed(seq, crcValid, partition);
 *   IntegrityResult result = verifier.verify(chaosStartNanos, true, true, false, false);
 * </pre>
 *
 * <p>All timestamps use {@link System#nanoTime()} (monotonic clock) for accuracy.
 */
public class DataIntegrityVerifier {

    private static final Logger LOG = Logger.getLogger(DataIntegrityVerifier.class.getName());

    private final AckTracker ackTracker;
    private final BitSet consumedSet = new BitSet();
    private final Map<Long, Integer> consumedCounts = new HashMap<>();
    private final AtomicLong totalConsumed = new AtomicLong();
    private final AtomicLong crcFailures = new AtomicLong();
    private final AtomicLong outOfOrderCount = new AtomicLong();
    private final ConcurrentHashMap<Integer, Long> perPartitionLastSeq = new ConcurrentHashMap<>();

    private volatile long firstConsumeGapNanos = -1;
    private volatile long firstConsumeRecoveryNanos = -1;
    private volatile boolean inConsumeGap = false;
    private long previousConsumedSeq = -1;

    public DataIntegrityVerifier(AckTracker ackTracker) {
        this.ackTracker = ackTracker;
    }

    /**
     * Record a consumed sequence number with optional CRC and ordering checks.
     *
     * @param sequence the sequence number from the payload
     * @param crcValid whether the CRC32 checksum matched
     * @param partition the Kafka partition this record came from
     */
    public void recordConsumed(long sequence, boolean crcValid, int partition) {
        int seq = (int) sequence;
        consumedSet.set(seq);
        consumedCounts.merge(sequence, 1, Integer::sum);
        totalConsumed.incrementAndGet();

        if (!crcValid) {
            crcFailures.incrementAndGet();
        }

        Long lastSeq = perPartitionLastSeq.get(partition);
        if (lastSeq != null && sequence < lastSeq) {
            outOfOrderCount.incrementAndGet();
        }
        perPartitionLastSeq.put(partition, sequence);

        if (previousConsumedSeq >= 0 && sequence > previousConsumedSeq + 1) {
            if (!inConsumeGap) {
                firstConsumeGapNanos = System.nanoTime();
                inConsumeGap = true;
            }
        } else if (inConsumeGap) {
            firstConsumeRecoveryNanos = System.nanoTime();
            inConsumeGap = false;
        }
        previousConsumedSeq = sequence;
    }

    /**
     * Legacy overload for callers that don't need CRC/ordering.
     */
    public void recordConsumed(long sequence) {
        recordConsumed(sequence, true, 0);
    }

    /**
     * Performs the reconciliation and returns the integrity result.
     *
     * @param chaosStartNanos monotonic nano timestamp for RPO computation. Pass -1 if no chaos.
     * @param crcVerified whether CRC checking was enabled
     * @param orderingVerified whether ordering checking was enabled
     * @param idempotenceEnabled whether idempotent producer was used
     * @param transactionsEnabled whether transactional producer was used
     */
    public IntegrityResult verify(long chaosStartNanos,
                                   boolean crcVerified,
                                   boolean orderingVerified,
                                   boolean idempotenceEnabled,
                                   boolean transactionsEnabled) {
        BitSet ackedSet = ackTracker.getAckedSet();

        // lost = acked but not consumed
        BitSet lostSet = (BitSet) ackedSet.clone();
        lostSet.andNot(consumedSet);
        long lostCount = lostSet.cardinality();

        // duplicates
        long duplicates = consumedCounts.values().stream()
                .filter(c -> c > 1)
                .mapToLong(c -> c - 1)
                .sum();

        List<LostRange> lostRanges = computeLostRanges(lostSet);

        // producer RTO
        long maxProducerRtoNanos = ackTracker.maxRtoNanos();
        Duration producerRto = maxProducerRtoNanos > 0 ? Duration.ofNanos(maxProducerRtoNanos) : Duration.ZERO;

        // consumer RTO
        Duration consumerRto = Duration.ZERO;
        if (firstConsumeGapNanos > 0 && firstConsumeRecoveryNanos > 0) {
            long consumerRtoNanos = firstConsumeRecoveryNanos - firstConsumeGapNanos;
            if (consumerRtoNanos > 0) {
                consumerRto = Duration.ofNanos(consumerRtoNanos);
            }
        }

        Duration maxRto = producerRto.compareTo(consumerRto) >= 0 ? producerRto : consumerRto;

        // RPO
        Duration rpo = Duration.ZERO;
        if (chaosStartNanos > 0 && ackTracker.getLastAckedSendNanos() > 0) {
            long rpoNanos = chaosStartNanos - ackTracker.getLastAckedSendNanos();
            if (rpoNanos > 0) {
                rpo = Duration.ofNanos(rpoNanos);
            }
        }

        long totalSent = ackTracker.getTotalSent();
        double lossPercent = totalSent > 0 ? (double) lostCount / totalSent * 100.0 : 0.0;

        List<AckTracker.FailureWindow> failureWindows = ackTracker.getCompletedWindows();

        IntegrityResult result = new IntegrityResult(
                totalSent,
                ackTracker.getTotalAcked(),
                totalConsumed.get(),
                lostCount,
                duplicates,
                lossPercent,
                lostRanges,
                producerRto,
                consumerRto,
                maxRto,
                rpo,
                failureWindows,
                outOfOrderCount.get(),
                crcFailures.get(),
                orderingVerified,
                crcVerified,
                idempotenceEnabled,
                transactionsEnabled
        );

        LOG.info(String.format(
                "Integrity: sent=%d acked=%d consumed=%d lost=%d dupes=%d " +
                        "ooo=%d crc_fail=%d producerRTO=%s consumerRTO=%s rpo=%s " +
                        "loss=%.4f%% windows=%d verdict=%s",
                totalSent, ackTracker.getTotalAcked(), totalConsumed.get(),
                lostCount, duplicates, outOfOrderCount.get(), crcFailures.get(),
                producerRto, consumerRto, rpo,
                lossPercent, failureWindows.size(), result.verdict()));

        return result;
    }

    /**
     * Legacy overload for callers that only need basic reconciliation.
     */
    public IntegrityResult verify(long chaosStartNanos) {
        return verify(chaosStartNanos, false, false, false, false);
    }

    private List<LostRange> computeLostRanges(BitSet lostSet) {
        List<LostRange> ranges = new ArrayList<>();
        int pos = lostSet.nextSetBit(0);
        while (pos >= 0) {
            int start = pos;
            int end = pos;
            while (lostSet.get(end + 1)) {
                end++;
            }
            ranges.add(new LostRange(start, end, end - start + 1));
            pos = lostSet.nextSetBit(end + 1);
        }
        return List.copyOf(ranges);
    }
}
