package com.bmscomp.kates.schedule;

import static org.junit.jupiter.api.Assertions.*;

import java.time.ZoneOffset;
import java.time.ZonedDateTime;

import org.junit.jupiter.api.Test;

class TestSchedulerCronTest {

    @Test
    void wildcardMatchesAny() {
        ZonedDateTime now = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        assertTrue(TestScheduler.matchesCron("* * * * *", now));
    }

    @Test
    void fixedMinuteMatches() {
        ZonedDateTime now = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        assertTrue(TestScheduler.matchesCron("30 * * * *", now));
    }

    @Test
    void fixedMinuteDoesNotMatch() {
        ZonedDateTime now = ZonedDateTime.of(2026, 2, 16, 14, 15, 0, 0, ZoneOffset.UTC);
        assertFalse(TestScheduler.matchesCron("30 * * * *", now));
    }

    @Test
    void stepValueMatches() {
        ZonedDateTime at0 = ZonedDateTime.of(2026, 2, 16, 14, 0, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at15 = ZonedDateTime.of(2026, 2, 16, 14, 15, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at30 = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at45 = ZonedDateTime.of(2026, 2, 16, 14, 45, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at7 = ZonedDateTime.of(2026, 2, 16, 14, 7, 0, 0, ZoneOffset.UTC);

        String cron = "*/15 * * * *";
        assertTrue(TestScheduler.matchesCron(cron, at0));
        assertTrue(TestScheduler.matchesCron(cron, at15));
        assertTrue(TestScheduler.matchesCron(cron, at30));
        assertTrue(TestScheduler.matchesCron(cron, at45));
        assertFalse(TestScheduler.matchesCron(cron, at7));
    }

    @Test
    void commaListMatches() {
        ZonedDateTime at0 = ZonedDateTime.of(2026, 2, 16, 14, 0, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at30 = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at10 = ZonedDateTime.of(2026, 2, 16, 14, 10, 0, 0, ZoneOffset.UTC);

        String cron = "0,30 * * * *";
        assertTrue(TestScheduler.matchesCron(cron, at0));
        assertTrue(TestScheduler.matchesCron(cron, at30));
        assertFalse(TestScheduler.matchesCron(cron, at10));
    }

    @Test
    void rangeMatches() {
        ZonedDateTime at9 = ZonedDateTime.of(2026, 2, 16, 9, 0, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at13 = ZonedDateTime.of(2026, 2, 16, 13, 0, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at17 = ZonedDateTime.of(2026, 2, 16, 17, 0, 0, 0, ZoneOffset.UTC);

        String cron = "* 9-17 * * *";
        assertTrue(TestScheduler.matchesCron(cron, at9));
        assertTrue(TestScheduler.matchesCron(cron, at13));
        assertTrue(TestScheduler.matchesCron(cron, at17));
    }

    @Test
    void rangeExcludes() {
        ZonedDateTime at8 = ZonedDateTime.of(2026, 2, 16, 8, 0, 0, 0, ZoneOffset.UTC);
        ZonedDateTime at18 = ZonedDateTime.of(2026, 2, 16, 18, 0, 0, 0, ZoneOffset.UTC);

        String cron = "* 9-17 * * *";
        assertFalse(TestScheduler.matchesCron(cron, at8));
        assertFalse(TestScheduler.matchesCron(cron, at18));
    }

    @Test
    void invalidCronReturnsFalse() {
        ZonedDateTime now = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        assertFalse(TestScheduler.matchesCron("* *", now));
        assertFalse(TestScheduler.matchesCron("*", now));
    }

    @Test
    void fullCronExpression() {
        // Monday = 1 in java.time DayOfWeek, but cron uses %7 → 1
        // Feb 16, 2026 is a Monday
        ZonedDateTime monday = ZonedDateTime.of(2026, 2, 16, 10, 30, 0, 0, ZoneOffset.UTC);
        assertTrue(TestScheduler.matchesCron("30 10 16 2 1", monday));
        assertFalse(TestScheduler.matchesCron("30 10 16 2 0", monday));
    }
}
