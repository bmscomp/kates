package com.klster.kates.api;

import com.klster.kates.service.KafkaAdminService;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import java.util.Map;
import java.util.Set;

@Path("/api/cluster")
@Produces(MediaType.APPLICATION_JSON)
public class ClusterResource {

    private final KafkaAdminService kafkaAdmin;

    @Inject
    public ClusterResource(KafkaAdminService kafkaAdmin) {
        this.kafkaAdmin = kafkaAdmin;
    }

    @GET
    @Path("/info")
    public Response getClusterInfo() {
        try {
            Map<String, Object> info = kafkaAdmin.describeCluster();
            return Response.ok(info).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(Map.of("error", "Failed to connect to Kafka cluster: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/topics")
    public Response getTopics() {
        try {
            Set<String> topics = kafkaAdmin.listTopics();
            return Response.ok(topics).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(Map.of("error", "Failed to list topics: " + e.getMessage()))
                    .build();
        }
    }
}
