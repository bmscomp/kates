# Chapter 6: Chaos Engineering Theory

Chaos engineering is the discipline of experimenting on a distributed system to build confidence in its ability to withstand turbulent conditions in production. This chapter covers the theory — Chapter 7 covers how Kates implements it.

## Why Chaos Engineering?

Distributed systems fail in ways that are impossible to predict from reading code alone. A Kafka cluster might handle a single broker failure gracefully in theory, but in practice:

- The leader election might take 30 seconds instead of 3
- Consumer groups might rebalance in a thundering herd
- The surviving brokers might hit memory pressure from absorbing extra partitions
- Network timeouts might cascade into producer retries that amplify the problem

Chaos engineering replaces **hope** with **evidence**.

```mermaid
graph TD
    subgraph Without Chaos
        A[Deploy to production] --> B[Wait for incident]
        B --> C[Scramble to fix]
        C --> D[Post-mortem]
        D --> E[Hope it doesn't happen again]
    end
    
    subgraph With Chaos
        F[Deploy to staging] --> G[Inject controlled failure]
        G --> H[Observe behavior]
        H --> I[Fix weaknesses]
        I --> J[Build confidence]
        J --> K[Deploy to production]
    end
```

## Core Principles

### 1. Build a Hypothesis Around Steady State

Before injecting chaos, you must define what "normal" looks like. For Kafka, steady state includes:

- All partitions have leaders
- ISR count equals replication factor
- Producer throughput meets the target rate
- Consumer lag is bounded
- P99 latency is within SLA

### 2. Vary Real-World Events

Inject faults that actually happen in production:

```mermaid
graph TB
    subgraph Infrastructure
        IF1[Pod/VM crash]
        IF2[Disk failure]
        IF3[CPU exhaustion]
        IF4[Memory pressure]
    end
    
    subgraph Network
        NF1[Partition]
        NF2[Latency injection]
        NF3[Packet loss]
        NF4[DNS failure]
    end
    
    subgraph Application
        AF1[Process kill]
        AF2[Config corruption]
        AF3[Resource exhaustion]
        AF4[Clock skew]
    end
    
    subgraph Kafka-Specific
        KF1[Broker crash]
        KF2[Leader election]
        KF3[ISR shrink]
        KF4[Log corruption]
        KF5[Rebalance storm]
    end
```

### 3. Run Experiments in Production (or Production-Like)

Chaos experiments in a toy environment prove nothing. The Kind cluster in this project is configured to mirror production topology:

| Production Property | Kind Equivalent |
|---|---|
| Multi-AZ deployment | 3 nodes with zone labels |
| Rack-aware replication | Strimzi rack configuration |
| Resource constraints | Memory limits on brokers |
| Persistent storage | PVCs with zone-specific StorageClasses |
| Monitoring | Same Prometheus/Grafana stack |

### 4. Automate Experiments to Run Continuously

One-off chaos tests are useful; scheduled, repeating chaos tests are powerful. Kates supports cron-based scheduling:

```bash
# Run an integrity chaos test every night at 2 AM
kates schedule create --type INTEGRITY --records 100000 --cron "0 2 * * *"
```

### 5. Minimize Blast Radius

Start small and expand:

```mermaid
graph LR
    L1["Level 1<br/>Kill 1 pod<br/>Known recovery"] --> L2["Level 2<br/>Network partition<br/>1 broker isolated"] --> L3["Level 3<br/>Kill 2 pods<br/>Near quorum loss"] --> L4["Level 4<br/>Full AZ failure<br/>Node drain"]
```

## The Game Day Methodology

A **Game Day** is a structured chaos engineering session. Here's the process:

```mermaid
graph TD
    subgraph Preparation
        P1[Define hypothesis]
        P2[Set SLA thresholds]
        P3[Prepare rollback plan]
        P4[Alert the team]
    end
    
    subgraph Execution
        E1[Establish baseline]
        E2[Inject failure]
        E3[Observe impact]
        E4[Allow recovery]
    end
    
    subgraph Analysis
        A1[Compare baseline vs. impact]
        A2[Measure recovery time]
        A3[Check for data loss]
        A4[Grade against SLA]
    end
    
    subgraph Follow-Up
        F1[Document findings]
        F2[File improvement tickets]
        F3[Schedule retest]
    end
    
    P1 --> P2 --> P3 --> P4
    P4 --> E1 --> E2 --> E3 --> E4
    E4 --> A1 --> A2 --> A3 --> A4
    A4 --> F1 --> F2 --> F3
```

