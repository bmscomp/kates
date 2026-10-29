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
    void invalidCronThrows() {
        ZonedDateTime now = ZonedDateTime.of(2026, 2, 16, 14, 30, 0, 0, ZoneOffset.UTC);
        assertThrows(IllegalArgumentException.class, () -> TestScheduler.matchesCron("* *", now));
        assertThrows(IllegalArgumentException.class, () -> TestScheduler.matchesCron("@daily", now));
        assertThrows(IllegalArgumentException.class, () -> TestScheduler.matchesCron("60 * * * *", now));
    }

    @Test
    void secondsWithinTheMinuteStillMatch() {
        assertTrue(
                TestScheduler.matchesCron("30 14 * * *", ZonedDateTime.of(2026, 2, 16, 14, 30, 42, 0, ZoneOffset.UTC)));
    }

    /** Day names were refused by the old matcher, which saved them and never fired. Feb 16, 2026 is a Monday. */
    @Test
    void dayNamesMatch() {
        assertTrue(TestScheduler.matchesCron("0 9 * * MON", ZonedDateTime.of(2026, 2, 16, 9, 0, 0, 0, ZoneOffset.UTC)));
        assertFalse(
                TestScheduler.matchesCron("0 9 * * MON", ZonedDateTime.of(2026, 2, 17, 9, 0, 0, 0, ZoneOffset.UTC)));
    }

    @Test
    void stepWithinARangeMatches() {
        String cron = "0-30/5 * * * *";
        assertTrue(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 2, 16, 14, 25, 0, 0, ZoneOffset.UTC)));
        assertFalse(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 2, 16, 14, 35, 0, 0, ZoneOffset.UTC)));
        assertFalse(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 2, 16, 14, 7, 0, 0, ZoneOffset.UTC)));
    }

    /** Day-of-week 7 is Sunday, as 0 is. Feb 15, 2026 is a Sunday. */
    @Test
    void daySevenIsSunday() {
        ZonedDateTime sunday = ZonedDateTime.of(2026, 2, 15, 0, 0, 0, 0, ZoneOffset.UTC);
        assertTrue(TestScheduler.matchesCron("0 0 * * 7", sunday));
        assertTrue(TestScheduler.matchesCron("0 0 * * 0", sunday));
    }

    /** A month step counts from January: the old matcher's value % 2 picked February, April, ... */
    @Test
    void monthStepStartsInJanuary() {
        String cron = "0 0 1 */2 *";
        assertTrue(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 1, 1, 0, 0, 0, 0, ZoneOffset.UTC)));
        assertFalse(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 2, 1, 0, 0, 0, 0, ZoneOffset.UTC)));
        assertTrue(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 3, 1, 0, 0, 0, 0, ZoneOffset.UTC)));
    }

    /** With both days set, cron fires on either one, not only when both hold. */
    @Test
    void dayOfMonthOrDayOfWeek() {
        String cron = "0 9 1 * 1";
        // Thursday Oct 1, Monday Oct 5, Tuesday Oct 6, 2026.
        assertTrue(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 10, 1, 9, 0, 0, 0, ZoneOffset.UTC)));
        assertTrue(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 10, 5, 9, 0, 0, 0, ZoneOffset.UTC)));
        assertFalse(TestScheduler.matchesCron(cron, ZonedDateTime.of(2026, 10, 6, 9, 0, 0, 0, ZoneOffset.UTC)));
    }
}
