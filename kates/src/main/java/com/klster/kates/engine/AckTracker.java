package com.klster.kates.engine;

import java.util.BitSet;
import java.util.List;
import java.util.concurrent.ConcurrentSkipListMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Thread-safe tracker for producer acknowledgments during data integrity tests.
 * Records which sequence numbers were sent, acknowledged, and failed,
 * plus the timestamps needed for RTO computation.
 *
 * <p>All timestamps use {@link System#nanoTime()} (monotonic clock) for accuracy.
 * Failure windows are tracked atomically to prevent race conditions between
 * concurrent producer callbacks.
 */
public class AckTracker {

    private enum AckState {
        SENT,
        ACKED,
        FAILED
    }

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

    private final ConcurrentSkipListMap<Long, AckState> states = new ConcurrentSkipListMap<>();
    private final ConcurrentSkipListMap<Long, Long> sendTimestamps = new ConcurrentSkipListMap<>();

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

    /**
     * Called when a record is sent (before ack).
     */
    public void recordSent(long sequence, long timestampNanos) {
        states.put(sequence, AckState.SENT);
        sendTimestamps.put(sequence, timestampNanos);
        totalSent.incrementAndGet();
    }

    /**
     * Called in the producer callback on successful ack.
     * Atomically closes an active failure window if one exists.
     */
    public void recordAcked(long sequence) {
        states.put(sequence, AckState.ACKED);
        totalAcked.incrementAndGet();
        lastAckedSendNanos = sendTimestamps.getOrDefault(sequence, System.nanoTime());

        long[] window = activeWindow.getAndSet(null);
        if (window != null) {
            completedWindows.add(new FailureWindow(window[0], System.nanoTime()));
        }
    }

    /**
     * Called in the producer callback on failure.
     * Atomically opens a failure window if none is active.
     */
    public void recordFailed(long sequence) {
        states.put(sequence, AckState.FAILED);
        totalFailed.incrementAndGet();

        activeWindow.compareAndSet(null, new long[] {System.nanoTime()});
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
     */
    public BitSet getAckedSet() {
        BitSet bs = new BitSet();
        states.forEach((seq, state) -> {
            if (state == AckState.ACKED) {
                bs.set(seq.intValue());
            }
        });
        return bs;
    }

    /**
     * Returns a BitSet of all sent sequence numbers.
     */
    public BitSet getSentSet() {
        BitSet bs = new BitSet();
        states.keySet().forEach(seq -> bs.set(seq.intValue()));
        return bs;
    }

    /**
     * Returns a BitSet of all failed sequence numbers.
     */
    public BitSet getFailedSet() {
        BitSet bs = new BitSet();
        states.forEach((seq, state) -> {
            if (state == AckState.FAILED) {
                bs.set(seq.intValue());
            }
        });
        return bs;
    }
}
