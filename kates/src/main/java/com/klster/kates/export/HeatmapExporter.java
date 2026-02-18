package com.klster.kates.export;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.ObjectMapper;

/**
 * Exports {@link LatencyHeatmapData} in JSON or CSV format for heatmap visualization.
 */
@ApplicationScoped
public class HeatmapExporter {

    private final ObjectMapper objectMapper;

    @Inject
    public HeatmapExporter(ObjectMapper objectMapper) {
        this.objectMapper = objectMapper;
    }

    /**
     * JSON export suitable for Grafana heatmap panels.
     */
    public String exportJson(LatencyHeatmapData data) {
        try {
            return objectMapper.writerWithDefaultPrettyPrinter().writeValueAsString(data);
        } catch (Exception e) {
            throw new RuntimeException("Failed to serialize heatmap JSON", e);
        }
    }

    /**
     * CSV export for spreadsheets. Columns = latency buckets, rows = time windows.
     */
    public String exportCsv(LatencyHeatmapData data) {
        StringBuilder sb = new StringBuilder();

        sb.append("timestamp,phase");
        for (String label : data.bucketLabels()) {
            sb.append(",").append(csvEscape(label));
        }
        sb.append("\n");

        for (LatencyHeatmapData.HeatmapRow row : data.rows()) {
            sb.append(row.timestampMs());
            sb.append(",").append(csvEscape(row.phase() != null ? row.phase() : ""));
            for (long count : row.counts()) {
                sb.append(",").append(count);
            }
            sb.append("\n");
        }

        return sb.toString();
    }

    private String csvEscape(String value) {
        if (value.contains(",") || value.contains("\"") || value.contains("\n")) {
            return "\"" + value.replace("\"", "\"\"") + "\"";
        }
        return value;
    }
}
