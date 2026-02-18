# Performance Engineering: The Science Behind the Numbers

Performance testing is not about running a benchmark and looking at the throughput number. It is about understanding the relationship between load, latency, throughput, and resource utilization — and using that understanding to predict how your system will behave under conditions you have not yet tested. This chapter covers the theoretical foundations that make the difference between "we ran a test" and "we understand our system."

## Throughput vs. Latency: The Fundamental Tradeoff

The single most important concept in performance engineering is that throughput and latency are not independent. They are coupled through queueing theory, and understanding this coupling is essential for interpreting test results correctly.

### The Queueing Theory Perspective

Every Kafka broker is, fundamentally, a queueing system. Produce requests arrive, wait in a request queue, are processed (written to disk and replicated), and are acknowledged. As the arrival rate approaches the service rate, the queue grows, and latency increases exponentially. This is the basic result of queueing theory, and it explains the characteristic "hockey stick" latency curve that you see in every STRESS test.

At low utilization (say, 20% of capacity), requests rarely queue — they are served almost immediately, and latency is dominated by the fixed processing time (network + disk I/O). At moderate utilization (50-70%), some queueing occurs, and latency starts increasing. At high utilization (80-95%), the queue grows rapidly, and latency increases sharply. Above 95% utilization, latency becomes unbounded — the system is in overload.

This is why the STRESS test exists: it ramps throughput from 10,000 to 200,000 messages/sec in five steps, specifically to find the inflection point where the curve goes from "gradual increase" to "hockey stick." That inflection point is your cluster's practical capacity limit — not the maximum throughput you can achieve in a short burst, but the sustainable throughput the cluster can handle without queueing-induced latency degradation.

### Little's Law

Little's Law states: **L = λ × W**, where L is the average number of items in a system, λ is the average arrival rate, and W is the average time an item spends in the system.

