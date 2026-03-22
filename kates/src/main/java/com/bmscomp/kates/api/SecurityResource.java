package com.bmscomp.kates.api;



import jakarta.inject.Inject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
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
}
