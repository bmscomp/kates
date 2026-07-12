package com.bmscomp.kates.config;

import io.quarkus.runtime.StartupEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;
import org.flywaydb.core.Flyway;
import org.flywaydb.core.api.MigrationInfo;
import org.flywaydb.core.api.MigrationInfoService;
import org.jboss.logging.Logger;

@ApplicationScoped
public class DatabaseMigrationValidator {

    private static final Logger LOG = Logger.getLogger(DatabaseMigrationValidator.class);

    @Inject
    Flyway flyway;

    void onStart(@Observes StartupEvent ev) {
        try {
            LOG.info("Running explicit Flyway validation checks...");
            flyway.validate();
            LOG.info("Flyway validation passed.");

            MigrationInfoService info = flyway.info();
            MigrationInfo current = info.current();
            if (current != null) {
                LOG.infof("Database is up-to-date at version: %s (%s)", current.getVersion(), current.getDescription());
            } else {
                LOG.warn("No Flyway migrations have been applied to the database yet.");
            }
        } catch (Exception e) {
            LOG.error("CRITICAL: Database migration validation failed! " + e.getMessage(), e);
            throw e; // Fail startup if validation fails
        }
    }
}
