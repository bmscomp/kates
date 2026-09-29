package com.bmscomp.kates.trogdor;

import java.util.List;
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

import com.fasterxml.jackson.databind.JsonNode;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;

/**
 * The Trogdor coordinator's REST API, as {@code CoordinatorRestResource} in
 * apache/kafka's trogdor module serves it (the same from 2.8 to trunk).
 *
 * <p>Reading, stopping and destroying a task used paths the coordinator never
 * served ({@code /task/{id}}, {@code /task/{id}/stop}), so every poll was a
 * 404 and a task only ended when the timeout reaper stopped it. Note the
 * singular {@code /task} for create and stop against the plural
 * {@code /tasks} for the rest: that is the coordinator's own naming.
 */
@Path("/coordinator")
@RegisterRestClient
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public interface TrogdorClient {

    @POST
    @Path("/task/create")
    JsonNode createTask(CreateTaskRequest request);

    /** A task's state; the coordinator answers 404 for a task it does not know. */
    @GET
    @Path("/tasks/{taskId}")
    JsonNode getTask(@PathParam("taskId") String taskId);

    /**
     * The tasks matching every given filter, as {@code {"tasks": {id: state}}}.
     * A null filter is left out of the query, and the coordinator then applies
     * no such filter; the times are epoch milliseconds.
     */
    @GET
    @Path("/tasks")
    JsonNode getTasks(
            @QueryParam("taskId") List<String> taskIds,
            @QueryParam("firstStartMs") Long firstStartMs,
            @QueryParam("lastStartMs") Long lastStartMs,
            @QueryParam("firstEndMs") Long firstEndMs,
            @QueryParam("lastEndMs") Long lastEndMs,
            @QueryParam("state") String state);

    @PUT
    @Path("/task/stop")
    JsonNode stopTask(StopTaskRequest request);

    /** Makes the coordinator forget a task and destroy its workers, running or not. */
    @DELETE
    @Path("/tasks")
    JsonNode destroyTask(@QueryParam("taskId") String taskId);

    class CreateTaskRequest {
        private String id;
        private Object spec;

        public CreateTaskRequest() {}

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

    /** The body of {@code PUT /coordinator/task/stop}. */
    record StopTaskRequest(String id) {}
}
