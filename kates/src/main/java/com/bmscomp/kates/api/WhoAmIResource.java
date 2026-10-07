package com.bmscomp.kates.api;

import java.util.List;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.quarkus.security.identity.SecurityIdentity;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.security.ApiKeys;
import com.bmscomp.kates.security.KatesIdentities;
import com.bmscomp.kates.security.KatesPrincipal;

/**
 * Who the caller's API key is (plans/mcp-server.md §6.2). kates mcp reads it
 * to learn whether it runs on an agent's key or a person's, and what that key
 * may do.
 */
@Path("/api/whoami")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Security")
public class WhoAmIResource {

    @Inject
    SecurityIdentity identity;

    @Inject
    ApiKeys keys;

    @GET
    @Operation(
            summary = "Who the API key is",
            description = "The principal that holds the caller's key: its name, whether a person (human) or an"
                    + " agent holds it, its scopes, and the Kafka clusterIds it may act on, absent when it may act"
                    + " on any. securityEnabled is false when the API checks no key, and every caller is then"
                    + " anonymous with every scope.")
    @APIResponse(responseCode = "200", description = "The caller's principal")
    @APIResponse(responseCode = "401", description = "No API key")
    @APIResponse(responseCode = "403", description = "An API key no principal holds")
    public WhoAmI whoAmI() {
        return WhoAmI.of(KatesIdentities.principalOf(identity), keys.securityEnabled());
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record WhoAmI(
            String principal,
            String principalType,
            List<String> scopes,
            List<String> allowedClusterIds,
            boolean securityEnabled) {

        static WhoAmI of(KatesPrincipal p, boolean securityEnabled) {
            return new WhoAmI(
                    p.name(),
                    p.type().wireName(),
                    p.scopes(),
                    p.allowedClusterIds().isEmpty() ? null : p.allowedClusterIds(),
                    securityEnabled);
        }
    }
}
