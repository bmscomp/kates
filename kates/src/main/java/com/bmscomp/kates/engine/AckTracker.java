package com.bmscomp.kates.engine;

import java.util.BitSet;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicLongArray;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Thread-safe tracker for producer acknowledgments during data integrity tests.
 * Records which sequence numbers were acknowledged, plus the timestamps needed
 * for RTO/RPO computation.
 *
 * <p><b>Memory.</b> Sequences are dense (0..maxMessages), so acknowledgment is
 * tracked in a bit per sequence — the same trick {@link DataIntegrityVerifier}
 * already uses for the consumed set. The previous implementation kept two
 * {@link java.util.concurrent.ConcurrentSkipListMap}s holding a boxed entry per
 * sequence: a 10M-record INTEGRITY or ENDURANCE run allocated ~20M map entries
 * (hundreds of MB, plus the GC pressure of doing it on the ack path) where the
 * same information now costs ~1.25 MB.
 *
 * <p>All timestamps use {@link System#nanoTime()} (monotonic clock) for accuracy.
 * Failure windows are tracked atomically to prevent race conditions between
 * concurrent producer callbacks.
 */
public class AckTracker {

    /**
     * Upper bound on per-sequence tracking. Beyond this the bitset would cost
     * more than the information is worth; counters and failure windows continue
     * to work, only the per-sequence acked set is capped.
     */
    private static final long MAX_TRACKED_SEQUENCES = 1_000_000_000L;

    private static final long DEFAULT_CAPACITY = 1_000_000L;

    /**
     * A single continuous failure window with start/end nano timestamps.
     */
    public record FailureWindow(long startNanos, long endNanos) {
        public long durationNanos() {
            return endNanos > 0 ? endNanos - startNanos : -1;
        }

        public boolean isRecovered() {
            return endNanos > 0;
        }
    }

    /** One bit per sequence; CAS-updated so concurrent callbacks are safe. */
    private final AtomicLongArray ackedBits;

    private final long capacity;

    private final AtomicLong totalSent = new AtomicLong();
    private final AtomicLong totalAcked = new AtomicLong();
    private final AtomicLong totalFailed = new AtomicLong();

    /**
     * Current failure window being built (null = not in failure state).
     * AtomicReference ensures the failure→recovery transition is atomic.
     */
    private final AtomicReference<long[]> activeWindow = new AtomicReference<>(null);

    /**
     * All completed failure windows (recovered).
     */
    private final CopyOnWriteArrayList<FailureWindow> completedWindows = new CopyOnWriteArrayList<>();

    private volatile long lastAckedSendNanos = -1;

    /** Uses the default capacity; prefer {@link #AckTracker(long)}. */
    public AckTracker() {
        this(DEFAULT_CAPACITY);
    }

    /**
     * @param expectedSequences the run's record count, used to size the acked
     *     bitset exactly. Values ≤ 0 fall back to the default.
     */
    public AckTracker(long expectedSequences) {
        long requested = expectedSequences > 0 ? expectedSequences : DEFAULT_CAPACITY;
        this.capacity = Math.min(requested, MAX_TRACKED_SEQUENCES);
        this.ackedBits = new AtomicLongArray((int) ((this.capacity + 63) >>> 6));
    }

    /**
     * Called when a record is sent (before ack).
     *
     * <p>The send timestamp is no longer stored per sequence — it is handed
     * back in {@link #recordAcked(long, long)} by the callback that already
     * holds it, which removes an entire per-record map.
     */
    public void recordSent(long sequence, long timestampNanos) {
        totalSent.incrementAndGet();
    }

    /**
     * Called in the producer callback on successful ack.
     * Atomically closes an active failure window if one exists.
     *
     * @param sendTimestampNanos when the record was handed to the producer;
     *     drives the RPO calculation.
     */
    public void recordAcked(long sequence, long sendTimestampNanos) {
        setAcked(sequence);
        totalAcked.incrementAndGet();
        lastAckedSendNanos = sendTimestampNanos > 0 ? sendTimestampNanos : System.nanoTime();

        long[] window = activeWindow.getAndSet(null);
        if (window != null) {
            completedWindows.add(new FailureWindow(window[0], System.nanoTime()));
        }
    }

    /** Ack without a known send timestamp (RPO falls back to "now"). */
    public void recordAcked(long sequence) {
        recordAcked(sequence, -1);
    }

    /**
     * Called in the producer callback on failure.
     * Atomically opens a failure window if none is active.
     */
    public void recordFailed(long sequence) {
        totalFailed.incrementAndGet();

        activeWindow.compareAndSet(null, new long[] {System.nanoTime()});
    }

    private void setAcked(long sequence) {
        if (sequence < 0 || sequence >= capacity) {
            return; // Outside the tracked range; counters still reflect it.
        }
        int word = (int) (sequence >>> 6);
        long mask = 1L << (sequence & 63);
        long current;
        do {
            current = ackedBits.get(word);
            if ((current & mask) != 0) {
                return; // Already set.
            }
        } while (!ackedBits.compareAndSet(word, current, current | mask));
    }

    public long getTotalSent() {
        return totalSent.get();
    }

    public long getTotalAcked() {
        return totalAcked.get();
    }

    public long getTotalFailed() {
        return totalFailed.get();
    }

    public long getLastAckedSendNanos() {
        return lastAckedSendNanos;
    }

    /**
     * Returns all completed failure windows (failure → recovery transitions).
     */
    public List<FailureWindow> getCompletedWindows() {
        return List.copyOf(completedWindows);
    }

    /**
     * Returns the maximum RTO in nanoseconds across all failure windows.
     * Returns -1 if no completed failure window exists.
     */
    public long maxRtoNanos() {
        return completedWindows.stream()
                .mapToLong(FailureWindow::durationNanos)
                .max()
                .orElse(-1);
    }

    /**
     * Returns the first failure window's RTO, or -1 if none.
     */
    public long firstRtoNanos() {
        if (completedWindows.isEmpty()) return -1;
        return completedWindows.getFirst().durationNanos();
    }

    /**
     * Returns the first failure start nano timestamp, or -1.
     */
    public long getFirstFailureNanos() {
        if (completedWindows.isEmpty()) {
            long[] window = activeWindow.get();
            return window != null ? window[0] : -1;
        }
        return completedWindows.getFirst().startNanos();
    }

    /**
     * Returns true if currently in a failure state (no recovery yet).
     */
    public boolean isInFailure() {
        return activeWindow.get() != null;
    }

    /**
     * Returns a BitSet of all acknowledged sequence numbers.
     *
     * <p>Materialised on demand for the verifier; the live tracking itself
     * never allocates per record.
     */
    public BitSet getAckedSet() {
        long[] words = new long[ackedBits.length()];
        for (int i = 0; i < words.length; i++) {
            words[i] = ackedBits.get(i);
        }
        return BitSet.valueOf(words);
    }

    /** Highest sequence this tracker records individually. */
    public long trackedCapacity() {
        return capacity;
    }
}
