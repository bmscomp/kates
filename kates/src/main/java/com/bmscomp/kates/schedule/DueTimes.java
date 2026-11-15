package com.bmscomp.kates.schedule;

import java.time.Duration;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.time.temporal.ChronoUnit;
import java.util.Optional;

import com.cronutils.model.time.ExecutionTime;

/**
 * Which due time of a schedule fires now. A tick that came late, after a
 * takeover or a restart, still fires the due time it missed, up to
 * {@link #CATCH_UP} late; when it missed several, only the latest starts a run.
 */
public final class DueTimes {

    /** How late a due time may still fire. */
    static final Duration CATCH_UP = Duration.ofMinutes(5);

    private DueTimes() {}

    /**
     * The latest due time {@code d} of {@code cron} (UNIX, UTC) with
     * {@code since < d <= now} and {@code d >= now - CATCH_UP}, or empty.
     * Throws IllegalArgumentException when {@code cron} does not parse.
     */
    public static Optional<Instant> toFire(String cron, Instant since, Instant now) {
        ExecutionTime times = ExecutionTime.forCron(TestScheduler.CRON.parse(cron));
        ZonedDateTime minute = now.atZone(ZoneOffset.UTC).truncatedTo(ChronoUnit.MINUTES);
        // lastExecution is strictly before its argument, so a due time at this very minute is asked first.
        Optional<ZonedDateTime> latest = times.isMatch(minute) ? Optional.of(minute) : times.lastExecution(minute);
        return latest.map(ZonedDateTime::toInstant)
                .filter(due -> due.isAfter(since) && !due.isBefore(now.minus(CATCH_UP)));
    }
}