### Example Hypothesis

> **Hypothesis:** "When we kill the leader broker for our main topic, producer latency will spike to no more than 500ms during leader election (which should complete within 10 seconds), and zero messages will be lost."

This hypothesis is testable, measurable, and has clear pass/fail criteria.

## Kafka-Specific Failure Modes

Kafka has unique failure characteristics that general-purpose chaos tools don't understand:

### Leader Election

When a partition's leader broker dies, Kafka must elect a new leader from the ISR:

```mermaid
sequenceDiagram
    participant P as Producer
    participant L as Leader (dies)
    participant F1 as Follower 1
    participant F2 as Follower 2
    participant Ctrl as Controller
    
    Note over L: Broker crashes
    P->>L: Write (fails)
    P->>P: Buffer + retry
    Ctrl->>Ctrl: Detect leader loss
    Ctrl->>F1: You are the new leader
    F1->>F1: Accept leadership
    P->>F1: Retry write (succeeds)
    
    Note over P,F2: Gap = detection time + election time
```

Key timing:

| Phase | Typical Duration | Depends On |
|-------|:---:|---|
| Failure detection | 5–15s | `session.timeout.ms`, health check interval |
| Leader election | \< 1s | Number of partitions, controller load |
| Client reconnection | 1–5s | `metadata.max.age.ms`, retry backoff |
| **Total unavailability** | **6–20s** | Sum of all phases |

### ISR Shrink and Expand

When a follower falls behind (or a broker recovers), the ISR changes:

```mermaid
stateDiagram-v2
    [*] --> Healthy: RF=3, ISR=3
    Healthy --> Degraded: Broker fails<br/>ISR=2
    Degraded --> Healthy: Broker recovers<br/>Catches up
    Degraded --> Critical: Another broker fails<br/>ISR=1
    Critical --> WriteUnavailable: ISR < min.insync.replicas
    Critical --> Degraded: Broker recovers
    WriteUnavailable --> Degraded: Broker recovers<br/>ISR≥2
```

### Consumer Group Rebalance

When a consumer dies or a new one joins, Kafka rebalances partition assignments:

```mermaid
sequenceDiagram
    participant C1 as Consumer 1
    participant C2 as Consumer 2
    participant Coord as Group Coordinator
    participant C3 as Consumer 3 (new)
    
    Note over C1,C2: Steady state: C1=[P0,P1], C2=[P2]
    C3->>Coord: JoinGroup
    Coord->>C1: Rebalance triggered
    Coord->>C2: Rebalance triggered
    Note over C1,C2: All consumers stop processing
    C1->>Coord: JoinGroup (re-negotiate)
    C2->>Coord: JoinGroup (re-negotiate)
    C3->>Coord: JoinGroup
    Coord->>C1: New assignment: [P0]
    Coord->>C2: New assignment: [P1]
    Coord->>C3: New assignment: [P2]
    Note over C1,C3: Processing resumes
```

During rebalancing, **all consumers in the group stop processing**. This "stop-the-world" pause can last seconds to minutes depending on group size and partition count.

## Key Metrics During Chaos

| Metric | What to Watch |
|--------|---------------|
| **Under-replicated partitions** | Should spike briefly, then return to 0 |
| **Offline partitions** | Should be 0 (if RF > failed brokers) |
| **Active controller changes** | Should happen exactly once per controller failure |
| **Consumer lag** | Should spike during failure, then drain |
| **Producer error rate** | Should spike briefly, producers should retry successfully |
| **Leader election rate** | Should equal the number of partitions on the failed broker |
| **Recovery time** | Time from failure to all ISRs fully expanded |

These metrics form the foundation of SLA grading in Kates disruption tests.
