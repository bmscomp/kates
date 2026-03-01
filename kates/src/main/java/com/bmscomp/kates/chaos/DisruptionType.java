package com.bmscomp.kates.chaos;

/**
 * Taxonomy of Kubernetes-native disruption types.
 * Backend-agnostic — both the direct K8s API and Litmus CRD providers
 * map these to their corresponding implementation.
 */
public enum DisruptionType {
    POD_KILL,
    POD_DELETE,
    NETWORK_PARTITION,
    NETWORK_LATENCY,
    CPU_STRESS,
    DISK_FILL,
    ROLLING_RESTART,
    LEADER_ELECTION,
    SCALE_DOWN,
    NODE_DRAIN
}
