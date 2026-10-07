package com.bmscomp.kates.chaos;

/**
 * A {@link K8sPodWatcher.WatchSession} fed by hand, the way its watch feeds
 * it, for tests outside this package: they cannot reach the methods the watch
 * calls.
 */
public final class TestPodWatch {

    private final K8sPodWatcher.WatchSession session = new K8sPodWatcher.WatchSession();

    /** Watches these pods, every one Ready. */
    public TestPodWatch(String... pods) {
        session.expectPods(pods.length);
        for (String pod : pods) {
            session.recordInitial(pod, true);
        }
    }

    public K8sPodWatcher.WatchSession session() {
        return session;
    }

    /** A watch event: the pod is no longer Ready. */
    public void notReady(String pod) {
        session.recordNotReady(pod);
    }

    /** A watch event: the pod is Ready. */
    public void ready(String pod) {
        session.recordReady(pod);
    }
}
