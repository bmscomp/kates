# Kafka Internals: What Every Tester Must Understand

You cannot meaningfully test a system you do not understand. Running `kafka-producer-perf-test.sh` and looking at the throughput number is easy enough, but interpreting *why* throughput dropped, *why* latency spiked, or *why* that consumer group stopped making progress requires understanding the internal machinery of Apache Kafka. This chapter covers the internals that matter most for performance and chaos testing.

## The Anatomy of a Kafka Write

When a producer calls `send()`, the message does not go directly to disk on a single machine. It enters a pipeline that spans the producer's memory, the network, the leader broker's page cache, the operating system's I/O scheduler, the physical disk, and then the replication pipeline to follower brokers. Understanding this pipeline is essential because every stage is a potential bottleneck, and each stage behaves differently under failure conditions.

### Producer Batching

The producer does not send messages one at a time. Instead, it accumulates messages in an internal buffer organized by topic-partition. Two parameters control when a batch is sent:

- **`batch.size`** (default 16KB) — when the accumulated data for a partition reaches this size, the batch is sent immediately.
- **`linger.ms`** (default 0) — even if the batch has not reached `batch.size`, it will be sent after this many milliseconds have elapsed since the first message was added.

With `linger.ms=0` (the default), every `send()` call triggers a network request. This is simple and low-latency for individual messages, but it dramatically reduces throughput because each network round-trip carries very little data. Setting `linger.ms=5` means the producer waits up to 5 milliseconds to accumulate more messages into the same batch, reducing the number of network requests and increasing throughput at the cost of a few milliseconds of additional latency.

This tradeoff is fundamental to Kafka performance tuning: **latency and throughput are inversely related at the batching layer.** You will see this directly in LOAD test results — tests with `linger.ms=0` will show lower throughput and lower average latency than tests with `linger.ms=10`.

### Leader Selection and the Write Path

Every Kafka partition has a single leader replica and zero or more follower replicas. All produce requests go to the leader. The leader:

