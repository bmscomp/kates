package com.klster.kates.report;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Per-broker metric breakdown for a test run.
 * Projects overall test performance onto individual brokers using
 * partition leadership ratio as a weight factor, enriched with
 * replication health and controller status.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record BrokerMetrics(
        int brokerId,
        String host,
        String rack,
        boolean isController,
        int leaderPartitions,
        int replicaPartitions,
        int isrPartitions,
        int underReplicatedPartitions,
        int totalPartitions,
        double leaderSharePercent,
        double skewPercent,
        boolean skewed,
        ReportSummary metrics) {}
