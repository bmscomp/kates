package com.bmscomp.kates.api;



import jakarta.inject.Inject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import io.smallrye.common.annotation.Blocking;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.service.SecurityService;

@Path("/api/security")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
@Tag(name = "Security")
public class SecurityResource {

    private final SecurityService securityService;

    @Inject
    public SecurityResource(SecurityService securityService) {
        this.securityService = securityService;
    }

    @GET
    @Path("/audit")
    @Operation(summary = "Security posture audit",
            description = "Runs 15 security checks against the Kafka cluster and returns an A-F grade")
    @APIResponse(responseCode = "200", description = "Security audit report")
    public Response securityAudit() {
        try {
            return Response.ok(securityService.securityAudit()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Security audit failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/tls")
    @Operation(summary = "TLS certificate inspection",
            description = "Inspects TLS configuration, protocol versions, cipher suites, and mTLS settings")
    @APIResponse(responseCode = "200", description = "TLS inspection report")
    public Response tlsInspect() {
        try {
            return Response.ok(securityService.tlsInspect()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "TLS inspection failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/auth-test")
    @Operation(summary = "ACL authorization test",
            description = "Probes ACL rules for a specified user to verify least-privilege access")
    @APIResponse(responseCode = "200", description = "Auth test report")
    public Response authTest(
            @Parameter(description = "Kafka username to test", required = true)
            @QueryParam("user") @DefaultValue("") String username) {
        if (username == null || username.isBlank()) {
            return Response.status(400)
                    .entity(ApiError.of(400, "Bad Request", "Query parameter 'user' is required"))
                    .build();
        }
        try {
            return Response.ok(securityService.authTest(username)).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Auth test failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/pentest")
    @Operation(summary = "Penetration test",
            description = "Runs adversarial security tests against the cluster configuration")
    @APIResponse(responseCode = "200", description = "Penetration test report")
    public Response pentest(
            @Parameter(description = "Specific test to run (or 'all')")
            @QueryParam("test") @DefaultValue("all") String testName) {
        try {
            return Response.ok(securityService.pentest(testName)).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Pentest failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/compliance")
    @Operation(summary = "Compliance report",
            description = "Maps security checks to CIS Kafka Benchmark, SOC2, and PCI-DSS frameworks")
    @APIResponse(responseCode = "200", description = "Compliance report")
    public Response compliance() {
        try {
            return Response.ok(securityService.securityCompliance()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Compliance report failed: " + e.getMessage()))
                    .build();
        }
    }

    @POST
    @Path("/baseline")
    @Operation(summary = "Save security baseline",
            description = "Captures current security posture as baseline for drift detection")
    @APIResponse(responseCode = "200", description = "Baseline saved")
    public Response saveBaseline() {
        try {
            return Response.ok(securityService.saveBaseline()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Baseline save failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/drift")
    @Operation(summary = "Security drift detection",
            description = "Compares current security posture against saved baseline")
    @APIResponse(responseCode = "200", description = "Drift report")
    public Response drift() {
        try {
            return Response.ok(securityService.securityDrift()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Drift detection failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/gate")
    @Operation(summary = "Security quality gate",
            description = "CI/CD gate that exits non-zero if security grade is below threshold")
    @APIResponse(responseCode = "200", description = "Gate result")
    public Response gate(
            @Parameter(description = "Minimum passing grade (A, B, C, D, F)")
            @QueryParam("min-grade") @DefaultValue("B") String minGrade) {
        try {
            return Response.ok(securityService.securityGate(minGrade.toUpperCase())).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Security gate failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/certs")
    @Operation(summary = "Certificate check",
            description = "Inspects SSL/TLS certificate configuration across brokers")
    @APIResponse(responseCode = "200", description = "Certificate check report")
    public Response certs() {
        try {
            return Response.ok(securityService.certificateCheck()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Certificate check failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/cve")
    @Operation(summary = "CVE vulnerability check",
            description = "Checks running Kafka version against known CVEs")
    @APIResponse(responseCode = "200", description = "CVE check report")
    public Response cve() {
        try {
            return Response.ok(securityService.cveCheck()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "CVE check failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/config-diff")
    @Operation(summary = "Broker config consistency check",
            description = "Compares security-critical configuration across all brokers to detect drift")
    @APIResponse(responseCode = "200", description = "Config consistency report")
    public Response configDiff() {
        try {
            return Response.ok(securityService.configConsistency()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Config consistency check failed: " + e.getMessage()))
                    .build();
        }
    }
}
