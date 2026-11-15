package com.bmscomp.kates.schedule;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.time.temporal.ChronoUnit;
import java.util.Optional;

import org.junit.jupiter.api.Test;

class DueTimesTest {

    private static Instant at(String time) {
        return Instant.parse("2026-11-15T" + time + "Z");
    }

    private static Optional<Instant> toFire(String cron, String since, String now) {
        return DueTimes.toFire(cron, at(since), at(now));
    }

    @Test
    void aMinuteJustPastFires() {
        assertEquals(Optional.of(at("10:37:00")), toFire("* * * * *", "10:36:00", "10:37:05"));
    }

    @Test
    void aDueTimeAlreadyFiredDoesNotFireAgain() {
        assertEquals(Optional.empty(), toFire("* * * * *", "10:37:00", "10:37:55"));
    }

    @Test
    void aDueTimeExactlyNowFires() {
        assertEquals(Optional.of(at("10:37:00")), toFire("* * * * *", "10:36:00", "10:37:00.000"));
    }

    @Test
    void onlyTheLatestMissedDueTimeFires() {
        assertEquals(Optional.of(at("10:37:00")), toFire("* * * * *", "10:33:00", "10:37:05"));
    }

    @Test
    void aDueTimeWithinTheCatchUpFires() {
        assertEquals(
                Optional.of(at("10:30:00")),
                DueTimes.toFire("30 10 * * *", Instant.parse("2026-11-14T10:30:00Z"), at("10:34:59")));
    }

    @Test
    void aDueTimeOlderThanTheCatchUpIsPassedOver() {
        assertEquals(
                Optional.empty(),
                DueTimes.toFire("30 10 * * *", Instant.parse("2026-11-14T10:30:00Z"), at("10:35:01")));
        assertEquals(Duration.ofMinutes(5), DueTimes.CATCH_UP);
    }

    @Test
    void aScheduleCreatedAfterADueTimeWaitsForTheNext() {
        assertEquals(Optional.empty(), toFire("* * * * *", "10:36:30", "10:36:50"));
        assertEquals(Optional.of(at("10:37:00")), toFire("* * * * *", "10:36:30", "10:37:01"));
    }

    /** A row from before V31 has no last_due; the run the previous version started still counts. */
    @Test
    void sinceIsTheLatestOfCreatedLastDueAndLastRun() {
        Instant created = at("09:00:00");
        assertEquals(created, DueTimes.since(created, null, null));
        assertEquals(at("10:35:02"), DueTimes.since(created, null, at("10:35:02")));
        assertEquals(at("10:40:00"), DueTimes.since(created, at("10:40:00"), at("10:35:02")));
        assertEquals(at("10:40:03"), DueTimes.since(created, at("10:40:00"), at("10:40:03")));
    }

    // ------------------------------------------------ cron semantics, from the old matcher's tests

    /** Whether {@code at}'s minute is a due time: it fires with since one minute before it. */
    private static boolean fires(String cron, ZonedDateTime at) {
        Instant now = at.toInstant();
        return DueTimes.toFire(cron, now.minus(Duration.ofMinutes(1)), now)
                .equals(Optional.of(now.truncatedTo(ChronoUnit.MINUTES)));
    }

    private static ZonedDateTime utc(int year, int month, int day, int hour, int minute) {
        return ZonedDateTime.of(year, month, day, hour, minute, 0, 0, ZoneOffset.UTC);
    }

    @Test
    void wildcardAndFixedMinute() {
        assertTrue(fires("* * * * *", utc(2026, 2, 16, 14, 30)));
        assertTrue(fires("30 * * * *", utc(2026, 2, 16, 14, 30)));
        assertFalse(fires("30 * * * *", utc(2026, 2, 16, 14, 15)));
    }

    @Test
    void stepValues() {
        String cron = "*/15 * * * *";
        assertTrue(fires(cron, utc(2026, 2, 16, 14, 0)));
        assertTrue(fires(cron, utc(2026, 2, 16, 14, 15)));
        assertTrue(fires(cron, utc(2026, 2, 16, 14, 45)));
        assertFalse(fires(cron, utc(2026, 2, 16, 14, 7)));
    }

    @Test
    void commaLists() {
        assertTrue(fires("0,30 * * * *", utc(2026, 2, 16, 14, 30)));
        assertFalse(fires("0,30 * * * *", utc(2026, 2, 16, 14, 10)));
    }

    @Test
    void ranges() {
        String cron = "* 9-17 * * *";
        assertTrue(fires(cron, utc(2026, 2, 16, 9, 0)));
        assertTrue(fires(cron, utc(2026, 2, 16, 17, 0)));
        assertFalse(fires(cron, utc(2026, 2, 16, 8, 0)));
        assertFalse(fires(cron, utc(2026, 2, 16, 18, 0)));
    }

    @Test
    void stepWithinARange() {
        String cron = "0-30/5 * * * *";
        assertTrue(fires(cron, utc(2026, 2, 16, 14, 25)));
        assertFalse(fires(cron, utc(2026, 2, 16, 14, 35)));
        assertFalse(fires(cron, utc(2026, 2, 16, 14, 7)));
    }

    @Test
    void secondsWithinTheMinuteStillFire() {
        assertTrue(fires("30 14 * * *", ZonedDateTime.of(2026, 2, 16, 14, 30, 42, 0, ZoneOffset.UTC)));
    }

    /** Feb 16, 2026 is a Monday, Feb 15 a Sunday. */
    @Test
    void dayNamesAndSundayAsSeven() {
        assertTrue(fires("0 9 * * MON", utc(2026, 2, 16, 9, 0)));
        assertFalse(fires("0 9 * * MON", utc(2026, 2, 17, 9, 0)));
        assertTrue(fires("0 0 * * 7", utc(2026, 2, 15, 0, 0)));
        assertTrue(fires("0 0 * * 0", utc(2026, 2, 15, 0, 0)));
    }

    /** A month step counts from January. */
    @Test
    void monthStepStartsInJanuary() {
        String cron = "0 0 1 */2 *";
        assertTrue(fires(cron, utc(2026, 1, 1, 0, 0)));
        assertFalse(fires(cron, utc(2026, 2, 1, 0, 0)));
        assertTrue(fires(cron, utc(2026, 3, 1, 0, 0)));
    }

    /** With both days set, cron fires on either one. Thursday Oct 1, Monday Oct 5, Tuesday Oct 6, 2026. */
    @Test
    void bothDayFieldsFireOnEither() {
        String cron = "0 9 1 * 1";
        assertTrue(fires(cron, utc(2026, 10, 1, 9, 0)));
        assertTrue(fires(cron, utc(2026, 10, 5, 9, 0)));
        assertFalse(fires(cron, utc(2026, 10, 6, 9, 0)));
    }

    @Test
    void aCronThatDoesNotParseThrows() {
        Instant now = at("10:37:00");
        assertThrows(IllegalArgumentException.class, () -> DueTimes.toFire("* *", now, now));
        assertThrows(IllegalArgumentException.class, () -> DueTimes.toFire("@daily", now, now));
        assertThrows(IllegalArgumentException.class, () -> DueTimes.toFire("60 * * * *", now, now));
    }
}
