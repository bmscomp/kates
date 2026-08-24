package com.bmscomp.kates.config;

import io.quarkus.runtime.annotations.RegisterForReflection;

/**
 * Reflection registration for payload types Quarkus cannot discover on its own.
 *
 * <p>{@link NativeReflectionConfig} covers the core test-run domain. This holder
 * covers the rest of what crosses the wire: reports, exports, disruption and
 * playbook payloads. They need registering because nearly every endpoint returns
 * {@code Response.ok(...)} rather than a typed signature, so the build-time
 * scanner has nothing to go on — and an unregistered type serializes as an empty
 * object (or throws) in native mode ONLY, long after the JVM tests went green.
 *
 * <p>{@code NativeReflectionRegistryTest} fails the build when a payload class in
 * these packages is missing here.
 */
@RegisterForReflection(
        targets = {
            // Reports
            com.bmscomp.kates.report.TestReport.class,
            com.bmscomp.kates.report.ReportSummary.class,
            com.bmscomp.kates.report.PhaseReport.class,
            com.bmscomp.kates.report.BrokerMetrics.class,
            com.bmscomp.kates.report.ClusterSnapshot.class,
            com.bmscomp.kates.report.ClusterSnapshot.BrokerInfo.class,
            com.bmscomp.kates.report.ClusterSnapshot.PartitionAssignment.class,
            com.bmscomp.kates.report.ComparisonReport.class,
            com.bmscomp.kates.report.ComparisonReport.ComparisonEntry.class,
            com.bmscomp.kates.report.TuningReport.class,
            com.bmscomp.kates.report.TuningReport.TuningStep.class,
            // Exports
            com.bmscomp.kates.export.LatencyHeatmapData.class,
            com.bmscomp.kates.export.LatencyHeatmapData.HeatmapRow.class,
            // Bulk API payloads
            com.bmscomp.kates.domain.BulkCreateResponse.TestRunSummary.class,
            // Trogdor backend payloads (sent to a Trogdor coordinator as JSON)
            com.bmscomp.kates.trogdor.spec.TrogdorSpec.class,
            com.bmscomp.kates.trogdor.spec.ProduceBenchSpec.class,
            com.bmscomp.kates.trogdor.spec.ProduceBenchSpec.TopicSpec.class,
            com.bmscomp.kates.trogdor.spec.ProduceBenchSpec.KeyGeneratorSpec.class,
            com.bmscomp.kates.trogdor.spec.ProduceBenchSpec.ValueGeneratorSpec.class,
            com.bmscomp.kates.trogdor.spec.ConsumeBenchSpec.class,
            com.bmscomp.kates.trogdor.spec.RoundTripWorkloadSpec.class,
            com.bmscomp.kates.trogdor.TrogdorClient.CreateTaskRequest.class,
            // Playbook YAML (loaded by DisruptionPlaybookCatalog, field-mapped)
            com.bmscomp.kates.disruption.DisruptionPlaybookCatalog.PlaybookEntry.class,
            com.bmscomp.kates.disruption.DisruptionPlaybookCatalog.PlaybookStep.class,
            com.bmscomp.kates.disruption.DisruptionPlaybookCatalog.PlaybookFaultSpec.class,
            // Disruption payloads
            com.bmscomp.kates.disruption.DisruptionPlan.class,
            com.bmscomp.kates.disruption.DisruptionReport.class,
            com.bmscomp.kates.chaos.FaultSpec.class,
            com.bmscomp.kates.chaos.DisruptionType.class,
            // Outbox event payload (serialized to JSON, read back by the poller)
            com.bmscomp.kates.domain.events.TestEvent.class
        })
public class NativePayloadReflectionConfig {}
