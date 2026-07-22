package com.bmscomp.kates.engine;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.atomic.AtomicInteger;
import jakarta.annotation.PreDestroy;
import jakarta.enterprise.context.ApplicationScoped;

import org.jboss.logging.Logger;

/**
 * Shared, bounded executor for long-running, blocking async work (benchmark
 * orchestration, CDC verification, chaos/K8s calls).
 *
 * <p>Previously this work ran on {@code CompletableFuture.supplyAsync(...)}
 * with no executor — i.e. the common {@link java.util.concurrent.ForkJoinPool},
 * whose parallelism is only {@code cores - 1} and is shared with every parallel
 * stream in the JVM. A handful of blocking Kafka/Kubernetes calls can starve
 * it. This pool is dedicated, virtual-thread-backed (cheap for blocking I/O),
 * and shut down cleanly with the application.
 */
@ApplicationScoped
public class KatesExecutor {

    private static final Logger LOG = Logger.getLogger(KatesExecutor.class);

    private final ExecutorService delegate = Executors.newThreadPerTaskExecutor(new ThreadFactory() {
        private final AtomicInteger counter = new AtomicInteger();

        @Override
        public Thread newThread(Runnable r) {
            return Thread.ofVirtual()
                    .name("kates-async-", counter.incrementAndGet())
                    .unstarted(r);
        }
    });

    /** The shared executor — pass this as the second arg to supplyAsync/runAsync. */
    public ExecutorService get() {
        return delegate;
    }

    @PreDestroy
    void shutdown() {
        delegate.shutdown();
        LOG.debug("KatesExecutor shut down");
    }
}
