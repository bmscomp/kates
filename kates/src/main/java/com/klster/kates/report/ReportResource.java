package com.klster.kates.report;

import com.klster.kates.domain.TestRun;
import com.klster.kates.engine.LatencyHistogram;
import com.klster.kates.engine.TestOrchestrator;
import com.klster.kates.export.CsvExporter;
import com.klster.kates.export.HeatmapExporter;
import com.klster.kates.export.JunitXmlExporter;
import com.klster.kates.export.LatencyHeatmapData;
import com.klster.kates.service.TestRunRepository;
import jakarta.inject.Inject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * REST endpoints for test report generation, export, and comparison.
 */
@Path("/api")
@Produces(MediaType.APPLICATION_JSON)
public class ReportResource {

    private final ReportGenerator generator;
    private final TestRunRepository repository;
    private final CsvExporter csvExporter;
    private final JunitXmlExporter junitXmlExporter;
    private final HeatmapExporter heatmapExporter;
    private final TestOrchestrator orchestrator;

    @Inject
    public ReportResource(ReportGenerator generator, TestRunRepository repository,
                          CsvExporter csvExporter, JunitXmlExporter junitXmlExporter,
                          HeatmapExporter heatmapExporter, TestOrchestrator orchestrator) {
        this.generator = generator;
        this.repository = repository;
        this.csvExporter = csvExporter;
        this.junitXmlExporter = junitXmlExporter;
        this.heatmapExporter = heatmapExporter;
        this.orchestrator = orchestrator;
    }

    @GET
    @Path("/tests/{id}/report")
    public Response getReport(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        return Response.ok(report).build();
    }

    @GET
    @Path("/tests/{id}/report/markdown")
    @Produces("text/markdown")
    public Response getMarkdownReport(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String markdown = generator.toMarkdown(report);
        return Response.ok(markdown).build();
    }

    @GET
    @Path("/tests/{id}/report/summary")
    public Response getReportSummary(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        return Response.ok(report.getSummary()).build();
    }

    @GET
    @Path("/tests/{id}/report/csv")
    @Produces("text/csv")
    public Response getCsvReport(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String csv = csvExporter.export(report);
        return Response.ok(csv)
                .header("Content-Disposition", "attachment; filename=\"kates-report-" + id + ".csv\"")
                .build();
    }

    @GET
    @Path("/tests/{id}/report/junit")
    @Produces("application/xml")
    public Response getJunitReport(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        String xml = junitXmlExporter.export(report);
        return Response.ok(xml)
                .header("Content-Disposition", "attachment; filename=\"kates-report-" + id + ".xml\"")
                .build();
    }

    @GET
    @Path("/tests/{id}/report/heatmap")
    public Response getHeatmapReport(@PathParam("id") String id,
                                     @QueryParam("format") @DefaultValue("json") String format) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));

        java.util.List<LatencyHeatmapData.HeatmapRow> rows = orchestrator.getHeatmapRows(id);
        if (rows.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("No heatmap data available for run: " + id).build();
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
                    .header("Content-Disposition",
                            "attachment; filename=\"kates-heatmap-" + id + ".csv\"")
                    .build();
        }

        String json = heatmapExporter.exportJson(data);
        return Response.ok(json, MediaType.APPLICATION_JSON).build();
    }

    @GET
    @Path("/tests/{id}/report/brokers")
    public Response getBrokerMetrics(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        List<BrokerMetrics> metrics = report.getBrokerMetrics();
        if (metrics == null || metrics.isEmpty()) {
            return Response.ok(List.of()).build();
        }
        return Response.ok(metrics).build();
    }

    @GET
    @Path("/tests/{id}/report/snapshot")
    public Response getClusterSnapshot(@PathParam("id") String id) {
        TestRun run = repository.findById(id)
                .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + id));
        TestReport report = generator.generate(run);
        ClusterSnapshot snapshot = report.getClusterSnapshot();
        if (snapshot == null) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("No cluster snapshot available for run: " + id).build();
        }
        return Response.ok(snapshot).build();
    }

    @GET
    @Path("/reports/compare")
    public Response compare(@QueryParam("ids") String ids) {
        if (ids == null || ids.isBlank()) {
            return Response.status(Response.Status.BAD_REQUEST).entity("Query param 'ids' is required").build();
        }

        List<String> runIds = Arrays.stream(ids.split(","))
                .map(String::trim)
                .filter(s -> !s.isEmpty())
                .toList();

        if (runIds.size() < 2) {
            return Response.status(Response.Status.BAD_REQUEST).entity("At least 2 run IDs required").build();
        }

        List<ComparisonReport.ComparisonEntry> entries = new ArrayList<>();
        for (String runId : runIds) {
            TestRun run = repository.findById(runId)
                    .orElseThrow(() -> new IllegalArgumentException("Test run not found: " + runId));
            TestReport report = generator.generate(run);
            entries.add(new ComparisonReport.ComparisonEntry(
                    runId,
                    run.getScenarioName(),
                    run.getTestType() != null ? run.getTestType().name() : null,
                    run.getBackend(),
                    report.getSummary()
            ));
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
        deltas.put("throughputRecPerSec", pctChange(baseline.avgThroughputRecPerSec(), latest.avgThroughputRecPerSec()));
        deltas.put("avgLatencyMs", pctChange(baseline.avgLatencyMs(), latest.avgLatencyMs()));
        deltas.put("p99LatencyMs", pctChange(baseline.p99LatencyMs(), latest.p99LatencyMs()));
        deltas.put("maxLatencyMs", pctChange(baseline.maxLatencyMs(), latest.maxLatencyMs()));
        deltas.put("totalRecords", pctChange(baseline.totalRecords(), latest.totalRecords()));
        return deltas;
    }

    private double pctChange(double base, double current) {
        if (base == 0) return current == 0 ? 0 : 100.0;
        return ((current - base) / base) * 100.0;
    }
}
