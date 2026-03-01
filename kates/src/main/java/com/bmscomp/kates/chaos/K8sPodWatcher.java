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

    public record RecoveryMetrics(
            Duration timeToFirstReady, Duration timeToAllReady, int totalPods, int recoveredPods) {}

    public static class WatchSession {
        private final List<PodEvent> events = new CopyOnWriteArrayList<>();
        private final Set<String> readyPods = Collections.synchronizedSet(new LinkedHashSet<>());
        private final Set<String> allPods = Collections.synchronizedSet(new LinkedHashSet<>());
        private final CountDownLatch firstReadyLatch = new CountDownLatch(1);
        private volatile Watch watch;
        private volatile Instant disruptionStart;
        private volatile Instant firstReadyTime;
        private volatile Instant allReadyTime;

        public List<PodEvent> getEvents() {
            return Collections.unmodifiableList(events);
        }

        public void markDisruptionStart() {
            this.disruptionStart = Instant.now();
            this.firstReadyTime = null;
            this.allReadyTime = null;
            this.readyPods.clear();
        }

        void addEvent(PodEvent event) {
            events.add(event);
        }

        void recordReady(String podName, int expectedPods) {
            readyPods.add(podName);
            if (firstReadyTime == null && disruptionStart != null) {
                firstReadyTime = Instant.now();
                firstReadyLatch.countDown();
            }
            if (readyPods.size() >= expectedPods && allReadyTime == null && disruptionStart != null) {
                allReadyTime = Instant.now();
            }
        }

        void recordNotReady(String podName) {
            readyPods.remove(podName);
            allReadyTime = null;
        }

        public RecoveryMetrics computeRecovery() {
            Duration tfr = firstReadyTime != null && disruptionStart != null
                    ? Duration.between(disruptionStart, firstReadyTime)
                    : null;
            Duration tar = allReadyTime != null && disruptionStart != null
                    ? Duration.between(disruptionStart, allReadyTime)
                    : null;
            return new RecoveryMetrics(tfr, tar, allPods.size(), readyPods.size());
        }

        public boolean awaitFirstReady(long timeout, TimeUnit unit) throws InterruptedException {
            return firstReadyLatch.await(timeout, unit);
        }

        public void close() {
            if (watch != null) {
                watch.close();
            }
        }
    }

    public WatchSession startWatching(String namespace, String labelSelector) {
        WatchSession session = new WatchSession();

        String[] parts = labelSelector.split("=", 2);
        String labelKey = parts[0];
        String labelValue = parts.length > 1 ? parts[1] : "";

        var podList = client.pods()
                .inNamespace(namespace)
                .withLabel(labelKey, labelValue)
                .list();

        int expectedPods = podList.getItems().size();
        for (Pod pod : podList.getItems()) {
            String podName = pod.getMetadata().getName();
            session.allPods.add(podName);
            if (isPodReady(pod)) {
                session.readyPods.add(podName);
            }
        }

        LOG.info("Starting pod watch: namespace=" + namespace + " label=" + labelSelector + " pods=" + expectedPods);

        Watch watch = client.pods()
                .inNamespace(namespace)
                .withLabel(labelKey, labelValue)
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
                            session.recordReady(podName, expectedPods);
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

    private boolean isPodReady(Pod pod) {
        if (pod.getStatus() == null || pod.getStatus().getConditions() == null) {
            return false;
        }
        return pod.getStatus().getConditions().stream()
                .filter(c -> "Ready".equals(c.getType()))
                .anyMatch(c -> "True".equals(c.getStatus()));
    }
}
