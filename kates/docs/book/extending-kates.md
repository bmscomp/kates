# Extending Kates: Writing Custom Providers and Backends

Kates is designed to be extended. The core orchestration logic — how tests are planned, how disruptions are coordinated, how results are graded — is stable and rarely needs modification. But the integration points — how benchmarks are executed, how faults are injected, how results are exported — are defined through Service Provider Interfaces (SPIs) that you can implement without changing any existing code.

This chapter explains the three extension points, the contracts each SPI defines, and walks you through implementing a custom provider from scratch.

## The Three Extension Points

| SPI | Package | Purpose | Existing Implementations |
|-----|---------|---------|--------------------------|
| `BenchmarkBackend` | `com.klster.kates.engine` | Executes performance benchmarks | `NativeBenchmarkBackend`, `TrogdorBenchmarkBackend` |
| `ChaosProvider` | `com.klster.kates.chaos` | Injects and cleans up faults | `KubernetesChaosProvider`, `LitmusChaosProvider`, `HybridChaosProvider`, `NoOpChaosProvider` |
| `ExportStrategy` | `com.klster.kates.export` | Transforms results into output formats | `CsvExporter`, `JunitXmlExporter`, `HeatmapExporter` |

Each SPI is a Java interface with CDI qualifiers. Implementations are discovered automatically by Quarkus's CDI container at startup. Adding a new implementation is as simple as creating a class that implements the interface and annotating it with the appropriate CDI qualifier.

## Writing a Custom BenchmarkBackend

The `BenchmarkBackend` SPI defines how Kates executes performance benchmarks. The native backend runs Kafka clients in-process using virtual threads. The Trogdor backend delegates to an external Trogdor Coordinator. You might want to write a custom backend for:

- A different benchmarking tool (e.g., OpenMessaging Benchmark)
- A cloud-managed Kafka service that requires proprietary client configuration
- A simulation or mock backend for development environments without a real Kafka cluster

### The Interface

```java
public interface BenchmarkBackend {
    String name();
    boolean isAvailable();
    CompletableFuture<TestRun> submit(TestRun run, List<BenchmarkTask> tasks);
    TestRun refreshStatus(TestRun run);
    void cancel(TestRun run);
}
```

**`name()`** — Returns the backend's identifier (e.g., `"native"`, `"trogdor"`, `"openmessaging"`). This is the value users pass in the `"backend"` field of the test request.

**`isAvailable()`** — Returns `true` if the backend is ready to accept test submissions. The native backend always returns `true`. The Trogdor backend checks whether the Trogdor Coordinator is reachable. Your implementation should check whatever external dependency it requires.

**`submit()`** — Accepts the test run and a list of benchmark tasks (producers, consumers) and begins executing them. Returns a `CompletableFuture` that completes when all tasks finish. The test run is updated with task-level `TestResult` objects as tasks complete.

**`refreshStatus()`** — Called periodically by the orchestrator to check the status of a running test. The implementation should query the underlying benchmark tool for current metrics and update the test run's results.

**`cancel()`** — Stops a running test and cleans up resources.

### Implementation Example: OpenMessaging Backend

```java
@ApplicationScoped
@Named("openmessaging")
public class OpenMessagingBackend implements BenchmarkBackend {

    @Override
    public String name() {
        return "openmessaging";
    }

    @Override
    public boolean isAvailable() {
        // Check if the OpenMessaging benchmark JAR is on the classpath
        try {
            Class.forName("io.openmessaging.benchmark.Benchmark");
            return true;
        } catch (ClassNotFoundException e) {
            return false;
        }
    }

    @Override
    public CompletableFuture<TestRun> submit(TestRun run, List<BenchmarkTask> tasks) {
        return CompletableFuture.supplyAsync(() -> {
            // Convert BenchmarkTasks to OpenMessaging workload configuration
            // Run the benchmark
            // Convert results back to TestResult objects
            return run;
        });
    }

    // ... refreshStatus() and cancel()
}
```

The `@Named("openmessaging")` annotation registers this backend so that users can request it with `"backend": "openmessaging"` in their test requests. The `TestOrchestrator` discovers all `BenchmarkBackend` beans at startup and offers them through the `GET /api/tests/backends` endpoint.

## Writing a Custom ChaosProvider

The `ChaosProvider` SPI defines how faults are injected and cleaned up. The `KubernetesChaosProvider` uses the Kubernetes API directly (pod delete, scale down). The `LitmusChaosProvider` creates Litmus `ChaosEngine` CRDs. You might want to write a custom provider for:

