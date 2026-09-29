package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.Collection;
import java.util.Collections;
import java.util.Map;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.ConsumerGroupDescription;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeConsumerGroupsResult;
import org.apache.kafka.clients.admin.ListConsumerGroupOffsetsResult;
import org.apache.kafka.clients.admin.ListConsumerGroupsResult;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.ConsumerGroupState;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.TopicPartition;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

@QuarkusTest
class ConsumerGroupServiceTest {

    @InjectMock
    KafkaAdminService kafkaAdminService;

    @Inject
    ConsumerGroupService consumerGroupService;

    AdminClient mockClient;

    @BeforeEach
    void setUp() {
        mockClient = mock(AdminClient.class);
        when(kafkaAdminService.getClient()).thenReturn(mockClient);
    }

    @Test
    void listConsumerGroupsReturnsGroupsWithState() {
        ListConsumerGroupsResult listResult = mock(ListConsumerGroupsResult.class);
        ConsumerGroupListing listing = mock(ConsumerGroupListing.class);
        when(listing.groupId()).thenReturn("test-group");
        when(listing.state()).thenReturn(java.util.Optional.of(ConsumerGroupState.STABLE));
        when(listResult.all()).thenReturn(KafkaFuture.completedFuture(Collections.singletonList(listing)));
        when(mockClient.listConsumerGroups(any(org.apache.kafka.clients.admin.ListConsumerGroupsOptions.class)))
                .thenReturn(listResult);

        ConsumerGroupDescription desc = mock(ConsumerGroupDescription.class);
        when(desc.groupId()).thenReturn("test-group");
        when(desc.state()).thenReturn(ConsumerGroupState.STABLE);
        when(desc.members()).thenReturn(Collections.emptyList());

        DescribeConsumerGroupsResult descResult = mock(DescribeConsumerGroupsResult.class);
        when(descResult.all()).thenReturn(KafkaFuture.completedFuture(Map.of("test-group", desc)));
        when(mockClient.describeConsumerGroups(any(Collection.class))).thenReturn(descResult);

        var groups = consumerGroupService.listConsumerGroups();
        assertEquals(1, groups.size());
        assertEquals("test-group", groups.get(0).get("groupId"));
        assertEquals("Stable", groups.get(0).get("state"));
    }

    @Test
    void describeConsumerGroupReturnsDetailWithLag() {
        ConsumerGroupDescription desc = mock(ConsumerGroupDescription.class);
        when(desc.groupId()).thenReturn("cg-1");
        when(desc.state()).thenReturn(ConsumerGroupState.STABLE);
        when(desc.members()).thenReturn(Collections.emptyList());

        DescribeConsumerGroupsResult descResult = mock(DescribeConsumerGroupsResult.class);
        when(descResult.all()).thenReturn(KafkaFuture.completedFuture(Map.of("cg-1", desc)));
        when(mockClient.describeConsumerGroups(any(Collection.class))).thenReturn(descResult);

        TopicPartition tp = new TopicPartition("topic-1", 0);
        ListConsumerGroupOffsetsResult offsetsResult = mock(ListConsumerGroupOffsetsResult.class);
        when(offsetsResult.partitionsToOffsetAndMetadata())
                .thenReturn(KafkaFuture.completedFuture(Map.of(tp, new OffsetAndMetadata(50))));
        when(mockClient.listConsumerGroupOffsets(eq("cg-1"))).thenReturn(offsetsResult);

        org.apache.kafka.clients.admin.ListOffsetsResult listOffsetsResult =
                mock(org.apache.kafka.clients.admin.ListOffsetsResult.class);
        org.apache.kafka.clients.admin.ListOffsetsResult.ListOffsetsResultInfo info =
                mock(org.apache.kafka.clients.admin.ListOffsetsResult.ListOffsetsResultInfo.class);
        when(info.offset()).thenReturn(100L);
        when(listOffsetsResult.all()).thenReturn(KafkaFuture.completedFuture(Map.of(tp, info)));
        when(mockClient.listOffsets(any())).thenReturn(listOffsetsResult);

        Map<String, Object> detail = consumerGroupService.describeConsumerGroup("cg-1");
        assertEquals("cg-1", detail.get("groupId"));
        assertEquals(50L, detail.get("totalLag"));
    }
}
