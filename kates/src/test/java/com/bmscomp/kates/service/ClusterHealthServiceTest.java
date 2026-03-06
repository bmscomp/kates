package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.Collection;
import java.util.Map;

import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

@QuarkusTest
class ClusterHealthServiceTest {

    @InjectMock
    KafkaAdminService kafkaAdminService;

    @Inject
    ClusterHealthService clusterHealthService;

    AdminClient mockClient;

    @BeforeEach
    void setUp() {
        mockClient = mock(AdminClient.class);
        when(kafkaAdminService.getClient()).thenReturn(mockClient);
        clusterHealthService.evictCache();
    }

    private void stubDescribeCluster() {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        Node controller = new Node(0, "broker-0", 9092);
        Collection<Node> nodes = java.util.List.of(
                new Node(0, "broker-0", 9092),
                new Node(1, "broker-1", 9092),
                new Node(2, "broker-2", 9092));
        when(result.clusterId()).thenReturn(KafkaFuture.completedFuture("test-cluster-id"));
        when(result.controller()).thenReturn(KafkaFuture.completedFuture(controller));
        when(result.nodes()).thenReturn(KafkaFuture.completedFuture(nodes));
        when(mockClient.describeCluster()).thenReturn(result);
    }

    @Test
    void describeClusterReturnsCorrectInfo() {
        stubDescribeCluster();

        Map<String, Object> info = clusterHealthService.describeCluster();
        assertEquals("test-cluster-id", info.get("clusterId"));
        assertEquals(3, info.get("brokerCount"));
    }

    @Test
    void describeClusterCachesResult() {
        stubDescribeCluster();

        clusterHealthService.describeCluster();
        clusterHealthService.describeCluster();

        verify(mockClient, times(1)).describeCluster();
    }

    @Test
    void isReachableReturnsTrueOnSuccess() {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        when(result.clusterId()).thenReturn(KafkaFuture.completedFuture("test-id"));
        when(mockClient.describeCluster()).thenReturn(result);

        assertTrue(clusterHealthService.isReachable());
    }

    @Test
    void isReachableReturnsFalseOnFailure() {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        @SuppressWarnings("unchecked")
        KafkaFuture<String> failFuture = mock(KafkaFuture.class);
        try {
            when(failFuture.get(anyLong(), any())).thenThrow(new java.util.concurrent.ExecutionException(new RuntimeException("refused")));
        } catch (Exception ignored) {}
        when(result.clusterId()).thenReturn(failFuture);
        when(mockClient.describeCluster()).thenReturn(result);

        assertFalse(clusterHealthService.isReachable());
    }

    @Test
    void brokerCountReturnsBrokerCount() {
        stubDescribeCluster();

        assertEquals(3, clusterHealthService.brokerCount());
    }

    @Test
    void brokerCountReturnsZeroOnError() {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        @SuppressWarnings("unchecked")
        KafkaFuture<String> failFuture = mock(KafkaFuture.class);
        try {
            when(failFuture.get(anyLong(), any())).thenThrow(new java.util.concurrent.ExecutionException(new RuntimeException("fail")));
        } catch (Exception ignored) {}
        when(result.clusterId()).thenReturn(failFuture);
        when(mockClient.describeCluster()).thenReturn(result);

        assertEquals(0, clusterHealthService.brokerCount());
    }

    @Test
    void evictCacheForcesRefresh() {
        stubDescribeCluster();

        clusterHealthService.describeCluster();
        clusterHealthService.evictCache();
        clusterHealthService.describeCluster();

        verify(mockClient, times(2)).describeCluster();
    }
}
