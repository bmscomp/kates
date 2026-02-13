package com.klster.kates.report;

import com.klster.kates.domain.TestRun;
import com.klster.kates.service.TestRunRepository;
import jakarta.inject.Inject;
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

    @Inject
    public ReportResource(ReportGenerator generator, TestRunRepository repository) {
        this.generator = generator;
        this.repository = repository;
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
