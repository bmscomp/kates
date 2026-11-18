package com.bmscomp.kates.api;

import java.util.List;
import jakarta.annotation.security.RolesAllowed;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.quorum.Quorum;
import com.bmscomp.kates.quorum.QuorumMember;
import com.bmscomp.kates.security.Scopes;

/** The Kates members' Raft quorum, every member as it reports itself. */
@RolesAllowed(Scopes.READ)
@Path("/api/quorum")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Health")
public class QuorumResource {

    @Inject
    Quorum quorum;

    @GET
    @Operation(
            summary = "Every quorum member's Raft state",
            description = "Any member answers: it asks each member, itself included, for its Ratis group info."
                    + " A member that does not answer within 800 ms has only error set. enabled is false, and"
                    + " members empty, on a Kates that runs alone.")
    @APIResponse(responseCode = "200", description = "The members")
    public QuorumStatus status() {
        return new QuorumStatus(quorum.memberId() != null, quorum.memberId(), quorum.members());
    }

    public record QuorumStatus(boolean enabled, String answeredBy, List<QuorumMember.View> members) {}
}
