package com.bmscomp.kates.report;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.engine.LatencyHistogram;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.engine.TuningTestRunner;
import com.bmscomp.kates.export.CsvExporter;
import com.bmscomp.kates.export.HeatmapExporter;
import com.bmscomp.kates.export.JunitXmlExporter;
import com.bmscomp.kates.export.LatencyHeatmapData;
import com.bmscomp.kates.service.BaselineService;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.util.MetricUtils;

/**
 * REST endpoints for test report generation, export, and comparison.
 */
@Path("/api")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Reports")
public class ReportResource {

    private final ReportGenerator generator;
    private final TestRunRepository repository;
    private final CsvExporter csvExporter;
    private final JunitXmlExporter junitXmlExporter;
    private final HeatmapExporter heatmapExporter;
    private final TestOrchestrator orchestrator;

    @Inject
    BaselineService baselineService;

    @Inject
    TuningTestRunner tuningRunner;

    @Inject
    public ReportResource(
            ReportGenerator generator,
            TestRunRepository repository,
            CsvExporter csvExporter,
            JunitXmlExporter junitXmlExporter,
            HeatmapExporter heatmapExporter,
            TestOrchestrator orchestrator) {
        this.generator = generator;
        this.repository = repository;
        this.csvExporter = csvExporter;
        this.junitXmlExporter = junitXmlExporter;
        this.heatmapExporter = heatmapExporter;
        this.orchestrator = orchestrator;
    }

