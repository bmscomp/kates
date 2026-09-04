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
            // Error bodies — returned by every failure path in every resource,
            // so an unregistered ApiError turns each 4xx into an empty object.
            com.bmscomp.kates.api.ApiError.class,
            // Trend analysis
            com.bmscomp.kates.trend.TrendResponse.class,
            com.bmscomp.kates.trend.TrendResponse.DataPoint.class,
            com.bmscomp.kates.trend.TrendResponse.Regression.class,
            com.bmscomp.kates.trend.PhaseTrendResponse.class,
            com.bmscomp.kates.trend.PhaseTrendResponse.PhaseTrend.class,
            com.bmscomp.kates.trend.BrokerTrendResponse.class,
            // Resilience
            com.bmscomp.kates.resilience.ResilienceReport.class,
            com.bmscomp.kates.resilience.ResilienceTestRequest.class,
            com.bmscomp.kates.resilience.ResilienceScenarios.Scenario.class,
            com.bmscomp.kates.chaos.ProbeResult.class,
            com.bmscomp.kates.chaos.ProbeSpec.class,
            com.bmscomp.kates.chaos.ChaosOutcome.class,
            com.bmscomp.kates.chaos.CompoundChaosOrchestrator.ProviderOutcome.class,
            com.bmscomp.kates.chaos.CompoundChaosOrchestrator.CompoundFault.class,
            com.bmscomp.kates.chaos.CompoundChaosOrchestrator.CompoundOutcome.class,
            com.bmscomp.kates.chaos.StrimziStateTracker.ReplicationHealth.class,
            // Pod-level timeline and recovery timings, embedded in every step report
            com.bmscomp.kates.chaos.K8sPodWatcher.PodEvent.class,
            com.bmscomp.kates.chaos.K8sPodWatcher.RecoveryMetrics.class,
            // Kafka admin request bodies
            com.bmscomp.kates.api.KafkaClientResource.CreateTopicRequest.class,
            com.bmscomp.kates.api.KafkaClientResource.AlterTopicRequest.class,
            com.bmscomp.kates.api.KafkaClientResource.ProduceRequest.class,
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
            // Disruption payloads. Nested and field types need listing too:
            // @RegisterForReflection covers the named class only, so a report
            // whose StepReport is missing renders with an empty steps array.
            com.bmscomp.kates.disruption.DisruptionPlan.class,
            com.bmscomp.kates.disruption.DisruptionPlan.DisruptionStep.class,
            com.bmscomp.kates.disruption.DisruptionReport.class,
            com.bmscomp.kates.disruption.DisruptionReport.StepReport.class,
            com.bmscomp.kates.disruption.DisruptionReport.DisruptionSummary.class,
            com.bmscomp.kates.disruption.IsrSnapshot.Metrics.class,
            com.bmscomp.kates.disruption.IsrSnapshot.Entry.class,
            com.bmscomp.kates.disruption.LagSnapshot.Metrics.class,
            com.bmscomp.kates.disruption.LagSnapshot.Entry.class,
            com.bmscomp.kates.chaos.FaultSpec.class,
            com.bmscomp.kates.chaos.DisruptionType.class,
            com.bmscomp.kates.disruption.IsrSnapshot.class,
            com.bmscomp.kates.disruption.LagSnapshot.class,
            com.bmscomp.kates.disruption.SlaGrader.SlaVerdict.class,
            com.bmscomp.kates.disruption.SlaGrader.SlaViolation.class,
            com.bmscomp.kates.disruption.DisruptionImpactScorer.ImpactScore.class,
            com.bmscomp.kates.disruption.AutoRollbackGuard.RollbackDecision.class,
            com.bmscomp.kates.disruption.PrometheusMetricsCapture.MetricsSnapshot.class,
            com.bmscomp.kates.disruption.ChaosTemplateCatalog.TemplateInfo.class,
            com.bmscomp.kates.disruption.DisruptionEventBus.DisruptionEvent.class,
            // Dry-run preview and validation results
            com.bmscomp.kates.disruption.DisruptionSafetyGuard.DryRunResult.class,
            com.bmscomp.kates.disruption.DisruptionSafetyGuard.StepPreview.class,
            com.bmscomp.kates.disruption.DisruptionSafetyGuard.ValidationResult.class,
            // Disruption request bodies
            com.bmscomp.kates.disruption.DisruptionDtos.CompoundChaosRequest.class,
            com.bmscomp.kates.disruption.DisruptionDtos.CompoundFaultEntry.class,
            com.bmscomp.kates.disruption.DisruptionDtos.CreateDisruptionScheduleRequest.class,
            // Litmus custom resources. The kubernetes-client extension registers
            // the CustomResource subclasses; their spec and status models are
            // plain POJOs it cannot see through, and they are what actually gets
            // serialised into the CR sent to the cluster.
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.AppInfo.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Experiment.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.ExperimentSpec.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Components.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.EnvVar.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Probe.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.CmdProbe.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.CmdProbeInputs.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.Comparator.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineSpec.RunProperties.class,
            com.bmscomp.kates.chaos.litmus.ChaosEngineStatus.class,
            com.bmscomp.kates.chaos.litmus.ChaosResultStatus.class,
            com.bmscomp.kates.chaos.litmus.ChaosResultStatus.ExperimentStatus.class,
            // Outbox event payload (serialized to JSON, read back by the poller)
            com.bmscomp.kates.domain.events.TestEvent.class
        })
public class NativePayloadReflectionConfig {}
