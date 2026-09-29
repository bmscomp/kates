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

    static void persistReport(
            String id, DisruptionReport report, DisruptionReportRepository repository, ObjectMapper objectMapper) {
        try {
            repository.save(toEntity(id, report, objectMapper));
            LOG.info("Persisted disruption report: " + id);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize report for persistence", e);
        }
    }

    /**
     * The row for a report. The status and grade live twice, in columns the
     * list reads and in the report JSON a GET returns, so they are only ever
     * written together.
     */
    static DisruptionReportEntity toEntity(String id, DisruptionReport report, ObjectMapper objectMapper)
            throws JsonProcessingException {
        String grade = report.getSlaVerdict() != null ? report.getSlaVerdict().grade() : null;
        String reportJson = objectMapper.writeValueAsString(report);
        String summaryJson = report.getSummary() != null ? objectMapper.writeValueAsString(report.getSummary()) : null;
        return new DisruptionReportEntity(id, report.getPlanName(), report.getStatus(), grade, reportJson, summaryJson);
    }

    static DisruptionReport loadReport(String id, DisruptionReportRepository repository, ObjectMapper objectMapper) {
        DisruptionReportEntity entity = repository.findById(id);
        if (entity == null) return null;
        return readReport(entity, objectMapper);
    }

    static DisruptionReport readReport(DisruptionReportEntity entity, ObjectMapper objectMapper) {
        try {
            return objectMapper.readValue(entity.getReportJson(), DisruptionReport.class);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to deserialize report: " + entity.getId(), e);
            return null;
        }
    }
}
