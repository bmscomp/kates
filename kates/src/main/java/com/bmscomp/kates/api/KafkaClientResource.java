package com.bmscomp.kates.api;

import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PATCH;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.parameters.Parameter;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.service.ClusterHealthService;
import com.bmscomp.kates.service.ConsumerGroupService;
import com.bmscomp.kates.service.KafkaClientService;
import com.bmscomp.kates.service.TopicService;

/**
 * Interactive Kafka client endpoints: produce, consume, and full topic/broker inspection.
 * These power the `kates kafka` CLI command suite.
 */
@Path("/api/kafka")
@Tag(name = "Kafka Client")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class KafkaClientResource {

    private final TopicService topicService;
    private final ConsumerGroupService consumerGroupService;
    private final ClusterHealthService clusterHealthService;
    private final KafkaClientService kafkaClientService;

    @Inject
    public KafkaClientResource(TopicService topicService, ConsumerGroupService consumerGroupService,
                               ClusterHealthService clusterHealthService, KafkaClientService kafkaClientService) {
        this.topicService = topicService;
        this.consumerGroupService = consumerGroupService;
        this.clusterHealthService = clusterHealthService;
        this.kafkaClientService = kafkaClientService;
    }

    @GET
    @Path("/brokers")
    @Operation(summary = "List brokers with full metadata")
    public Response brokers() {
        try {
            Map<String, Object> info = clusterHealthService.describeCluster();
            return Response.ok(info).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/topics")
    @Operation(summary = "List all topics with partition and replication details")
    public Response topics() {
        try {
            var topicNames = topicService.listTopics();
            if (topicNames.isEmpty()) {
                return Response.ok(List.of()).build();
            }
            var descs = topicService.describeTopics(topicNames);
            var result = descs.values().stream()
                    .sorted(java.util.Comparator.comparing(t -> t.name()))
                    .map(desc -> {
                        var m = new java.util.LinkedHashMap<String, Object>();
                        m.put("name", desc.name());
                        m.put("internal", desc.isInternal());
                        m.put("partitions", desc.partitions().size());
                        int rf = desc.partitions().isEmpty()
                                ? 0
                                : desc.partitions().get(0).replicas().size();
                        m.put("replicationFactor", rf);
                        long underReplicated = desc.partitions().stream()
                                .filter(pi -> pi.isr().size() < pi.replicas().size())
                                .count();
                        m.put("underReplicated", underReplicated);
                        return m;
                    })
                    .toList();
            return Response.ok(result).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/topics/{name}")
    @Operation(summary = "Describe a topic in detail")
    public Response topicDetail(@PathParam("name") String name) {
        try {
            return Response.ok(topicService.describeTopicDetail(name)).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Topic not found: " + name))
                        .build();
            }
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups")
    @Operation(summary = "List consumer groups with state and member count")
    public Response groups() {
        try {
            return Response.ok(consumerGroupService.listConsumerGroups()).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/groups/{id}")
    @Operation(summary = "Describe a consumer group with offsets and lag")
    public Response groupDetail(@PathParam("id") String id) {
        try {
            return Response.ok(consumerGroupService.describeConsumerGroup(id)).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Group not found: " + id))
                        .build();
            }
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/consume/{topic}")
    @Operation(summary = "Fetch recent records from a topic")
    public Response consume(
            @Parameter(description = "Topic name") @PathParam("topic") String topic,
            @Parameter(description = "Offset reset: earliest or latest") @QueryParam("offset") @DefaultValue("latest")
                    String offset,
            @Parameter(description = "Maximum records to return") @QueryParam("limit") @DefaultValue("20") int limit) {
        try {
            int safeLimit = Math.min(Math.max(1, limit), 200);
            List<Map<String, Object>> records = kafkaClientService.fetchRecords(topic, offset, safeLimit);
            return Response.ok(records).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @POST
    @Path("/produce/{topic}")
    @Operation(summary = "Produce a record to a topic")
    public Response produce(
            @Parameter(description = "Topic name") @PathParam("topic") String topic, ProduceRequest request) {
        try {
            if (request == null || request.value() == null) {
                return Response.status(400)
                        .entity(ApiError.of(400, "Bad Request", "Request body with 'value' is required"))
                        .build();
            }
            Map<String, Object> meta = kafkaClientService.produceRecord(topic, request.key(), request.value());
            return Response.status(201).entity(meta).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    public record ProduceRequest(String key, String value) {}

    @POST
    @Path("/topics")
    @Operation(summary = "Create a new topic")
    public Response createTopic(CreateTopicRequest request) {
        try {
            if (request == null || request.name() == null || request.name().isBlank()) {
                return Response.status(400)
                        .entity(ApiError.of(400, "Bad Request", "'name' is required"))
                        .build();
            }
            int partitions = request.partitions() > 0 ? request.partitions() : 1;
            int rf = request.replicationFactor() > 0 ? request.replicationFactor() : 1;
            topicService.createTopic(request.name(), partitions, rf, request.configs());
            var detail = topicService.describeTopicDetail(request.name());
            return Response.status(201).entity(detail).build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @PATCH
    @Path("/topics/{name}")
    @Operation(summary = "Alter topic configuration entries")
    public Response alterTopic(
            @Parameter(description = "Topic name") @PathParam("name") String name, AlterTopicRequest request) {
        try {
            if (request == null
                    || request.configs() == null
                    || request.configs().isEmpty()) {
                return Response.status(400)
                        .entity(ApiError.of(400, "Bad Request", "'configs' map is required"))
                        .build();
            }
            topicService.alterTopicConfig(name, request.configs());
            var detail = topicService.describeTopicDetail(name);
            return Response.ok(detail).build();
        } catch (RuntimeException e) {
            if (e.getMessage() != null && e.getMessage().contains("not found")) {
                return Response.status(404)
                        .entity(ApiError.of(404, "Not Found", "Topic not found: " + name))
                        .build();
            }
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    @DELETE
    @Path("/topics/{name}")
    @Operation(summary = "Delete a topic")
    public Response deleteTopic(@Parameter(description = "Topic name") @PathParam("name") String name) {
        try {
            topicService.deleteTopic(name);
            return Response.noContent().build();
        } catch (Exception e) {
            return Response.serverError()
                    .entity(ApiError.of(500, "Kafka Error", e.getMessage()))
                    .build();
        }
    }

    public record CreateTopicRequest(
            String name, int partitions, int replicationFactor, java.util.Map<String, String> configs) {}

    public record AlterTopicRequest(java.util.Map<String, String> configs) {}
}
