package com.bmscomp.kates.engine;

import java.util.ArrayDeque;
import java.util.BitSet;
import java.util.List;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicLongArray;
import java.util.concurrent.atomic.AtomicReference;
import java.util.concurrent.atomic.AtomicReferenceArray;

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
 * <p><b>Allocation is lazy.</b> Capacity comes from the caller-supplied record
 * count, which on the API path is whatever the client asked for — sizing the
 * whole bitset up front let a single request reserve hundreds of MB before
 * producing one record. The bitset is therefore split into chunks that are
 * allocated on first write, so memory tracks the sequences actually acked
 * rather than the number claimed.
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

    /** Sequences covered by one lazily allocated chunk (512 KB of longs). */
    private static final int SEQUENCES_PER_CHUNK = 1 << 22;

    private static final int WORDS_PER_CHUNK = SEQUENCES_PER_CHUNK >>> 6;

    /**
     * One send timestamp is kept per this many sequences. RPO needs the send
     * time of the oldest lost record, and a timestamp per record would cost 64
     * times the acked bitset; one per 64 costs the same as the bitset. Because
     * the producer assigns sequences and timestamps in the same order, the
     * sample at or below a sequence is a lower bound on its send time, so RPO
     * is overstated by at most 63 sends and never understated.
     */
    static final int SEND_TIME_STRIDE = 64;

    /** Send-time samples per lazily allocated chunk (512 KB of longs). */
    private static final int SEND_TIMES_PER_CHUNK = 1 << 16;

    /**
     * Recovered failure windows retained for reporting. A flapping broker can
     * close a window per ack, so this list is bounded; the worst and first RTO
     * are tracked separately and therefore survive eviction.
     */
    private static final int MAX_COMPLETED_WINDOWS = 1_000;

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

    /**
     * One bit per sequence, held in chunks that are allocated on first write.
     * CAS-updated so concurrent callbacks are safe.
     */
    private final AtomicReferenceArray<AtomicLongArray> chunks;

    /** Total words the full capacity would need, if every chunk were allocated. */
    private final int wordCount;

    private final long capacity;

    /** Every {@link #SEND_TIME_STRIDE}th send's timestamp, in chunks allocated on first write. */
    private final AtomicReferenceArray<AtomicLongArray> sendTimeChunks;

    private final AtomicLong totalSent = new AtomicLong();
    private final AtomicLong totalAcked = new AtomicLong();
    private final AtomicLong totalFailed = new AtomicLong();

    /**
     * Current failure window being built (null = not in failure state).
     * AtomicReference ensures the failure→recovery transition is atomic.
     */
    private final AtomicReference<long[]> activeWindow = new AtomicReference<>(null);

    /** Recovered failure windows, newest last, capped at {@link #MAX_COMPLETED_WINDOWS}. */
    private final ArrayDeque<FailureWindow> completedWindows = new ArrayDeque<>();

    /** Windows evicted by the cap, so reporting can say the list is partial. */
    private final AtomicLong droppedWindows = new AtomicLong();

    /** Tracked separately so eviction cannot change the reported worst RTO. */
    private final AtomicLong maxRtoNanosSeen = new AtomicLong(-1);

    /** Kept explicitly: the oldest window may be evicted from the deque. */
    private final AtomicReference<FailureWindow> firstCompletedWindow = new AtomicReference<>();

    private final AtomicLong lastAckedSendNanos = new AtomicLong(-1);

    /** Set once the bitset has been released; blocks re-allocation of chunks. */
    private volatile boolean released;

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
        long words = (this.capacity + 63) >>> 6;
        this.wordCount = (int) words;
        int chunkCount = (int) ((words + WORDS_PER_CHUNK - 1) / WORDS_PER_CHUNK);
        // Only the chunk table is allocated up front: ~2 KB even at the 1e9 cap.
        this.chunks = new AtomicReferenceArray<>(Math.max(1, chunkCount));
        long samples = (this.capacity + SEND_TIME_STRIDE - 1) / SEND_TIME_STRIDE;
        this.sendTimeChunks = new AtomicReferenceArray<>(
                (int) Math.max(1, (samples + SEND_TIMES_PER_CHUNK - 1) / SEND_TIMES_PER_CHUNK));
    }

    /**
     * Called when a record is sent (before ack).
     *
     * <p>The send timestamp is not stored per sequence — only every
     * {@link #SEND_TIME_STRIDE}th one is, which is enough to place a lost
     * record in time (see {@link #sendTimeAtOrBefore(long)}).
     */
    public void recordSent(long sequence, long timestampNanos) {
        totalSent.incrementAndGet();
        if (sequence >= 0 && sequence < capacity && sequence % SEND_TIME_STRIDE == 0) {
            long sample = sequence / SEND_TIME_STRIDE;
            AtomicLongArray chunk = sendTimeChunkForWrite((int) (sample / SEND_TIMES_PER_CHUNK));
            if (chunk != null) {
                chunk.set((int) (sample % SEND_TIMES_PER_CHUNK), timestampNanos);
            }
        }
    }

    /**
     * A lower bound on when {@code sequence} was sent: the recorded send time
     * of the nearest sampled sequence at or below it, or -1 when none was
     * recorded (outside the tracked range, or released).
     */
    public long sendTimeAtOrBefore(long sequence) {
        if (sequence < 0 || sequence >= capacity) {
            return -1;
        }
        long sample = sequence / SEND_TIME_STRIDE;
        AtomicLongArray chunk = sendTimeChunks.get((int) (sample / SEND_TIMES_PER_CHUNK));
        long nanos = chunk == null ? 0 : chunk.get((int) (sample % SEND_TIMES_PER_CHUNK));
        return nanos > 0 ? nanos : -1;
    }

    private AtomicLongArray sendTimeChunkForWrite(int chunkIndex) {
        if (released) {
            return null;
        }
        AtomicLongArray chunk = sendTimeChunks.get(chunkIndex);
        if (chunk != null) {
            return chunk;
        }
        long samples = (capacity + SEND_TIME_STRIDE - 1) / SEND_TIME_STRIDE;
        int size = (int) Math.min(SEND_TIMES_PER_CHUNK, samples - (long) chunkIndex * SEND_TIMES_PER_CHUNK);
        AtomicLongArray created = new AtomicLongArray(Math.max(1, size));
        return sendTimeChunks.compareAndSet(chunkIndex, null, created) ? created : sendTimeChunks.get(chunkIndex);
    }

    /**
     * Called in the producer callback on successful ack.
     * Atomically closes an active failure window if one exists.
     *
     * @param sendTimestampNanos when the record was handed to the producer.
     */
    public void recordAcked(long sequence, long sendTimestampNanos) {
        setAcked(sequence);
        totalAcked.incrementAndGet();

        // Acks complete out of order across partitions and retries, so a plain
        // write could leave an OLDER send timestamp as "last acked". Keep the
        // maximum instead.
        long candidate = sendTimestampNanos > 0 ? sendTimestampNanos : System.nanoTime();
        long current;
        do {
            current = lastAckedSendNanos.get();
        } while (candidate > current && !lastAckedSendNanos.compareAndSet(current, candidate));

        long[] window = activeWindow.getAndSet(null);
        if (window != null) {
            recordCompletedWindow(new FailureWindow(window[0], System.nanoTime()));
        }
    }

    /**
     * Appends a recovered failure window, keeping at most
     * {@link #MAX_COMPLETED_WINDOWS}.
     *
     * <p>The list is on the ack path, and a flapping broker can open and close a
     * window per ack. Unbounded, that grew without limit; on a copy-on-write list
     * each append also copies the whole array, so the ack path degraded
     * quadratically exactly when the cluster was least healthy. The oldest
     * windows are dropped, and the count of dropped ones is kept so RTO
     * reporting can say the list is partial.
     */
    private void recordCompletedWindow(FailureWindow window) {
        synchronized (completedWindows) {
            if (completedWindows.size() >= MAX_COMPLETED_WINDOWS) {
                completedWindows.removeFirst();
                droppedWindows.incrementAndGet();
            }
            completedWindows.addLast(window);
        }
        firstCompletedWindow.compareAndSet(null, window);

        long durationNanos = window.durationNanos();
        long currentMax;
        do {
            currentMax = maxRtoNanosSeen.get();
        } while (durationNanos > currentMax && !maxRtoNanosSeen.compareAndSet(currentMax, durationNanos));
    }

    /** Ack without a known send timestamp ("last acked" falls back to "now"). */
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
        int chunkIndex = word / WORDS_PER_CHUNK;
        int offset = word % WORDS_PER_CHUNK;
        AtomicLongArray chunk = chunkForWrite(chunkIndex);
        if (chunk == null) {
            return; // Released after the run finished; counters still hold.
        }
        long mask = 1L << (sequence & 63);
        long current;
        do {
            current = chunk.get(offset);
            if ((current & mask) != 0) {
                return; // Already set.
            }
        } while (!chunk.compareAndSet(offset, current, current | mask));
    }

    /** Returns the chunk holding {@code chunkIndex}, allocating it on first use. */
    private AtomicLongArray chunkForWrite(int chunkIndex) {
        if (released) {
            return null;
        }
        AtomicLongArray chunk = chunks.get(chunkIndex);
        if (chunk != null) {
            return chunk;
        }
        int size = Math.min(WORDS_PER_CHUNK, wordCount - chunkIndex * WORDS_PER_CHUNK);
        AtomicLongArray created = new AtomicLongArray(Math.max(1, size));
        // Losers of the race take the winner's chunk; no bits can be lost,
        // because a losing thread has not written to its own copy yet.
        return chunks.compareAndSet(chunkIndex, null, created) ? created : chunks.get(chunkIndex);
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
        return lastAckedSendNanos.get();
    }

    /**
     * Returns the retained completed failure windows (failure → recovery
     * transitions), oldest first. Capped at {@link #MAX_COMPLETED_WINDOWS} —
     * see {@link #getDroppedWindowCount()} for how many older ones were evicted.
     */
    public List<FailureWindow> getCompletedWindows() {
        synchronized (completedWindows) {
            return List.copyOf(completedWindows);
        }
    }

    /** Completed windows discarded by the retention cap. */
    public long getDroppedWindowCount() {
        return droppedWindows.get();
    }

    /**
     * Returns the maximum RTO in nanoseconds across all failure windows,
     * including any that have since been evicted from the retained list.
     * Returns -1 if no completed failure window exists.
     */
    public long maxRtoNanos() {
        return maxRtoNanosSeen.get();
    }

    /**
     * Returns the first failure window's RTO, or -1 if none.
     */
    public long firstRtoNanos() {
        FailureWindow first = firstCompletedWindow.get();
        return first != null ? first.durationNanos() : -1;
    }

    /**
     * Returns the first failure start nano timestamp, or -1.
     */
    public long getFirstFailureNanos() {
        FailureWindow first = firstCompletedWindow.get();
        if (first != null) {
            return first.startNanos();
        }
        long[] window = activeWindow.get();
        return window != null ? window[0] : -1;
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
        int highestChunk = -1;
        for (int i = chunks.length() - 1; i >= 0; i--) {
            if (chunks.get(i) != null) {
                highestChunk = i;
                break;
            }
        }
        if (highestChunk < 0) {
            return new BitSet();
        }
        // Materialise only up to the highest chunk ever written: unallocated
        // chunks are all zeros, and BitSet drops trailing zero words anyway.
        int words = Math.min(wordCount, (highestChunk + 1) * WORDS_PER_CHUNK);
        long[] out = new long[words];
        for (int i = 0; i < words; i++) {
            AtomicLongArray chunk = chunks.get(i / WORDS_PER_CHUNK);
            out[i] = chunk == null ? 0L : chunk.get(i % WORDS_PER_CHUNK);
        }
        return BitSet.valueOf(out);
    }

    /**
     * Drops the per-sequence bitset and send-time samples once the run is
     * over. Counters, failure windows and RTO stay readable; only the memory
     * goes away. Called on the worker's terminal path, after verification, so
     * a finished run stops holding either.
     */
    public void release() {
        released = true;
        for (int i = 0; i < chunks.length(); i++) {
            chunks.set(i, null);
        }
        for (int i = 0; i < sendTimeChunks.length(); i++) {
            sendTimeChunks.set(i, null);
        }
    }

    /** Highest sequence this tracker records individually. */
    public long trackedCapacity() {
        return capacity;
    }
}
