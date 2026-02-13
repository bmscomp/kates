package com.klster.kates.engine;

import java.nio.ByteBuffer;

/**
 * Binary protocol for data integrity verification.
 * Each produced record carries a unique identity that can be reconciled
 * after a failure to detect data loss.
 *
 * <p>Wire format (24 bytes header + padding):
 * <pre>
 *   [8 bytes] sequence number  (long)
 *   [8 bytes] timestamp nanos  (long)
 *   [8 bytes] run ID hash      (long)
 *   [N bytes] zero padding     (to match target record size)
 * </pre>
 */
public final class SequencedPayload {

    public static final int HEADER_SIZE = 24;

    private final long sequence;
    private final long timestampNanos;
    private final long runIdHash;

    public SequencedPayload(long sequence, long timestampNanos, long runIdHash) {
        this.sequence = sequence;
        this.timestampNanos = timestampNanos;
        this.runIdHash = runIdHash;
    }

    public long getSequence() { return sequence; }
    public long getTimestampNanos() { return timestampNanos; }
    public long getRunIdHash() { return runIdHash; }

    /**
     * Encodes the payload into a byte array of the given total size.
     * Minimum size is {@link #HEADER_SIZE} (24 bytes).
     */
    public static byte[] encode(long sequence, long timestampNanos, long runIdHash, int totalSize) {
        int size = Math.max(totalSize, HEADER_SIZE);
        ByteBuffer buf = ByteBuffer.allocate(size);
        buf.putLong(sequence);
        buf.putLong(timestampNanos);
        buf.putLong(runIdHash);
        return buf.array();
    }

    /**
     * Decodes a sequenced payload from raw bytes.
     *
     * @throws IllegalArgumentException if the array is smaller than {@link #HEADER_SIZE}
     */
    public static SequencedPayload decode(byte[] data) {
        if (data == null || data.length < HEADER_SIZE) {
            throw new IllegalArgumentException(
                    "Payload must be at least " + HEADER_SIZE + " bytes, got " +
                            (data == null ? 0 : data.length));
        }
        ByteBuffer buf = ByteBuffer.wrap(data);
        return new SequencedPayload(buf.getLong(), buf.getLong(), buf.getLong());
    }

    /**
     * Computes a stable hash for a run ID string.
     */
    public static long hashRunId(String runId) {
        long h = 0;
        for (int i = 0; i < runId.length(); i++) {
            h = 31 * h + runId.charAt(i);
        }
        return h;
    }
}