    @GET
    @Path("/tests/{id}/report/regression")
    @Operation(
            summary = "Regression check against baseline",
            description = "Compares this run's metrics against the baseline for its test type")
    @APIResponse(responseCode = "200", description = "Regression report with metric deltas")
    @APIResponse(responseCode = "404", description = "Run not found or no baseline set")
    public Response getRegressionReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        Map<String, Object> result = baselineService.compareRegression(id);
        if (result == null) {
            return Response.status(404)
                    .entity(com.bmscomp.kates.api.ApiError.of(404, "Not Found", "Test run not found: " + id))
                    .build();
        }
        if (result.containsKey("error")) {
            return Response.status(404)
                    .entity(com.bmscomp.kates.api.ApiError.of(404, "Not Found", (String) result.get("error")))
                    .build();
        }
        return Response.ok(result).build();
    }

    @GET
    @Path("/tests/{id}/report/tuning")
    @Operation(
            summary = "Tuning comparison report",
            description = "Returns step-by-step parameter sweep results with best-configuration recommendation")
    @APIResponse(responseCode = "200", description = "Tuning report with ranked steps")
    @APIResponse(responseCode = "404", description = "Run not found or not a tuning test")
    @Tag(name = "Tuning")
    public Response getTuningReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TuningReport report = tuningRunner.buildReport(id);
        if (report == null) {
            return Response.status(404)
                    .entity(com.bmscomp.kates.api.ApiError.of(
                            404, "Not Found", "Run not found or not a tuning test: " + id))
                    .build();
        }
        return Response.ok(report).build();
    }

    @GET
    @Path("/tuning/types")
    @Operation(summary = "List available tuning tests")
    @Tag(name = "Tuning")
    public Response listTuningTypes() {
        return Response.ok(TuningTestRunner.availableTuningTests()).build();
    }

    @GET
    @Path("/tests/{id}/report")
    @Operation(summary = "Generate a full test report")
    @APIResponse(responseCode = "200", description = "Complete test report with summary, phases, and SLA verdict")
    public Response getReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        return Response.ok(report).build();
    }

    @GET
    @Path("/tests/{id}/report/markdown")
    @Produces("text/markdown")
    @Operation(summary = "Export report as Markdown")
    public Response getMarkdownReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String markdown = generator.toMarkdown(report);
        return Response.ok(markdown).build();
    }

    @GET
    @Path("/tests/{id}/report/summary")
    @Operation(summary = "Get report summary only")
    public Response getReportSummary(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        return Response.ok(report.getSummary()).build();
    }

    @GET
    @Path("/tests/{id}/report/csv")
    @Produces("text/csv")
    @Operation(summary = "Export report as CSV")
    public Response getCsvReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String csv = csvExporter.export(report);
        return Response.ok(csv)
                .header("Content-Disposition", "attachment; filename=\"kates-report-" + id + ".csv\"")
                .build();
    }

    @GET
    @Path("/tests/{id}/report/junit")
    @Produces("application/xml")
    @Operation(
            summary = "Export report as JUnit XML",
            description = "Returns test results in JUnit XML format for CI integration")
    public Response getJunitReport(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String xml = junitXmlExporter.export(report);
        return Response.ok(xml)
                .header("Content-Disposition", "attachment; filename=\"kates-report-" + id + ".xml\"")
                .build();
    }

    @GET
    @Path("/tests/{id}/report/heatmap")
    @Operation(summary = "Get latency heatmap", description = "Returns latency distribution data as JSON or CSV")
    public Response getHeatmapReport(
            @Parameter(description = "Test run ID") @PathParam("id") String id,
            @Parameter(description = "Output format: json or csv") @QueryParam("format") @DefaultValue("json")
                    String format) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));

        java.util.List<LatencyHeatmapData.HeatmapRow> rows = orchestrator.getHeatmapRows(id);
        if (rows.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("No heatmap data available for run: " + id)
                    .build();
        }

        double[] boundaries = LatencyHistogram.HEATMAP_BOUNDARIES;
        LatencyHeatmapData data = new LatencyHeatmapData(
                id,
                run.getTestType() != null ? run.getTestType().name() : null,
                LatencyHeatmapData.buildLabels(boundaries),
                LatencyHeatmapData.buildBoundaries(boundaries),
                rows);

        if ("csv".equalsIgnoreCase(format)) {
            String csv = heatmapExporter.exportCsv(data);
            return Response.ok(csv, "text/csv")
                    .header("Content-Disposition", "attachment; filename=\"kates-heatmap-" + id + ".csv\"")
                    .build();
        }

        String json = heatmapExporter.exportJson(data);
        return Response.ok(json, MediaType.APPLICATION_JSON).build();
    }

    @GET
    @Path("/tests/{id}/report/brokers")
    @Operation(summary = "Get per-broker metrics")
    public Response getBrokerMetrics(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        List<BrokerMetrics> metrics = report.getBrokerMetrics();
        if (metrics == null || metrics.isEmpty()) {
            return Response.ok(List.of()).build();
        }
        return Response.ok(metrics).build();
    }

    @GET
    @Path("/tests/{id}/report/snapshot")
    @Operation(summary = "Get cluster snapshot at test time")
    public Response getClusterSnapshot(@Parameter(description = "Test run ID") @PathParam("id") String id) {
        TestRun run =
                repository.findById(id).orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        ClusterSnapshot snapshot = report.getClusterSnapshot();
        if (snapshot == null) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("No cluster snapshot available for run: " + id)
                    .build();
        }
        return Response.ok(snapshot).build();
    }

    @GET
    @Path("/reports/compare")
    @Operation(summary = "Compare test runs", description = "Side-by-side comparison of 2+ runs with percentage deltas")
    public Response compare(
            @Parameter(description = "Comma-separated run IDs (min 2)", required = true) @QueryParam("ids")
                    String ids) {
        if (ids == null || ids.isBlank()) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity("Query param 'ids' is required")
                    .build();
        }

        List<String> runIds = Arrays.stream(ids.split(","))
                .map(String::trim)
                .filter(s -> !s.isEmpty())
                .toList();

        if (runIds.size() < 2) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity("At least 2 run IDs required")
                    .build();
        }

        List<ComparisonReport.ComparisonEntry> entries = new ArrayList<>();
        for (String runId : runIds) {
            TestRun run = repository
                    .findById(runId)
                    .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));
            TestReport report = generator.generate(run);
            entries.add(new ComparisonReport.ComparisonEntry(
                    runId,
                    run.getScenarioName(),
                    run.getTestType() != null ? run.getTestType().name() : null,
                    run.getBackend(),
                    report.getSummary()));
        }

        ComparisonReport comparison = new ComparisonReport();
        comparison.setBaselineRunId(runIds.get(0));
        comparison.setRuns(entries);

        ReportSummary baseline = entries.get(0).summary();
        ReportSummary latest = entries.get(entries.size() - 1).summary();
        comparison.setDeltas(computeDeltas(baseline, latest));

        return Response.ok(comparison).build();
    }

    private Map<String, Double> computeDeltas(ReportSummary baseline, ReportSummary latest) {
        Map<String, Double> deltas = new LinkedHashMap<>();
        deltas.put(
                "throughputRecPerSec",
                MetricUtils.pctChange(baseline.avgThroughputRecPerSec(), latest.avgThroughputRecPerSec()));
        deltas.put("avgLatencyMs", MetricUtils.pctChange(baseline.avgLatencyMs(), latest.avgLatencyMs()));
        deltas.put("p99LatencyMs", MetricUtils.pctChange(baseline.p99LatencyMs(), latest.p99LatencyMs()));
        deltas.put("maxLatencyMs", MetricUtils.pctChange(baseline.maxLatencyMs(), latest.maxLatencyMs()));
        deltas.put("totalRecords", MetricUtils.pctChange(baseline.totalRecords(), latest.totalRecords()));
        return deltas;
    }
}