- A different chaos framework (e.g., Chaos Mesh, Toxiproxy)
- Cloud-provider-specific fault injection (AWS FIS, Azure Chaos Studio)
- A custom fault type not covered by the built-in providers

### The Interface

```java
public interface ChaosProvider {
    String name();
    boolean supports(FaultSpec spec);
    CompletableFuture<ChaosOutcome> inject(FaultSpec spec);
    void cleanup(FaultSpec spec);
}
```

**`name()`** — Provider identifier (e.g., `"kubernetes"`, `"litmus"`, `"chaos-mesh"`).

**`supports()`** — Returns `true` if this provider can handle the given fault specification. The `HybridChaosProvider` uses this method to route faults to the best available provider. If you implement a provider that only handles network-related faults, return `true` only for `NETWORK_PARTITION`, `NETWORK_CORRUPT`, `NETWORK_DELAY`, and `NETWORK_LOSS`.

**`inject()`** — Executes the fault. Returns a `CompletableFuture<ChaosOutcome>` that completes when the fault has been applied (or has run for its full duration, for time-bounded faults like `CPU_STRESS`).

**`cleanup()`** — Reverses the fault. This is called by the auto-rollback mechanism if a step fails. Implementations must be idempotent — calling cleanup twice should not cause an error.

### Implementation Example: Chaos Mesh Provider

```java
@ApplicationScoped
@Named("chaos-mesh")
public class ChaosMeshProvider implements ChaosProvider {

    @Inject
    KubernetesClient k8s;

    @Override
    public String name() {
        return "chaos-mesh";
    }

    @Override
    public boolean supports(FaultSpec spec) {
        // Chaos Mesh supports all disruption types
        return true;
    }

    @Override
    public CompletableFuture<ChaosOutcome> inject(FaultSpec spec) {
        return CompletableFuture.supplyAsync(() -> {
            // 1. Build the Chaos Mesh CRD from the FaultSpec
            // 2. Apply it to the cluster
            // 3. Wait for the experiment to complete
            // 4. Return the outcome
            return new ChaosOutcome(/* ... */);
        });
    }

    @Override
    public void cleanup(FaultSpec spec) {
        // Delete all Chaos Mesh CRDs created by this provider
        // Must be idempotent
    }
}
```

### Registering with HybridChaosProvider

The `HybridChaosProvider` automatically discovers all `ChaosProvider` beans and routes faults to the provider that claims to support them. If multiple providers support the same fault type, the Hybrid provider uses a priority order (Litmus → Chaos Mesh → Kubernetes → NoOp). To insert your provider at a specific priority, implement the `Comparable<ChaosProvider>` interface or add a `@Priority` annotation.

## Writing a Custom ExportStrategy

The `ExportStrategy` SPI defines how test results are serialized for external consumption. You might want a custom exporter for:

- A metrics backend (InfluxDB, Datadog, CloudWatch)
- A notification system (Slack, PagerDuty)
- A custom report format (PDF, HTML dashboard)

### The Interface

```java
public interface ExportStrategy {
    String format();
    byte[] export(TestReport report);
}
```

**`format()`** — The format name (e.g., `"csv"`, `"junit-xml"`, `"influxdb"`). This is used in the export API to select the format.

**`export()`** — Converts the test report to bytes in the target format.

### CDI Registration Pattern

All three SPIs use the same CDI registration pattern:

```java
@ApplicationScoped  // One instance per application lifecycle
@Named("my-format") // Identifier for selection
public class MyExporter implements ExportStrategy { ... }
```

Quarkus discovers the bean at build time (during augmentation), so there is no runtime classpath scanning overhead. The bean is instantiated lazily on first use.

## Testing Your Extension

Kates' test suite uses `@InjectMock` to replace providers during testing. When writing tests for your custom provider, follow the same pattern:

```java
@QuarkusTest
class ChaosMeshProviderTest {

    @Inject
    ChaosMeshProvider provider;

    @InjectMock
    KubernetesClient k8s;

    @Test
    void shouldInjectPodKill() {
        // Arrange: mock the Kubernetes client to accept CRD creation
        when(k8s.resource(any())).thenReturn(/* ... */);

        // Act: inject the fault
        FaultSpec spec = new FaultSpec("test", DisruptionType.POD_KILL, /* ... */);
        ChaosOutcome outcome = provider.inject(spec).join();

        // Assert: verify the CRD was created with the correct spec
        verify(k8s).resource(argThat(crd -> /* ... */));
    }
}
```

This approach tests the provider's logic without requiring a real Kubernetes cluster with Chaos Mesh installed. The integration between the provider and the real cluster is verified separately in staging.
