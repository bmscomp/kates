package com.bmscomp.kates.it;

import java.util.Map;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import org.testcontainers.containers.PostgreSQLContainer;

/** Real PostgreSQL for integration tests — jdbc jsonb/GIN semantics included. */
public class PostgresTestResource implements QuarkusTestResourceLifecycleManager {

    private PostgreSQLContainer<?> postgres;
    private static volatile PostgreSQLContainer<?> current;

    /** The container of the class running now, for a test that pauses it. */
    static PostgreSQLContainer<?> container() {
        return current;
    }

    @Override
    public Map<String, String> start() {
        postgres = new PostgreSQLContainer<>("postgres:16-alpine")
                .withDatabaseName("kates")
                .withUsername("kates")
                .withPassword("kates");
        postgres.start();
        current = postgres;
        return Map.of(
                "quarkus.datasource.jdbc.url", postgres.getJdbcUrl(),
                "quarkus.datasource.username", postgres.getUsername(),
                "quarkus.datasource.password", postgres.getPassword());
    }

    @Override
    public void stop() {
        if (postgres != null) {
            postgres.stop();
        }
    }
}
