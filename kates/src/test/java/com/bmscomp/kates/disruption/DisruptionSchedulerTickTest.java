package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.assertEquals;

import io.quarkus.scheduler.Scheduled;
import org.junit.jupiter.api.Test;

class DisruptionSchedulerTickTest {

    /**
     * A tick runs each plan it fires to its end. A second tick started while
     * one plan runs would fire a schedule due the same minute into the target
     * the first holds, and its report would be REJECTED, its due time spent.
     */
    @Test
    void aTickDoesNotStartWhileAnotherRunsAPlan() throws Exception {
        Scheduled tick =
                DisruptionScheduler.class.getDeclaredMethod("evaluateSchedules").getAnnotation(Scheduled.class);
        assertEquals(Scheduled.ConcurrentExecution.SKIP, tick.concurrentExecution());
    }
}
