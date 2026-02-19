package com.klster.kates.webhook;

import java.util.List;

import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

/**
 * REST endpoints for managing webhook registrations.
 * Webhooks receive HTTP POST notifications when test runs complete.
 */
@Path("/api/webhooks")
@Tag(name = "Webhooks")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class WebhookResource {

    @Inject
    WebhookService webhookService;

    @GET
    @Operation(summary = "List registered webhooks")
    public List<WebhookService.WebhookRegistration> list() {
        return webhookService.list();
    }

    @POST
    @Operation(summary = "Register a webhook")
    public Response register(WebhookService.WebhookRegistration registration) {
        if (registration.name() == null || registration.name().isBlank()) {
            return Response.status(400).entity("{\"error\":\"name is required\"}").build();
        }
        if (registration.url() == null || registration.url().isBlank()) {
            return Response.status(400).entity("{\"error\":\"url is required\"}").build();
        }
        webhookService.register(registration);
        return Response.status(201).entity(registration).build();
    }

    @DELETE
    @Path("/{name}")
    @Operation(summary = "Unregister a webhook")
    public Response unregister(@PathParam("name") String name) {
        webhookService.unregister(name);
        return Response.noContent().build();
    }
}
