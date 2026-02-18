package com.klster.kates.engine;

import java.nio.ByteBuffer;
import java.util.zip.CRC32;

/**
 * Binary protocol for data integrity verification.
 * Each produced record carries a unique identity that can be reconciled
 * after a failure to detect data loss.
 *
 * <p>Wire format (28 bytes header + padding):
 * <pre>
 *   [8 bytes] sequence number  (long)
 *   [8 bytes] timestamp nanos  (long)
 *   [8 bytes] run ID hash      (long)
 *   [4 bytes] CRC32            (int — checksum of first 24 bytes)
 *   [N bytes] zero padding     (to match target record size)
 * </pre>
 */
public final class SequencedPayload {

    public static final int HEADER_SIZE = 28;

    private final long sequence;
    private final long timestampNanos;
    private final long runIdHash;
    private final boolean crcValid;

    public SequencedPayload(long sequence, long timestampNanos, long runIdHash, boolean crcValid) {
        this.sequence = sequence;
        this.timestampNanos = timestampNanos;
        this.runIdHash = runIdHash;
        this.crcValid = crcValid;
    }

    public long getSequence() {
        return sequence;
    }

    public long getTimestampNanos() {
        return timestampNanos;
    }

    public long getRunIdHash() {
        return runIdHash;
    }

    public boolean isCrcValid() {
        return crcValid;
    }

    /**
     * Encodes the payload into a byte array of the given total size.
     * Minimum size is {@link #HEADER_SIZE} (28 bytes).
     */
    public static byte[] encode(long sequence, long timestampNanos, long runIdHash, int totalSize) {
        int size = Math.max(totalSize, HEADER_SIZE);
        ByteBuffer buf = ByteBuffer.allocate(size);
        buf.putLong(sequence);
        buf.putLong(timestampNanos);
        buf.putLong(runIdHash);

        CRC32 crc = new CRC32();
        crc.update(buf.array(), 0, 24);
        buf.putInt((int) crc.getValue());

        return buf.array();
    }

    /**
     * Decodes a sequenced payload from raw bytes, verifying CRC32 integrity.
     *
     * @throws IllegalArgumentException if the array is smaller than {@link #HEADER_SIZE}
     */
    public static SequencedPayload decode(byte[] data) {
        if (data == null || data.length < HEADER_SIZE) {
            throw new IllegalArgumentException(
                    "Payload must be at least " + HEADER_SIZE + " bytes, got " + (data == null ? 0 : data.length));
        }
        ByteBuffer buf = ByteBuffer.wrap(data);
        long seq = buf.getLong();
        long ts = buf.getLong();
        long hash = buf.getLong();
        int storedCrc = buf.getInt();

        CRC32 crc = new CRC32();
        crc.update(data, 0, 24);
        boolean valid = storedCrc == (int) crc.getValue();

        return new SequencedPayload(seq, ts, hash, valid);
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
