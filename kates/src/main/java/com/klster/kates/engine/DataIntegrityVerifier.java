package com.klster.kates.engine;

import com.klster.kates.domain.IntegrityResult;
import com.klster.kates.domain.LostRange;

import java.time.Duration;
import java.util.ArrayList;
import java.util.BitSet;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.logging.Logger;

/**
 * Post-benchmark verifier that reconciles produced records against consumed records
 * to compute data loss, RPO, and both producer-side and consumer-side RTO.
 *
 * <p>Usage:
 * <pre>
 *   var verifier = new DataIntegrityVerifier(ackTracker);
 *   verifier.recordConsumed(sequenceNumber);  // called for each consumed record
 *   IntegrityResult result = verifier.verify(chaosStartNanos);
 * </pre>
 *
 * <p>All timestamps use {@link System#nanoTime()} (monotonic clock) for accuracy.
 */
public class DataIntegrityVerifier {

    private static final Logger LOG = Logger.getLogger(DataIntegrityVerifier.class.getName());

    private final AckTracker ackTracker;
    private final BitSet consumedSet = new BitSet();
    private final Map<Long, Integer> consumedCounts = new HashMap<>();
    private long totalConsumed;

    private volatile long firstConsumeGapNanos = -1;
    private volatile long firstConsumeRecoveryNanos = -1;
    private volatile boolean inConsumeGap = false;
    private long previousConsumedSeq = -1;

    public DataIntegrityVerifier(AckTracker ackTracker) {
        this.ackTracker = ackTracker;
    }

    /**
     * Record a consumed sequence number.
     * Called for each record read by the verification consumer.
     * Tracks consumer-side gaps for consumer RTO computation.
     */
    public void recordConsumed(long sequence) {
        int seq = (int) sequence;
        consumedSet.set(seq);
        consumedCounts.merge(sequence, 1, Integer::sum);
        totalConsumed++;

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
     * Performs the reconciliation and returns the integrity result.
     *
     * @param chaosStartNanos monotonic nano timestamp from
     *        {@link com.klster.kates.chaos.ChaosOutcome#chaosStartNanos()}
     *        for RPO computation. Pass -1 if no chaos was triggered.
     */
    public IntegrityResult verify(long chaosStartNanos) {
        BitSet ackedSet = ackTracker.getAckedSet();

        // lost = acked but not consumed (data the broker claimed to persist but didn't)
        BitSet lostSet = (BitSet) ackedSet.clone();
        lostSet.andNot(consumedSet);
        long lostCount = lostSet.cardinality();

        // unacked lost = failed sends that also weren't consumed (expected losses)
        BitSet failedSet = ackTracker.getFailedSet();
        BitSet unackedLostSet = (BitSet) failedSet.clone();
        unackedLostSet.andNot(consumedSet);
        long unackedLost = unackedLostSet.cardinality();

        // duplicates
        long duplicates = consumedCounts.values().stream()
                .filter(c -> c > 1)
                .mapToLong(c -> c - 1)
                .sum();

        // lost ranges (contiguous gaps in the acked-but-not-consumed set)
        List<LostRange> lostRanges = computeLostRanges(lostSet);

        // producer RTO: max across all failure windows
        long maxProducerRtoNanos = ackTracker.maxRtoNanos();
        Duration producerRto = maxProducerRtoNanos > 0
                ? Duration.ofNanos(maxProducerRtoNanos) : Duration.ZERO;

        // consumer RTO: time from first gap detection to gap closure
        Duration consumerRto = Duration.ZERO;
        if (firstConsumeGapNanos > 0 && firstConsumeRecoveryNanos > 0) {
            long consumerRtoNanos = firstConsumeRecoveryNanos - firstConsumeGapNanos;
            if (consumerRtoNanos > 0) {
                consumerRto = Duration.ofNanos(consumerRtoNanos);
            }
        }

        // max RTO across both producer and consumer perspectives
        Duration maxRto = producerRto.compareTo(consumerRto) >= 0 ? producerRto : consumerRto;

        // RPO: time between chaos start and last acknowledged record (both nanoTime)
        Duration rpo = Duration.ZERO;
        if (chaosStartNanos > 0 && ackTracker.getLastAckedSendNanos() > 0) {
            long rpoNanos = chaosStartNanos - ackTracker.getLastAckedSendNanos();
            if (rpoNanos > 0) {
                rpo = Duration.ofNanos(rpoNanos);
            }
        }

        // data loss percent
        long totalSent = ackTracker.getTotalSent();
        double lossPercent = totalSent > 0 ? (double) lostCount / totalSent * 100.0 : 0.0;

        // failure windows for detailed reporting
        List<AckTracker.FailureWindow> failureWindows = ackTracker.getCompletedWindows();

        IntegrityResult result = IntegrityResult.builder()
                .totalSent(totalSent)
                .totalAcked(ackTracker.getTotalAcked())
                .totalConsumed(totalConsumed)
                .lostRecords(lostCount)
                .unackedLost(unackedLost)
                .duplicateRecords(duplicates)
                .lostRanges(lostRanges)
                .producerRto(producerRto)
                .consumerRto(consumerRto)
                .maxRto(maxRto)
                .rpo(rpo)
                .dataLossPercent(lossPercent)
                .failureWindows(failureWindows)
                .build();

        LOG.info(String.format(
                "Integrity: sent=%d acked=%d consumed=%d lost=%d dupes=%d " +
                        "producerRTO=%s consumerRTO=%s rpo=%s loss=%.4f%% windows=%d",
                totalSent, ackTracker.getTotalAcked(), totalConsumed,
                lostCount, duplicates, producerRto, consumerRto, rpo,
                lossPercent, failureWindows.size()));

        return result;
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
