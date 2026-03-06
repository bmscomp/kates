package com.bmscomp.kates.service;

import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.ConsumerGroupDescription;
import org.apache.kafka.clients.admin.GroupListing;
import org.apache.kafka.clients.admin.ListGroupsOptions;
import org.apache.kafka.clients.admin.ListOffsetsResult;
import org.apache.kafka.clients.admin.OffsetSpec;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.TopicPartition;

@ApplicationScoped
public class ConsumerGroupService {

    private static final int TIMEOUT_SECONDS = 30;

    private final KafkaAdminService adminService;

    @Inject
    public ConsumerGroupService(KafkaAdminService adminService) {
        this.adminService = adminService;
    }

    public List<Map<String, Object>> listConsumerGroups() {
        AdminClient client = adminService.getClient();
        try {
            Collection<GroupListing> groups = client.listGroups(ListGroupsOptions.forConsumerGroups())
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<Map<String, Object>> result = new ArrayList<>();
            for (GroupListing listing : groups) {
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("groupId", listing.groupId());
                item.put("state", listing.groupState().isPresent()
                        ? listing.groupState().get().toString() : "UNKNOWN");
                result.add(item);
            }
            return result;
        } catch (Exception e) {
            throw new RuntimeException("Failed to list consumer groups", e);
        }
    }

    public Map<String, Object> describeConsumerGroup(String groupId) {
        AdminClient client = adminService.getClient();
        try {
            ConsumerGroupDescription desc = client.describeConsumerGroups(Collections.singleton(groupId))
                    .all()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
                    .get(groupId);

            if (desc == null) {
                throw new RuntimeException("Consumer group not found: " + groupId);
            }

            Map<String, Object> result = new LinkedHashMap<>();
            result.put("groupId", desc.groupId());
            result.put("state", desc.groupState().toString());
            result.put("members", desc.members().size());

            Map<TopicPartition, OffsetAndMetadata> offsets = client.listConsumerGroupOffsets(groupId)
                    .partitionsToOffsetAndMetadata()
                    .get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            Map<TopicPartition, OffsetSpec> latestRequest = new HashMap<>();
            offsets.keySet().forEach(tp -> latestRequest.put(tp, OffsetSpec.latest()));

            Map<TopicPartition, ListOffsetsResult.ListOffsetsResultInfo> endOffsets = latestRequest.isEmpty()
                    ? Map.of()
                    : client.listOffsets(latestRequest).all().get(TIMEOUT_SECONDS, TimeUnit.SECONDS);

            List<Map<String, Object>> offsetList = new ArrayList<>();
            long totalLag = 0;

            for (var entry : offsets.entrySet()) {
                TopicPartition tp = entry.getKey();
                long current = entry.getValue().offset();
                long end = endOffsets.containsKey(tp) ? endOffsets.get(tp).offset() : current;
                long lag = Math.max(0, end - current);
                totalLag += lag;

                Map<String, Object> item = new LinkedHashMap<>();
                item.put("topic", tp.topic());
                item.put("partition", tp.partition());
                item.put("currentOffset", current);
                item.put("endOffset", end);
                item.put("lag", lag);
                offsetList.add(item);
            }

            result.put("offsets", offsetList);
            result.put("totalLag", totalLag);
            return result;

        } catch (Exception e) {
            throw new RuntimeException("Failed to describe consumer group: " + groupId, e);
        }
    }
}
