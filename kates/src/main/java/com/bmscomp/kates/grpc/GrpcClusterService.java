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
import com.bmscomp.kates.service.ClusterTopologyService;
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

    @Inject
    ClusterTopologyService clusterTopologyService;

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

    @Override
    @SuppressWarnings("unchecked")
    public Uni<ClusterTopology> getClusterTopology(com.google.protobuf.Empty request) {
        return Uni.createFrom().item(() -> {
            Map<String, Object> topo = clusterTopologyService.describeTopology();
            var builder = ClusterTopology.newBuilder();

            if (topo.get("clusterName") != null) builder.setClusterName(topo.get("clusterName").toString());
            if (topo.get("kafkaVersion") != null) builder.setKafkaVersion(topo.get("kafkaVersion").toString());
            if (topo.get("kraftMode") instanceof Boolean b) builder.setKraftMode(b);
            if (topo.get("controllerQuorumLeader") instanceof Number n) builder.setControllerQuorumLeader(n.intValue());

            if (topo.get("nodePools") instanceof List<?> pools) {
                for (Object p : pools) {
                    if (p instanceof Map<?, ?> pm) {
                        var pb = NodePoolInfo.newBuilder();
                        if (pm.get("name") != null) pb.setName(pm.get("name").toString());
                        if (pm.get("role") != null) pb.setRole(pm.get("role").toString());
                        if (pm.get("replicas") instanceof Number n) pb.setReplicas(n.intValue());
                        if (pm.get("storageType") != null) pb.setStorageType(pm.get("storageType").toString());
                        if (pm.get("storageSize") != null) pb.setStorageSize(pm.get("storageSize").toString());
                        builder.addNodePools(pb.build());
                    }
                }
            }

            if (topo.get("nodes") instanceof List<?> nodes) {
                for (Object nd : nodes) {
                    if (nd instanceof Map<?, ?> nm) {
                        var nb = NodeInfo.newBuilder();
                        if (nm.get("id") instanceof Number n) nb.setId(n.intValue());
                        if (nm.get("host") != null) nb.setHost(nm.get("host").toString());
                        if (nm.get("port") instanceof Number n) nb.setPort(n.intValue());
                        if (nm.get("rack") != null) nb.setRack(nm.get("rack").toString());
                        if (nm.get("role") != null) nb.setRole(nm.get("role").toString());
                        if (nm.get("pool") != null) nb.setPool(nm.get("pool").toString());
                        if (nm.get("status") != null) nb.setStatus(nm.get("status").toString());
                        if (nm.get("isQuorumLeader") instanceof Boolean b) nb.setIsQuorumLeader(b);
                        builder.addNodes(nb.build());
                    }
                }
            }

            return builder.build();
        });
    }
}