1. Validates the message format and size
2. Appends the message batch to the leader's log segment file
3. Assigns each message a monotonically increasing **offset** (the message's position in the partition log)
4. If `acks=all`, waits for all in-sync replicas to fetch and persist the batch
5. If `acks=1`, immediately acknowledges the write after step 3
6. If `acks=0`, does not acknowledge at all — the producer does not wait

Step 4 is where most "slow produce" issues originate during chaos experiments. When you kill a broker that hosts followers for many partitions, those followers stop fetching from the leader. The leader's `acks=all` requests now wait until the controller removes the dead follower from the ISR set, which takes `replica.lag.time.max.ms` (default 30 seconds). During this window, every produce request with `acks=all` is blocked.

This is why disruption tests that kill a broker often show a latency spike lasting exactly 30 seconds — it is not the recovery time, it is the ISR eviction timeout.

## In-Sync Replicas (ISR): The Heart of Kafka Durability

The ISR is the set of replicas that are "caught up" with the leader and are considered reliable for acknowledging writes. When a producer sends a message with `acks=all`, the leader waits for every member of the ISR to confirm that it has persisted the message before acknowledging the write to the producer.

### How a Replica Falls Out of the ISR

A replica is removed from the ISR when it has not fetched from the leader for longer than `replica.lag.time.max.ms`. The mechanism works like this:

1. Each follower continuously sends Fetch requests to the leader
2. The leader tracks the last fetch time for each follower
3. If a follower's last fetch time exceeds `replica.lag.time.max.ms`, the controller removes it from the ISR

Notice that this is a *time-based* check, not a *log-offset-based* check (that was the old behavior, removed in Kafka 0.9). This means a follower that is fetching but slowly — perhaps because its disk is busy — stays in the ISR as long as it keeps fetching within the timeout window. But a follower on a killed broker stops fetching entirely, and after `replica.lag.time.max.ms`, it is evicted.

### How a Replica Rejoins the ISR

Once a killed broker restarts and its follower replicas begin fetching again, they gradually catch up to the leader's log end offset. When a follower's log end offset reaches the leader's log end offset (i.e., the follower has replicated all messages), the controller adds it back to the ISR.

The time to rejoin depends on how much data was produced while the follower was down. If 1 GB of data was produced at 50 MB/s and the follower can fetch at 100 MB/s, catch-up takes approximately 10 seconds. This is why the recovery time in disruption reports varies with test throughput — higher throughput means more data to catch up on.

### The ISR, `min.insync.replicas`, and Availability Tradeoffs

The `min.insync.replicas` (MISR) configuration creates a floor for the ISR size. If the ISR drops below MISR, the leader rejects produce requests with `acks=all` by returning `NOT_ENOUGH_REPLICAS`. This prevents writes from being acknowledged by too few replicas, which would risk data loss if those replicas also failed.

The relationship between replication factor, MISR, and broker failures creates a critical availability equation:

| replication.factor | min.insync.replicas | Brokers that can fail | Behavior when limit exceeded |
|-------------------|--------------------|-----------------------|-----------------------------|
| 3 | 2 | 1 | Leader rejects `acks=all` writes |
| 3 | 1 | 2 | Writes succeed with single replica (lower durability) |
| 5 | 3 | 2 | Leader rejects writes when ISR < 3 |

This table is the foundation of chaos engineering with Kafka. When you run a disruption test that kills a broker, you are testing the boundary between the "normal" column and the "behavior when limit exceeded" column. The disruption report's SLA grade tells you whether your configuration survived the boundary correctly.

## Leader Election: What Happens When a Leader Dies

When the leader of a partition dies (because the broker hosting it was killed or crashed), Kafka must elect a new leader from the surviving ISR members. The election process differs between ZooKeeper-based and KRaft-based clusters:

### KRaft-Based Election (Modern Kafka)

In KRaft mode (which Strimzi uses by default since Kafka 3.3+), the controller quorum handles leader election:

1. The controller quorum detects the broker failure (heartbeat timeout)
2. The active controller selects a new leader from the ISR for each affected partition
3. The controller writes the new partition metadata to the `__cluster_metadata` topic
4. Brokers watching the metadata topic learn about the new leaders
5. Producers and consumers refresh their metadata (either proactively or on the next request that returns `NOT_LEADER_OR_FOLLOWER`)

The total election time is typically 1-5 seconds in a healthy KRaft cluster. This is faster than ZooKeeper-based election because there is no external service to coordinate with — the controller quorum is part of the Kafka cluster itself.

### Unclean Leader Election

If all ISR members for a partition die, the partition has no valid leader candidates. At this point, two things can happen:

- **`unclean.leader.election.enable=false`** (default) — the partition remains leaderless until an ISR member comes back. This preserves data consistency but makes the partition unavailable.
- **`unclean.leader.election.enable=true`** — Kafka promotes an out-of-sync replica to leader. This restores availability but may lose messages that were acknowledged but not yet replicated to the new leader.

This is the most dangerous tradeoff in Kafka. The disruption playbook "Split-Brain Simulation" is specifically designed to test this boundary — it creates a network partition that isolates ISR members from each other to see what happens to partition leadership.

## Consumer Groups: Rebalancing Under Pressure

When you run a performance test with multiple consumers, they form a consumer group. The group coordinator (a broker elected for each group) assigns partitions to consumers using a rebalancing protocol.

### The Rebalance Protocol

Rebalancing is triggered by three events:

1. **A new consumer joins the group** — after calling `subscribe()`
2. **A consumer leaves the group** — after calling `close()`, or when its heartbeat times out
3. **Topic metadata changes** — partitions are added or removed

The default "eager" rebalancing protocol is disruptive: it revokes all partitions from all consumers, then reassigns them. During this window (which can take 5-30 seconds depending on `max.poll.interval.ms`), no consumer in the group is processing messages. This is visible in lag tracking as a sudden spike that begins at the rebalance trigger and ends when partition assignment completes.

The "cooperative" rebalancing protocol (available since Kafka 2.4) is less disruptive: it only reassigns the partitions that need to move, while other consumers keep processing. If your consumers use cooperative rebalancing, you should see smaller lag spikes during disruption tests.

### Heartbeats and Session Timeouts

Each consumer in a group sends periodic heartbeats to the group coordinator. If the coordinator does not receive a heartbeat within `session.timeout.ms` (default 45 seconds), it considers the consumer dead and triggers a rebalance.

During a disruption test that kills the broker hosting the group coordinator, the consumer group loses its coordinator. Consumers detect this when their next heartbeat fails, and they search for the new coordinator by querying other brokers. The group does not rebalance during this coordinator failover — it only rebalances if the coordinator failover takes longer than `session.timeout.ms`.

This is why consumer lag during broker kills sometimes shows a brief "stall" (no progress for a few seconds) without a full lag spike (no rebalance). The stall is the coordinator failover; the consumers resume processing once they find the new coordinator.

## Log Segments and Disk I/O

Kafka stores messages in log segment files. Each partition's data is stored in a dedicated directory on the broker's disk, organized into segments of configurable size (default 1GB). Understanding disk I/O patterns helps explain the VOLUME test type's behavior and the disk-fill disruption type.

### Sequential writes

Kafka achieves high throughput because it writes sequentially to append-only log files. Sequential I/O is orders of magnitude faster than random I/O on both spinning disks and SSDs, because:

- Spinning disks: no seek time between writes (the head does not need to move)
- SSDs: sequential writes avoid write amplification and trigger fewer garbage collection cycles

This is why Kafka can sustain hundreds of MB/s of write throughput on commodity hardware. It is also why the VOLUME test (which writes large and small messages on separate topics) reveals disk I/O differences — large messages fill log segments faster, triggering more frequent segment rolls, which are briefly non-sequential.

### Page Cache and Zero-Copy

Kafka relies heavily on the operating system's page cache for read performance. When a consumer reads recent messages, the data is almost always in the page cache (because the producer just wrote it). The kernel serves the read without hitting disk, using the `sendfile()` system call (zero-copy) to transfer data directly from the page cache to the network socket, bypassing the JVM heap entirely.

This explains a common performance test observation: consumer throughput is higher than producer throughput when the consumer is reading recent data. The consumer is reading from RAM, not disk. But if the consumer falls behind (large lag), it starts reading from disk, and throughput drops dramatically. This is the "falling off the page cache cliff" — a critical threshold that the ENDURANCE test is designed to detect.
