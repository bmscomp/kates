package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.Map;
import java.util.Set;

import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.CreateTopicsResult;
import org.apache.kafka.clients.admin.DeleteTopicsResult;
import org.apache.kafka.clients.admin.ListTopicsResult;
import org.apache.kafka.common.KafkaFuture;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

@QuarkusTest
class TopicServiceTest {

    @InjectMock
    KafkaAdminService kafkaAdminService;

    @Inject
    TopicService topicService;

    AdminClient mockClient;

    @BeforeEach
    void setUp() {
        mockClient = mock(AdminClient.class);
        when(kafkaAdminService.getClient()).thenReturn(mockClient);
        topicService.evictCache();
    }

    @Test
    void createTopicSucceeds() {
        CreateTopicsResult result = mock(CreateTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.createTopics(any())).thenReturn(result);

        assertDoesNotThrow(() ->
                topicService.createTopic("test-topic", 3, 1, Map.of("retention.ms", "60000")));
    }

    @Test
    void createTopicWithNullConfigsSucceeds() {
        CreateTopicsResult result = mock(CreateTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.createTopics(any())).thenReturn(result);

        assertDoesNotThrow(() ->
                topicService.createTopic("test-topic", 1, 1, null));
    }

    @Test
    void deleteTopicSucceeds() {
        DeleteTopicsResult result = mock(DeleteTopicsResult.class);
        when(result.all()).thenReturn(KafkaFuture.completedFuture(null));
        when(mockClient.deleteTopics(any(java.util.Collection.class))).thenReturn(result);

        assertDoesNotThrow(() -> topicService.deleteTopic("test-topic"));
    }

    @Test
    void listTopicsReturnsFromClient() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a", "topic-b")));
        when(mockClient.listTopics()).thenReturn(result);

        Set<String> topics = topicService.listTopics();
        assertEquals(2, topics.size());
        assertTrue(topics.contains("topic-a"));
        assertTrue(topics.contains("topic-b"));
    }

    @Test
    void listTopicsCacheWorks() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a")));
        when(mockClient.listTopics()).thenReturn(result);

        topicService.listTopics();
        topicService.listTopics();

        verify(mockClient, times(1)).listTopics();
    }

    @Test
    void evictCacheForcesRefresh() {
        ListTopicsResult result = mock(ListTopicsResult.class);
        when(result.names()).thenReturn(KafkaFuture.completedFuture(Set.of("topic-a")));
        when(mockClient.listTopics()).thenReturn(result);

        topicService.listTopics();
        topicService.evictCache();
        topicService.listTopics();

        verify(mockClient, times(2)).listTopics();
    }
}
