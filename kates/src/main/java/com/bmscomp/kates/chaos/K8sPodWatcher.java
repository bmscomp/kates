package com.bmscomp.kates.chaos;

import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.Watch;
import io.fabric8.kubernetes.client.Watcher;
import io.fabric8.kubernetes.client.WatcherException;
import org.jboss.logging.Logger;

/**
 * Watches Kafka broker pods during a disruption and records a timeline
 * of lifecycle events. Computes Time-to-First-Ready (TFR) and
 * Time-to-All-Ready (TAR) after disruption injection.
 */
@ApplicationScoped
public class K8sPodWatcher {

    private static final Logger LOG = Logger.getLogger(K8sPodWatcher.class.getName());

    @Inject
    KubernetesClient client;

    public record PodEvent(
            Instant timestamp, String podName, String eventType, String phase, String reason, String message) {}

    /**
     * @param podWentDown whether any watched pod stopped being Ready after the
     *     disruption started. When one did and {@code timeToAllReady} is null,
     *     the pods had not all come back when the timings were taken.
     */
    public record RecoveryMetrics(
            Duration timeToFirstReady,
            Duration timeToAllReady,
            int totalPods,
            int recoveredPods,
            boolean podWentDown) {}

    public static class WatchSession {
        private final List<PodEvent> events = new CopyOnWriteArrayList<>();
        /** Pods that reported Ready since the disruption started. */
        private final Set<String> readyPods = Collections.synchronizedSet(new LinkedHashSet<>());
        /**
         * Pods Ready right now. Unlike {@link #readyPods} this is not cleared
         * at the disruption start: a broker the fault left alone sends no
         * event afterwards, so counting only pods that reported since then
         * never reached the total after a single-pod fault, and time-to-all-
         * ready was never set.
         */
        private final Set<String> currentlyReady = Collections.synchronizedSet(new LinkedHashSet<>());

        private final Set<String> allPods = Collections.synchronizedSet(new LinkedHashSet<>());
        private final CountDownLatch firstReadyLatch = new CountDownLatch(1);
        private final Object readyChanged = new Object();
        private volatile Watch watch;
        private volatile int expectedPods;
        private volatile Instant disruptionStart;
        private volatile Instant firstReadyTime;
        private volatile Instant allReadyTime;
        private volatile boolean podWentDown;

        public List<PodEvent> getEvents() {
            return Collections.unmodifiableList(events);
        }

        public void markDisruptionStart() {
            this.disruptionStart = Instant.now();
            this.firstReadyTime = null;
            this.allReadyTime = null;
            this.podWentDown = false;
            this.readyPods.clear();
        }

        void addEvent(PodEvent event) {
            events.add(event);
        }

        /** A pod from the list taken before watching starts. */
        void recordInitial(String podName, boolean ready) {
            allPods.add(podName);
            if (ready) {
                readyPods.add(podName);
                currentlyReady.add(podName);
            }
        }

        void expectPods(int expectedPods) {
            this.expectedPods = expectedPods;
        }

        void recordReady(String podName) {
            readyPods.add(podName);
            currentlyReady.add(podName);
            if (firstReadyTime == null && disruptionStart != null) {
                firstReadyTime = Instant.now();
                firstReadyLatch.countDown();
            }
            if (podWentDown && currentlyReady.size() >= expectedPods && allReadyTime == null) {
                allReadyTime = Instant.now();
            }
            synchronized (readyChanged) {
                readyChanged.notifyAll();
            }
        }

        void recordNotReady(String podName) {
            readyPods.remove(podName);
            currentlyReady.remove(podName);
            if (disruptionStart != null) {
                podWentDown = true;
            }
            allReadyTime = null;
        }

        public RecoveryMetrics computeRecovery() {
            Duration tfr = firstReadyTime != null && disruptionStart != null
                    ? Duration.between(disruptionStart, firstReadyTime)
                    : null;
            Duration tar = allReadyTime != null && disruptionStart != null
                    ? Duration.between(disruptionStart, allReadyTime)
                    : null;
            return new RecoveryMetrics(tfr, tar, allPods.size(), readyPods.size(), podWentDown);
        }

        /**
         * Waits, within one timeout, for a pod to report Ready after the
         * disruption and then for every watched pod to be Ready. Waiting for
         * the first one alone let a fault that took down several pods pass
         * the gate while the rest were still restarting.
         */
        public boolean awaitRecovery(long timeout, TimeUnit unit) throws InterruptedException {
            long deadline = System.nanoTime() + unit.toNanos(timeout);
            if (!firstReadyLatch.await(timeout, unit)) {
                return false;
            }
            synchronized (readyChanged) {
                while (currentlyReady.size() < expectedPods) {
                    long left = deadline - System.nanoTime();
                    if (left <= 0) {
                        return false;
                    }
                    TimeUnit.NANOSECONDS.timedWait(readyChanged, left);
                }
            }
            return true;
        }

        public void close() {
            if (watch != null) {
                watch.close();
            }
        }
    }

    public WatchSession startWatching(String namespace, String labelSelector) {
        WatchSession session = new WatchSession();

        String selector = ParsedLabelSelector.parse(labelSelector).toString();

        var podList =
                client.pods().inNamespace(namespace).withLabelSelector(selector).list();

        int expectedPods = podList.getItems().size();
        session.expectPods(expectedPods);
        for (Pod pod : podList.getItems()) {
            session.recordInitial(pod.getMetadata().getName(), isPodReady(pod));
        }

        LOG.info("Starting pod watch: namespace=" + namespace + " label=" + labelSelector + " pods=" + expectedPods);

        Watch watch = client.pods()
                .inNamespace(namespace)
                .withLabelSelector(selector)
                .watch(new Watcher<>() {
                    @Override
                    public void eventReceived(Action action, Pod pod) {
                        String podName = pod.getMetadata().getName();
                        String phase = pod.getStatus() != null ? pod.getStatus().getPhase() : "Unknown";

                        session.allPods.add(podName);

                        PodEvent event = new PodEvent(Instant.now(), podName, action.name(), phase, "", "");
                        session.addEvent(event);

                        if (action == Action.DELETED) {
                            session.recordNotReady(podName);
                        } else if (isPodReady(pod)) {
                            session.recordReady(podName);
                        } else {
                            session.recordNotReady(podName);
                        }
                    }

                    @Override
                    public void onClose(WatcherException cause) {
                        if (cause != null) {
                            LOG.warn("Pod watch closed with error", cause);
                        }
                    }
                });

        session.watch = watch;
        return session;
    }

    /**
     * A terminating pod keeps its Ready condition until its containers stop,
     * so without the deletion check a graceful delete looked recovered the
     * moment it started.
     */
    static boolean isPodReady(Pod pod) {
        if (pod.getMetadata() != null && pod.getMetadata().getDeletionTimestamp() != null) {
            return false;
        }
        if (pod.getStatus() == null || pod.getStatus().getConditions() == null) {
            return false;
        }
        return pod.getStatus().getConditions().stream()
                .filter(c -> "Ready".equals(c.getType()))
                .anyMatch(c -> "True".equals(c.getStatus()));
    }
}
