package com.bmscomp.kates.api;

import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import io.smallrye.common.annotation.Blocking;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.audit.NotAudited;
import com.bmscomp.kates.compliance.ComplianceCheck;
import com.bmscomp.kates.compliance.InvalidReferenceException;
import com.bmscomp.kates.compliance.KafkaReferenceReader;
import com.bmscomp.kates.security.Scopes;

/**
 * Checks the cluster against a KafkaReference. A POST only because the
 * reference is a body: the check writes nothing, anywhere, and stores nothing.
 */
@RolesAllowed(Scopes.READ)
@Path("/api/conformity")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
@Tag(name = "Compliance")
public class ComplianceResource {

    private final KafkaReferenceReader reader;
    private final ComplianceCheck check;

    @Inject
    public ComplianceResource(KafkaReferenceReader reader, ComplianceCheck check) {
        this.reader = reader;
        this.check = check;
    }

    @NotAudited("reads the cluster and stores nothing")
    @POST
    @Path("/check")
    @Consumes(MediaType.APPLICATION_JSON)
    @Operation(
            summary = "Check the cluster against a reference",
            description =
                    "Reads the cluster read-only and reports each declared rule; the verdict is PASS, FAIL or UNKNOWN")
    @APIResponse(responseCode = "200", description = "The report, whatever the verdict")
    @APIResponse(responseCode = "400", description = "A reference the check cannot run, each field's reason by path")
    @APIResponse(responseCode = "409", description = "The reference is for another cluster, or its Kafka is missing")
    @APIResponse(responseCode = "413", description = "A reference over 256 KiB")
    @APIResponse(
            responseCode = "503",
            description = "The Kubernetes API does not answer, or refuses the Kates API the Kafka CR's get")
    public Response check(byte[] body) {
        try {
            return Response.ok(check.check(reader.read(body))).build();
        } catch (InvalidReferenceException e) {
            return Response.status(e.getError().getStatus())
                    .entity(e.getError())
                    .build();
        }
    }
}
