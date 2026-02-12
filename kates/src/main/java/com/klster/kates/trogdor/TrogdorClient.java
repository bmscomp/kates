package com.klster.kates.trogdor;

import com.fasterxml.jackson.databind.JsonNode;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.PUT;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;

@Path("/coordinator")
@RegisterRestClient
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public interface TrogdorClient {

    @POST
    @Path("/task/create")
    JsonNode createTask(CreateTaskRequest request);

    @GET
    @Path("/task/{taskId}")
    JsonNode getTask(@PathParam("taskId") String taskId);

    @GET
    @Path("/tasks")
    JsonNode getTasks(
            @QueryParam("firstTaskId") String firstTaskId,
            @QueryParam("maxTasks") int maxTasks);

    @PUT
    @Path("/task/{taskId}/stop")
    JsonNode stopTask(@PathParam("taskId") String taskId);

    @DELETE
    @Path("/task/{taskId}")
    JsonNode destroyTask(@PathParam("taskId") String taskId);

    class CreateTaskRequest {
        private String id;
        private Object spec;

        public CreateTaskRequest() {
        }

        public CreateTaskRequest(String id, Object spec) {
            this.id = id;
            this.spec = spec;
        }

        public String getId() {
            return id;
        }

        public void setId(String id) {
            this.id = id;
        }

        public Object getSpec() {
            return spec;
        }

        public void setSpec(Object spec) {
            this.spec = spec;
        }
    }
}
