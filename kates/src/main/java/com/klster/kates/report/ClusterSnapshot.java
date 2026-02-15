package com.klster.kates.report;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;

/**
 * Snapshot of cluster topology captured at test execution time.
 * Records broker membership and partition leadership for correlation with test metrics.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record ClusterSnapshot(
        String clusterId,
        int brokerCount,
        int controllerId,
        List<BrokerInfo> brokers,
        List<PartitionAssignment> leaders
) {
    public record BrokerInfo(int id, String host, int port, String rack) {}

    public record PartitionAssignment(
            String topic,
            int partition,
            int leaderId,
            List<Integer> replicas,
            List<Integer> isr
    ) {}

    public int leaderCountForBroker(int brokerId) {
        if (leaders == null) return 0;
        return (int) leaders.stream().filter(l -> l.leaderId() == brokerId).count();
    }

    public int replicaCountForBroker(int brokerId) {
        if (leaders == null) return 0;
        return (int) leaders.stream()
                .filter(l -> l.replicas() != null && l.replicas().contains(brokerId))
                .count();
    }

    public int isrCountForBroker(int brokerId) {
        if (leaders == null) return 0;
        return (int) leaders.stream()
                .filter(l -> l.isr() != null && l.isr().contains(brokerId))
                .count();
    }

    public int underReplicatedCountForBroker(int brokerId) {
        if (leaders == null) return 0;
        return (int) leaders.stream()
                .filter(l -> l.replicas() != null && l.replicas().contains(brokerId))
                .filter(l -> l.isr() != null && l.isr().size() < l.replicas().size())
                .count();
    }
}
