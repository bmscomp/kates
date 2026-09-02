package com.bmscomp.kates.config;

import jakarta.inject.Singleton;

import io.quarkus.flyway.FlywayConfigurationCustomizer;
import org.flywaydb.core.api.configuration.FluentConfiguration;
import org.flywaydb.database.postgresql.PostgreSQLConfigurationExtension;

/**
 * Switches Flyway's PostgreSQL schema-history lock from transactional to
 * session-level, which is what lets V20 build its indexes CONCURRENTLY.
 *
 * <p><b>The deadlock this removes.</b> Flyway locks the schema-history table
 * for the duration of a migration group, and on PostgreSQL it does so inside a
 * transaction by default. An open transaction holds a snapshot;
 * {@code CREATE INDEX CONCURRENTLY} waits for every snapshot older than itself
 * to be released before it can finish. So the migration waits for the lock
 * transaction, and the lock transaction cannot commit until the migration
 * finishes. Neither side times out — Postgres is not deadlocked in a way it can
 * detect, both are simply waiting — so boot hangs forever
 * (<a href="https://github.com/flyway/flyway/issues/3508">flyway#3508</a>).
 *
 * <p>What that looked like here: the native image built fine, started, and then
 * never served a request. No error, no stack trace, nothing in the log at all —
 * boot-time categories sit at WARN, so a boot that blocks before Quarkus prints
 * its startup line prints nothing whatsoever. The only visible symptom was a
 * container that stayed running with a dead port until the smoke test gave up.
 *
 * <p>Setting the lock to session level (an advisory lock held on the connection
 * rather than inside a transaction) holds no snapshot, so CONCURRENTLY
 * completes. The lock is still exclusive across processes, which is the
 * property that matters when more than one replica boots at once.
 *
 * <p>Quarkus exposes no {@code quarkus.flyway.*} key for this, so it goes
 * through the extension's own configuration API.
 */
@Singleton
public class FlywayPostgresLockConfig implements FlywayConfigurationCustomizer {

    @Override
    public void customize(FluentConfiguration configuration) {
        PostgreSQLConfigurationExtension postgres =
                configuration.getConfigurationExtension(PostgreSQLConfigurationExtension.class);
        if (postgres != null) {
            postgres.setTransactionalLock(false);
        }
    }
}
