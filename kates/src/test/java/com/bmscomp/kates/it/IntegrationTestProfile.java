package com.bmscomp.kates.it;

import java.util.Map;

import io.quarkus.test.junit.QuarkusTestProfile;

/**
 * Profile for integration tests running against real containers.
 *
 * db-kind is a build-time property, so it cannot be switched by a test
 * resource at runtime — the profile forces a separate augmentation with the
 * PostgreSQL driver active, Flyway owning the schema (V1..Vn actually run,
 * unlike the H2 unit-test path), and Hibernate DDL generation off.
 */
public class IntegrationTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of(
                "quarkus.datasource.db-kind", "postgresql",
                "quarkus.hibernate-orm.database.generation", "none",
                "quarkus.flyway.migrate-at-start", "true");
    }
}
