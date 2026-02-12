package com.klster.kates.service;

import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.domain.TestSpec;
import com.klster.kates.domain.TestType;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Optional;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import static org.junit.jupiter.api.Assertions.*;

class TestRunRepositoryTest {

    TestRunRepository repository;

    @BeforeEach
    void setUp() {
        repository = new TestRunRepository();
    }

    @Test
    void saveAndFindById() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);

        Optional<TestRun> found = repository.findById(run.getId());
        assertTrue(found.isPresent());
        assertEquals(run.getId(), found.get().getId());
        assertEquals(TestType.LOAD, found.get().getTestType());
    }

    @Test
    void findByIdReturnsEmptyForUnknown() {
        assertTrue(repository.findById("nonexistent").isEmpty());
    }

    @Test
    void findAllReturnsAllRuns() {
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.STRESS, new TestSpec()));
        repository.save(new TestRun(TestType.SPIKE, new TestSpec()));

        assertEquals(3, repository.findAll().size());
    }

    @Test
    void findByTypeFiltersCorrectly() {
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.LOAD, new TestSpec()));
        repository.save(new TestRun(TestType.STRESS, new TestSpec()));

        List<TestRun> loadRuns = repository.findByType(TestType.LOAD);
        List<TestRun> stressRuns = repository.findByType(TestType.STRESS);
        List<TestRun> spikeRuns = repository.findByType(TestType.SPIKE);

        assertEquals(2, loadRuns.size());
        assertEquals(1, stressRuns.size());
        assertEquals(0, spikeRuns.size());
    }

    @Test
    void deleteRemovesRun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);
        repository.delete(run.getId());

        assertTrue(repository.findById(run.getId()).isEmpty());
        assertEquals(0, repository.findAll().size());
    }

    @Test
    void saveUpdatesExistingRun() {
        TestRun run = new TestRun(TestType.LOAD, new TestSpec());
        repository.save(run);

        run.setStatus(TestResult.TaskStatus.DONE);
        repository.save(run);

        TestRun updated = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.DONE, updated.getStatus());
        assertEquals(1, repository.findAll().size());
    }

    @Test
    void concurrentSavesAreThreadSafe() throws Exception {
        int threadCount = 50;
        ExecutorService executor = Executors.newFixedThreadPool(threadCount);
        CountDownLatch latch = new CountDownLatch(threadCount);

        for (int i = 0; i < threadCount; i++) {
            executor.submit(() -> {
                try {
                    repository.save(new TestRun(TestType.LOAD, new TestSpec()));
                } finally {
                    latch.countDown();
                }
            });
        }

        latch.await();
        executor.shutdown();

        assertEquals(threadCount, repository.findAll().size());
    }
}
