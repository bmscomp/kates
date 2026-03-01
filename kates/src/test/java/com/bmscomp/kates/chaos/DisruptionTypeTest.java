package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

class DisruptionTypeTest {

    @Test
    void allThirteenValuesExist() {
        assertEquals(13, DisruptionType.values().length);
    }

    @ParameterizedTest
    @EnumSource(DisruptionType.class)
    void valueOfRoundTrips(DisruptionType type) {
        assertEquals(type, DisruptionType.valueOf(type.name()));
    }

    @Test
    void specificValuesPresent() {
        assertDoesNotThrow(() -> DisruptionType.valueOf("POD_KILL"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("POD_DELETE"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("MEMORY_STRESS"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("IO_STRESS"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("DNS_ERROR"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("NODE_DRAIN"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("NETWORK_PARTITION"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("CPU_STRESS"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("DISK_FILL"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("ROLLING_RESTART"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("LEADER_ELECTION"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("SCALE_DOWN"));
        assertDoesNotThrow(() -> DisruptionType.valueOf("NETWORK_LATENCY"));
    }

    @Test
    void invalidValueThrows() {
        assertThrows(IllegalArgumentException.class, () -> DisruptionType.valueOf("NONEXISTENT"));
    }
}