For Kafka, this translates to: **in-flight requests = throughput × latency**. If your producer is sending 50,000 messages/sec with an average latency of 5ms, there are approximately 250 messages in-flight at any time (in the network, in the broker's request queue, or being replicated).

This relationship has practical implications:

1. **Buffer sizing:** The producer's `buffer.memory` (default 32MB) must be large enough to hold all in-flight messages. At 50,000 msg/sec × 1KB × 5ms latency, that is about 250KB, well within the default. But at 200,000 msg/sec × 1KB × 50ms latency (during a stress test), that is 10MB — still within the default, but getting closer to the limit.

2. **`max.in.flight.requests.per.connection`:** This setting (default 5) limits the number of unacknowledged batches per connection. If each batch is 16KB and takes 5ms to acknowledge, the maximum throughput per connection is 5 × 16KB / 5ms = 16 MB/s. If you need more, increase `batch.size` or use more producer connections (which is what `numProducers > 1` does in Kates).

3. **Backpressure:** When the producer's internal buffer is full (because the broker is slow to acknowledge), the `send()` call blocks. This is Kafka's built-in backpressure mechanism. In test results, this manifests as the producer achieving lower throughput than configured — the producer is trying to send at 100,000 msg/sec, but the broker can only acknowledge at 80,000 msg/sec, so the producer's buffer fills and sends are throttled.

## Tail Latency: Why P99 Matters More Than Average

The average latency is easy to compute and easy to understand, but it is a poor metric for system health. Consider a system that processes 99% of requests in 2ms and 1% of requests in 500ms. The average latency is about 7ms — which tells you almost nothing about what either the fast requests or the slow requests experienced.

### Tail Latency Amplification

The problem with high tail latency is that it gets worse in distributed systems through a phenomenon called tail latency amplification. If a single producer call has a 1% chance of taking 500ms, and a consumer request fans out to 10 partitions (each served by a different broker), the probability that *at least one* partition's response takes 500ms is:

```
P(at least one slow) = 1 - (0.99)^10 ≈ 9.6%
```

So the single request's 1% tail becomes the aggregate request's 10% tail. With 100 partitions, the probability rises to 63%. This is why high-partition-count topics tend to have worse consumer P99 latency — it is pure probability, not a Kafka bug.

This is also why the ROUND_TRIP test measures the actual end-to-end latency (produce → consume) rather than just the produce latency. The produce P99 tells you about the broker's write performance. The round-trip P99 tells you about the user's actual experience.

### percentile Distributions vs. Histograms

Percentiles compress a complex distribution into a few numbers. This is useful for comparison (our P99 went from 15ms to 45ms after the code change), but it hides important details. Two workloads can have identical P50 and P99 while having very different distributions:

- **Normal distribution:** a smooth bell curve around the median, with the P99 about 2× the mean
- **Bimodal distribution:** two separate peaks (e.g., fast cache hits and slow cache misses), with the P99 capturing only the tail of the slow peak
- **Heavy-tailed distribution:** most requests are fast, but the slow requests are *very* slow (100× or 1000× the median)

The latency heatmap export in Kates exists specifically to reveal this detail. A percentile tells you "1% of requests were slower than X." A heatmap shows you exactly what happened second-by-second across the entire latency spectrum.

## Batching Theory: The Micro-Economics of Kafka Throughput

Kafka's throughput advantage comes from batching at every layer of the stack. Understanding why batching works — and where it breaks down — is essential for tuning performance tests.

### Why Batching Increases Throughput

Every network round-trip has a fixed overhead: TCP packet headers (40 bytes minimum), TLS handshake overhead, request serialization, broker-side request parsing, and operating system context switches. This overhead is approximately constant regardless of whether the request carries 1 message or 1,000 messages.

If you send 10,000 individual 1KB messages, the total overhead is 10,000 × (overhead per request). If you batch them into 10 batches of 1,000, the total overhead is 10 × (overhead per request) — a 1000× reduction in overhead. The data volume is the same, but the protocol overhead is three orders of magnitude lower.

This is why `linger.ms=0` (default) with small messages produces dramatically lower throughput than `linger.ms=5`. The difference is not the 5ms of latency — it is the reduction in round-trips. A test that shows 20,000 msg/sec with `linger.ms=0` might show 50,000 msg/sec with `linger.ms=5` and 70,000 msg/sec with `linger.ms=20`, because each batch carries more messages.

### The Optimal Batch Size

There is no universal "optimal" batch size because it depends on the message size, the network bandwidth, and the broker's disk throughput:

- **Small messages (< 256 bytes):** Large batches (64KB-128KB) are optimal because protocol overhead dominates. The batch might contain hundreds or thousands of messages.
- **Medium messages (1-10 KB):** The default 16KB batch works well. Each batch contains 2-16 messages.
- **Large messages (> 100 KB):** Batching provides diminishing returns because each message already fills a significant portion of the batch. The overhead-to-data ratio is already low.

The VOLUME test uses two workloads specifically to compare these regimes: a large-message workload (100KB messages, where batching matters less) and a high-count workload (1KB messages with aggressive batching, where `batch.size=256KB` and `linger.ms=50`). The throughput difference between these workloads reveals how much of your cluster's performance is "real" I/O capacity versus protocol overhead savings.

### Compression: A Form of Batching

Compression (`compression.type=lz4|snappy|zstd|gzip`) reduces the size of each batch after the producer has accumulated the messages. This effectively increases the batch capacity — a 16KB batch compressed to 8KB can hold twice as many messages before hitting network bandwidth limits.

The tradeoff is CPU time. Compression adds CPU overhead on both the producer (compression) and the consumer (decompression). For CPU-bound workloads, compression can actually reduce throughput. For network-bound workloads (which is the common case in cloud environments with limited bandwidth), compression significantly increases throughput.

LZ4 is typically the best choice for Kafka: it has the best speed-to-compression-ratio tradeoff, with very fast decompression. ZSTD provides better compression ratio but is slower. Snappy is similar to LZ4 but slightly less efficient. Gzip is the slowest and should only be used when maximum compression is required.

## Back-Pressure: What Happens When You Push Too Hard

Back-pressure is the mechanism by which a slow downstream component slows down a fast upstream component. In Kafka, back-pressure occurs at two levels:

### Producer-Side Back-Pressure

The producer's internal buffer (`buffer.memory`, default 32MB) accumulates unsent messages. When the buffer is full, the `send()` call blocks for up to `max.block.ms` (default 60 seconds). If the buffer does not drain within this timeout, the produce call throws a `TimeoutException`.

In test results, back-pressure manifests as throughput plateauing below the configured target. If you configure `throughput=100000` but the results show 65,000 msg/sec, the producer is being back-pressured by the broker's ability to acknowledge writes. This is not an error — it is the system telling you its actual capacity.

### Broker-Side Back-Pressure

Brokers have a request queue depth limit (`queued.max.requests`, default 500). When the queue is full, the broker stops accepting new connections. This is the last line of defense before a broker's heap is exhausted by buffered requests.

In extreme overload (like the 200,000 msg/sec step in a STRESS test against a small cluster), you might see `TIMEOUT` errors in the test results. These are not network timeouts — they are the broker rejecting requests because its queue is full. The `error` column in the CSV export will show these errors, and the overall error rate metric captures their frequency.

## The Saturation Curve: Your Cluster's DNA

The most valuable output of a STRESS test is the saturation curve — a plot of throughput vs. latency as load increases. This curve has four characteristic regions:

1. **Linear region (0-50% capacity):** Throughput increases linearly with load, latency is flat. The system has plenty of headroom.
2. **Sublinear region (50-80% capacity):** Throughput still increases but at a decreasing rate, latency begins to rise. Queue effects are becoming visible.
3. **Saturation point (~80-90% capacity):** Throughput peaks and latency rises sharply. This is the practical capacity limit.
4. **Overload region (>90% capacity):** Throughput may actually decrease (due to timeouts, retries, and resource contention), and latency becomes unbounded. The system is in distress.

The saturation point is the number you care about for capacity planning. If your production workload will be 50,000 msg/sec, and the saturation point is 100,000 msg/sec, you have 2× headroom — generally considered safe for most applications. If the saturation point is 60,000 msg/sec, you have only 1.2× headroom, which is dangerously thin for handling traffic spikes.

The STRESS test reveals this curve. Plot the `throughputRecPerSec` and `p99LatencyMs` columns from the CSV export against each step (10K, 25K, 50K, 100K, 200K msg/sec). The resulting chart is your cluster's performance DNA — it tells you everything you need for capacity planning decisions.
