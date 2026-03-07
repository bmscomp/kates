package com.bmscomp.kates.grpc;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;

import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;

import com.bmscomp.kates.service.ClusterHealthService;
import com.bmscomp.kates.service.ConsumerGroupService;
import com.bmscomp.kates.service.TopicService;

import com.bmscomp.kates.grpc.proto.*;

/**
 * gRPC implementation of the ClusterService — introspects the Kafka cluster
 * via the same service layer as the REST API.
 */
@GrpcService
@Blocking
public class GrpcClusterService extends MutinyClusterServiceGrpc.ClusterServiceImplBase {

    @Inject
    TopicService topicService;

    @Inject
    ConsumerGroupService consumerGroupService;

    @Inject
    ClusterHealthService clusterHealthService;

    @Override
    public Uni<ClusterInfo> getClusterInfo(com.google.protobuf.Empty request) {
        return Uni.createFrom().item(() -> {
            Map<String, Object> info = clusterHealthService.describeCluster();
            var builder = ClusterInfo.newBuilder();

            if (info.get("clusterId") != null) {
                builder.setClusterId(info.get("clusterId").toString());
            }
            if (info.get("controllerId") instanceof Number n) {
                builder.setControllerId(n.intValue());
            }
            if (info.get("brokers") instanceof List<?> brokers) {
                for (Object b : brokers) {
                    if (b instanceof Map<?, ?> bm) {
                        var bb = BrokerInfo.newBuilder();
                        if (bm.get("id") instanceof Number n) bb.setId(n.intValue());
                        if (bm.get("host") != null) bb.setHost(bm.get("host").toString());
                        if (bm.get("port") instanceof Number n) bb.setPort(n.intValue());
                        if (bm.get("rack") != null) bb.setRack(bm.get("rack").toString());
                        builder.addBrokers(bb.build());
                    }
                }
            }
            return builder.build();
        });
    }

    @Override
    public Uni<ListTopicsResponse> listTopics(ListTopicsRequest request) {
        return Uni.createFrom().item(() -> {
            int page = Math.max(0, request.getPage());
            int size = Math.max(1, Math.min(request.getSize() > 0 ? request.getSize() : 50, 200));

            Set<String> allTopics = topicService.listTopics();
            List<String> sorted = new ArrayList<>(allTopics);
            sorted.sort(String::compareTo);

            int total = sorted.size();
            int from = Math.min(page * size, total);
            int to = Math.min(from + size, total);
            List<String> pageItems = sorted.subList(from, to);

            var builder = ListTopicsResponse.newBuilder()
                    .setPage(page)
                    .setSize(size)
                    .setTotal(total);

            for (String name : pageItems) {
                builder.addItems(TopicInfo.newBuilder().setName(name).build());
            }
            return builder.build();
        });
    }

    @Override
    public Uni<TopicDetail> getTopicDetail(GetTopicRequest request) {
        return Uni.createFrom().item(() -> {
            Map<String, Object> detail = topicService.describeTopicDetail(request.getName());
            var builder = TopicDetail.newBuilder()
                    .setName(request.getName());

            if (detail.get("partitions") instanceof Number n) builder.setPartitions(n.intValue());
            if (detail.get("replicationFactor") instanceof Number n) builder.setReplicationFactor(n.intValue());
            if (detail.get("internal") instanceof Boolean b) builder.setInternal(b);
            if (detail.get("configs") instanceof Map<?, ?> configs) {
                configs.forEach((k, v) -> {
                    if (k != null && v != null) builder.putConfigs(k.toString(), v.toString());
                });
            }
            return builder.build();
        });
    }

    @Override
    public Uni<ListGroupsResponse> listConsumerGroups(ListGroupsRequest request) {
        return Uni.createFrom().item(() -> {
            int page = Math.max(0, request.getPage());
            int size = Math.max(1, Math.min(request.getSize() > 0 ? request.getSize() : 50, 200));

            List<Map<String, Object>> all = consumerGroupService.listConsumerGroups();
            int total = all.size();
            int from = Math.min(page * size, total);
            int to = Math.min(from + size, total);
            List<Map<String, Object>> pageItems = all.subList(from, to);

            var builder = ListGroupsResponse.newBuilder()
                    .setPage(page)
                    .setSize(size)
                    .setTotal(total);

            for (Map<String, Object> g : pageItems) {
                var gb = ConsumerGroupInfo.newBuilder();
                if (g.get("groupId") != null) gb.setGroupId(g.get("groupId").toString());
                if (g.get("state") != null) gb.setState(g.get("state").toString());
                if (g.get("members") instanceof Number n) gb.setMembers(n.intValue());
                builder.addItems(gb.build());
            }
            return builder.build();
        });
    }
}
