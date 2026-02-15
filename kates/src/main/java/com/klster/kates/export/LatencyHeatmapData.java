package com.klster.kates.export;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.List;

/**
 * Time-bucketed latency distribution data for heatmap visualization.
 * Each row represents a 1-second sampling window with counts per latency bucket.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record LatencyHeatmapData(
        String runId,
        String testType,
        List<String> bucketLabels,
        List<double[]> bucketBoundaries,
        List<HeatmapRow> rows
) {
    public record HeatmapRow(
            long timestampMs,
            String phase,
            long[] counts
    ) {}

    /**
     * Creates heatmap bucket labels from boundary values.
     */
    public static List<String> buildLabels(double[] boundaries) {
        List<String> labels = new java.util.ArrayList<>(boundaries.length - 1);
        for (int i = 0; i < boundaries.length - 1; i++) {
            labels.add(formatMs(boundaries[i]) + "-" + formatMs(boundaries[i + 1]));
        }
        return labels;
    }

    /**
     * Creates boundary pairs from boundary values.
     */
    public static List<double[]> buildBoundaries(double[] boundaries) {
        List<double[]> pairs = new java.util.ArrayList<>(boundaries.length - 1);
        for (int i = 0; i < boundaries.length - 1; i++) {
            pairs.add(new double[]{boundaries[i], boundaries[i + 1]});
        }
        return pairs;
    }

    private static String formatMs(double ms) {
        if (ms >= 1000) return String.format("%.0fs", ms / 1000);
        if (ms == (long) ms) return String.format("%.0fms", ms);
        return String.format("%.1fms", ms);
    }
}
