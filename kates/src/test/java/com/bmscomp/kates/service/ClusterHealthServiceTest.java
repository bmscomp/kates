package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.time.Duration;
import java.util.Collection;
import java.util.List;
import java.util.Map;
import java.util.Set;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.ConsumerGroupListing;
import org.apache.kafka.clients.admin.DescribeClusterResult;
import org.apache.kafka.clients.admin.DescribeMetadataQuorumResult;
import org.apache.kafka.clients.admin.ListConsumerGroupsOptions;
import org.apache.kafka.clients.admin.ListConsumerGroupsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.clients.admin.QuorumInfo;
import org.apache.kafka.common.KafkaFuture;
import org.apache.kafka.common.Node;
import org.apache.kafka.common.errors.TimeoutException;
import org.apache.kafka.common.errors.UnsupportedVersionException;
import org.apache.kafka.common.internals.KafkaFutureImpl;
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
        clusterHealthService.evictHealthCache();
    }

    private void stubDescribeCluster() {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        Node controller = new Node(0, "broker-0", 9092);
        Collection<Node> nodes = java.util.List.of(
                new Node(0, "broker-0", 9092), new Node(1, "broker-1", 9092), new Node(2, "broker-2", 9092));
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
            when(failFuture.get(anyLong(), any()))
                    .thenThrow(new java.util.concurrent.ExecutionException(new RuntimeException("refused")));
        } catch (Exception ignored) {
        }
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
            when(failFuture.get(anyLong(), any()))
                    .thenThrow(new java.util.concurrent.ExecutionException(new RuntimeException("fail")));
        } catch (Exception ignored) {
        }
        when(result.clusterId()).thenReturn(failFuture);
        when(mockClient.describeCluster()).thenReturn(result);

        assertEquals(0, clusterHealthService.brokerCount());
    }

    /**
     * brokerCount() used to route through describeCluster(), which opens with a
     * 30-second get. Both callers — the advisor and the cost estimate — treat 0
     * as "no cluster information", so an absent broker made them wait half a
     * minute for an answer they already had. The IT suite failed with "Read
     * timed out" whenever /advisor lost that race.
     */
    @Test
    void brokerCountGivesUpOnceWhenTheClusterIsAbsent() throws Exception {
        DescribeClusterResult result = mock(DescribeClusterResult.class);
        @SuppressWarnings("unchecked")
        KafkaFuture<Collection<Node>> unanswered = mock(KafkaFuture.class);
        when(unanswered.get(anyLong(), any()))
                .thenThrow(new java.util.concurrent.TimeoutException("no broker listening"));
        when(result.nodes()).thenReturn(unanswered);
        when(mockClient.describeCluster()).thenReturn(result);

        assertEquals(0, clusterHealthService.brokerCount());
        // One describe, and the short deadline rather than describeCluster()'s.
        verify(mockClient, times(1)).describeCluster();
        verify(unanswered).get(eq((long) 5), eq(java.util.concurrent.TimeUnit.SECONDS));
    }

    @Test
    void brokerCountAnswersFromAWarmCacheWithoutTouchingTheBroker() {
        stubDescribeCluster();
        clusterHealthService.describeCluster();
        clearInvocations(mockClient);

        assertEquals(3, clusterHealthService.brokerCount());
        verify(mockClient, never()).describeCluster();
    }

    private Map<String, Object> healthCheckWithQuorum(KafkaFuture<QuorumInfo> quorum) {
        stubDescribeCluster();
        ListTopicsResult topics = mock(ListTopicsResult.class);
        when(topics.names()).thenReturn(KafkaFuture.completedFuture(Set.<String>of()));
        when(mockClient.listTopics()).thenReturn(topics);
        ListConsumerGroupsResult groups = mock(ListConsumerGroupsResult.class);
        when(groups.all()).thenReturn(KafkaFuture.<Collection<ConsumerGroupListing>>completedFuture(List.of()));
        when(mockClient.listConsumerGroups(any(ListConsumerGroupsOptions.class)))
                .thenReturn(groups);
        DescribeMetadataQuorumResult described = mock(DescribeMetadataQuorumResult.class);
        when(described.quorumInfo()).thenReturn(quorum);
        when(mockClient.describeMetadataQuorum()).thenReturn(described);
        return clusterHealthService.clusterHealthCheck().await().atMost(Duration.ofSeconds(5));
    }

    private static KafkaFuture<QuorumInfo> failedQuorum(Exception e) {
        KafkaFutureImpl<QuorumInfo> future = new KafkaFutureImpl<>();
        future.completeExceptionally(e);
        return future;
    }

    @Test
    void healthCheckReportsTheKraftQuorumLeader() {
        QuorumInfo quorum = mock(QuorumInfo.class);
        when(quorum.leaderId()).thenReturn(3);
        when(quorum.voters()).thenReturn(List.of());
        when(quorum.observers()).thenReturn(List.of());

        Map<String, Object> report = healthCheckWithQuorum(KafkaFuture.completedFuture(quorum));

        assertEquals(Map.of("leaderId", 3, "voters", 0, "observers", 0, "hasLeader", true), report.get("kraftQuorum"));
        assertEquals("HEALTHY", report.get("status"));
    }

    /** A ZooKeeper broker does not support DescribeQuorum: there is no quorum to report. */
    @Test
    void healthCheckLeavesTheQuorumOutWhenTheBrokerDoesNotSupportIt() {
        Map<String, Object> report = healthCheckWithQuorum(
                failedQuorum(new UnsupportedVersionException("The node does not support DESCRIBE_QUORUM")));

        assertFalse(report.containsKey("kraftQuorum"));
        assertEquals("HEALTHY", report.get("status"));
    }

    /**
     * On KRaft only the quorum leader answers DescribeQuorum, so a leaderless quorum fails the call
     * (NOT_LEADER_OR_FOLLOWER, retried until the admin client's timeout). It used to be dropped like
     * a ZooKeeper cluster's, and the check said HEALTHY.
     */
    @Test
    void healthCheckWarnsWhenTheQuorumCannotBeDescribed() {
        Map<String, Object> report = healthCheckWithQuorum(
                failedQuorum(new TimeoutException("Call(callName=describeMetadataQuorum) timed out")));

        assertEquals(
                Map.of("leaderId", -1, "hasLeader", false, "issue", "UNAVAILABLE: TimeoutException"),
                report.get("kraftQuorum"));
        assertEquals("WARNING", report.get("status"));
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
