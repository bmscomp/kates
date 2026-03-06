package com.bmscomp.kates.disruption;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.jboss.logging.Logger;

/**
 * Shared persistence helpers for disruption reports.
 */
final class DisruptionPersistence {

    private static final Logger LOG = Logger.getLogger(DisruptionPersistence.class);

    private DisruptionPersistence() {}

    static void persistReport(String id, DisruptionReport report,
                              DisruptionReportRepository repository, ObjectMapper objectMapper) {
        try {
            String grade = report.getSlaVerdict() != null ? report.getSlaVerdict().grade() : null;
            String reportJson = objectMapper.writeValueAsString(report);
            String summaryJson = report.getSummary() != null
                    ? objectMapper.writeValueAsString(report.getSummary()) : null;

            DisruptionReportEntity entity = new DisruptionReportEntity(
                    id, report.getPlanName(), report.getStatus(), grade, reportJson, summaryJson);
            repository.save(entity);
            LOG.info("Persisted disruption report: " + id);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize report for persistence", e);
        }
    }

    static DisruptionReport loadReport(String id, DisruptionReportRepository repository,
                                        ObjectMapper objectMapper) {
        DisruptionReportEntity entity = repository.findById(id);
        if (entity == null) return null;
        try {
            return objectMapper.readValue(entity.getReportJson(), DisruptionReport.class);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to deserialize report: " + id, e);
            return null;
        }
    }
}
