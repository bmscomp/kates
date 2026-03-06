package com.bmscomp.kates.api;

import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.inject.Inject;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import io.smallrye.common.annotation.Blocking;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.responses.APIResponse;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.service.ClusterHealthService;
import com.bmscomp.kates.service.ConsumerGroupService;
import com.bmscomp.kates.service.TopicService;

@Path("/api/cluster")
@Produces(MediaType.APPLICATION_JSON)
@Blocking
@Tag(name = "Cluster")
public class ClusterResource {

    private final TopicService topicService;
    private final ConsumerGroupService consumerGroupService;
    private final ClusterHealthService clusterHealthService;

    @Inject
    public ClusterResource(TopicService topicService, ConsumerGroupService consumerGroupService,
                           ClusterHealthService clusterHealthService) {
        this.topicService = topicService;
        this.consumerGroupService = consumerGroupService;
        this.clusterHealthService = clusterHealthService;
    }

    @GET
    @Path("/info")
    @Operation(summary = "Get cluster info", description = "Returns cluster ID, broker count, and controller details")
    @APIResponse(responseCode = "200", description = "Cluster information")
    public Response getClusterInfo() {
        try {
            Map<String, Object> info = clusterHealthService.describeCluster();
            return Response.ok(info).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(
                            500, "Internal Server Error", "Failed to connect to Kafka cluster: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/topics")
    @Operation(summary = "List all topics")
    @APIResponse(responseCode = "200", description = "Paginated list of topic names")
    public Response getTopics(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {
        try {
            int effectiveSize = Math.min(Math.max(size, 1), 200);
            Set<String> allTopics = topicService.listTopics();
            List<String> sorted = allTopics.stream().sorted().toList();
            int start = Math.min(page * effectiveSize, sorted.size());
            int end = Math.min(start + effectiveSize, sorted.size());
            List<String> paged = sorted.subList(start, end);
            return Response.ok(Map.of(
                            "page",
                            page,
                            "size",
                            effectiveSize,
                            "total",
                            sorted.size(),
                            "count",
                            paged.size(),
                            "items",
                            paged))
                    .build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Failed to list topics: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/topics/{name}")
    @Operation(summary = "Describe a topic", description = "Returns partition details, replication, and ISR info")
    @APIResponse(responseCode = "200", description = "Topic details")
    @APIResponse(responseCode = "404", description = "Topic not found")
    public Response getTopicDetail(@Parameter(description = "Topic name") @PathParam("name") String name) {
        try {
            Map<String, Object> detail = topicService.describeTopicDetail(name);
            return Response.ok(detail).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Topic not found: " + name))
                        .build();
            }
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Failed to describe topic: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups")
    @Operation(summary = "List consumer groups")
    @APIResponse(responseCode = "200", description = "Paginated list of consumer group summaries")
    public Response getConsumerGroups(
            @Parameter(description = "Page number (0-based)") @QueryParam("page") @DefaultValue("0") int page,
            @Parameter(description = "Page size (max 200)") @QueryParam("size") @DefaultValue("50") int size) {
        try {
            int effectiveSize = Math.min(Math.max(size, 1), 200);
            List<Map<String, Object>> allGroups = consumerGroupService.listConsumerGroups();
            int start = Math.min(page * effectiveSize, allGroups.size());
            int end = Math.min(start + effectiveSize, allGroups.size());
            List<Map<String, Object>> paged = allGroups.subList(start, end);
            return Response.ok(Map.of(
                            "page",
                            page,
                            "size",
                            effectiveSize,
                            "total",
                            allGroups.size(),
                            "count",
                            paged.size(),
                            "items",
                            paged))
                    .build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(
                            500, "Internal Server Error", "Failed to list consumer groups: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups/{id}")
    @Operation(summary = "Describe a consumer group", description = "Returns members, partitions, offsets, and lag")
    @APIResponse(responseCode = "200", description = "Consumer group details")
    @APIResponse(responseCode = "404", description = "Consumer group not found")
    public Response getConsumerGroupDetail(@Parameter(description = "Group ID") @PathParam("id") String id) {
        try {
            Map<String, Object> detail = consumerGroupService.describeConsumerGroup(id);
            return Response.ok(detail).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Consumer group not found: " + id))
                        .build();
            }
            return Response.serverError()
                    .entity(ApiError.of(
                            500, "Internal Server Error", "Failed to describe consumer group: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/brokers/{id}/configs")
    @Operation(
            summary = "Get broker configuration",
            description = "Returns configuration entries for a specific broker")
    @APIResponse(responseCode = "200", description = "Broker configuration")
    public Response getBrokerConfigs(@Parameter(description = "Broker ID") @PathParam("id") int id) {
        try {
            return Response.ok(clusterHealthService.describeBrokerConfigs(id)).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(
                            500, "Internal Server Error", "Failed to describe broker configs: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/check")
    public Response clusterCheck() {
        try {
            return Response.ok(clusterHealthService.clusterHealthCheck()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Internal Server Error", "Cluster health check failed: " + e.getMessage()))
                    .build();
        }
    }
}
