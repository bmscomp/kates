package com.klster.kates.service;

import com.klster.kates.domain.TestRun;
import jakarta.enterprise.context.ApplicationScoped;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.stream.Collectors;

import com.klster.kates.domain.TestType;

@ApplicationScoped
public class TestRunRepository {

    private final Map<String, TestRun> runs = new ConcurrentHashMap<>();

    public void save(TestRun run) {
        runs.put(run.getId(), run);
    }

    public Optional<TestRun> findById(String id) {
        return Optional.ofNullable(runs.get(id));
    }

    public List<TestRun> findAll() {
        return new ArrayList<>(runs.values());
    }

    public List<TestRun> findByType(TestType type) {
        return runs.values().stream()
                .filter(r -> r.getTestType() == type)
                .collect(Collectors.toList());
    }

    public void delete(String id) {
        runs.remove(id);
    }
}
