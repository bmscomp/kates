package com.klster.kates.api;

import com.klster.kates.service.KafkaAdminService;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
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

    @GET
    @Path("/topics/{name}")
    public Response getTopicDetail(@PathParam("name") String name) {
        try {
            Map<String, Object> detail = kafkaAdmin.describeTopicDetail(name);
            return Response.ok(detail).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(Map.of("error", "Topic not found: " + name))
                        .build();
            }
            return Response.serverError()
                    .entity(Map.of("error", "Failed to describe topic: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups")
    public Response getConsumerGroups() {
        try {
            return Response.ok(kafkaAdmin.listConsumerGroups()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(Map.of("error", "Failed to list consumer groups: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups/{id}")
    public Response getConsumerGroupDetail(@PathParam("id") String id) {
        try {
            Map<String, Object> detail = kafkaAdmin.describeConsumerGroup(id);
            return Response.ok(detail).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(Map.of("error", "Consumer group not found: " + id))
                        .build();
            }
            return Response.serverError()
                    .entity(Map.of("error", "Failed to describe consumer group: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/brokers/{id}/configs")
    public Response getBrokerConfigs(@PathParam("id") int id) {
        try {
            return Response.ok(kafkaAdmin.describeBrokerConfigs(id)).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(Map.of("error", "Failed to describe broker configs: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/check")
    public Response clusterCheck() {
        try {
            return Response.ok(kafkaAdmin.clusterHealthCheck()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(Map.of("error", "Cluster health check failed: " + e.getMessage()))
                    .build();
        }
    }
}
