package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Optional;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

@QuarkusTest
class TestRunRepositoryTest {

    @Inject
    TestRunRepository repository;

    @Inject
    jakarta.persistence.EntityManager em;

    @BeforeEach
    @Transactional
    void setUp() {
        em.createQuery("DELETE FROM TestResultEntity").executeUpdate();
        em.createQuery("DELETE FROM TestRunEntity").executeUpdate();
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

        run = run.withStatus(TestResult.TaskStatus.DONE);
        repository.save(run);

        TestRun updated = repository.findById(run.getId()).orElseThrow();
        assertEquals(TestResult.TaskStatus.DONE, updated.getStatus());
        assertEquals(1, repository.findAll().size());
    }
}
