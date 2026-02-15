package com.klster.kates.report;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Per-broker metric breakdown for a test run.
 * Projects overall test performance onto individual brokers using
 * partition leadership ratio as a weight factor.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record BrokerMetrics(
        int brokerId,
        String host,
        String rack,
        int leaderPartitions,
        int totalPartitions,
        double leaderSharePercent,
        ReportSummary metrics,
        boolean skewed
) {}
