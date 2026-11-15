package com.bmscomp.kates.quorum;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Event;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;

import io.quarkus.runtime.ShutdownEvent;
import io.quarkus.runtime.StartupEvent;
import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * The quorum this Kates belongs to. Without {@code kates.quorum.peers} there is none: the instance
 * is alone, leads, and takes every write. With one, only the leader takes writes, starts runs and
 * injects faults, and only once it has recovered what the previous leader left
 * ({@link LeadershipAcquired}).
 */
// Built without config (as tests build it), a Quorum is alone.
@ApplicationScoped
public class Quorum {

    private static final Logger LOG = Logger.getLogger(Quorum.class);

    @ConfigProperty(name = "kates.quorum.peers")
    Optional<List<String>> peers = Optional.empty();

    // Unset: the pod name, which is what the operator names a StatefulSet's members.
    @ConfigProperty(name = "kates.quorum.member-id")
    Optional<String> memberId = Optional.empty();

    @ConfigProperty(name = "kates.quorum.dir")
    Optional<String> dir = Optional.empty();

    @ConfigProperty(name = "kates.quorum.tls.cert")
    Optional<String> cert = Optional.empty();

    @ConfigProperty(name = "kates.quorum.tls.key")
    Optional<String> key = Optional.empty();

    @ConfigProperty(name = "kates.quorum.tls.ca")
    Optional<String> ca = Optional.empty();

    @Inject
    Event<LeadershipAcquired> takeover;

    private volatile QuorumMember member;
    private volatile long recoveredTerm = -1;
    private QuorumMember.Settings settings;
    private volatile boolean stopping;
    private volatile Instant notServingSince;

    void start(@Observes StartupEvent event) throws IOException {
        if (peers.isEmpty()) {
            return;
        }
        // Ratis exits the JVM when a server it starts cannot bind, which would kill live runs over a
        // member this class starts again anyway: an ExitException instead, caught by revive().
        org.apache.ratis.util.ExitUtils.disableSystemExit();
        settings = QuorumMember.Settings.parse(
                peers.get(), memberId(), dir.orElse(null), cert.orElse(null), key.orElse(null), ca.orElse(null));
        member = QuorumMember.start(settings);
        LOG.infof(
                "quorum member %s started, %d members",
                settings.memberId(), settings.peers().size());
    }

    void stop(@Observes ShutdownEvent event) {
        stopping = true;
        QuorumMember current = member;
        if (current == null) {
            return;
        }
        try {
            current.close();
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    /** This member's id, from its settings, before the member has started; null with no quorum. */
    public String memberId() {
        return peers.isEmpty() ? null : memberId.orElse(System.getenv("HOSTNAME"));
    }

    /** Whether this instance leads, as it last heard: cheap, and late by up to a step-down. */
    public boolean isLeader() {
        QuorumMember current = member;
        return current == null || (current.serving() && current.isLeader());
    }

    /**
     * Whether this instance may take a write, start a run or inject a fault now: always with no
     * quorum. A member must confirm with the quorum that it leads, and on its first write of a term
     * it recovers first: {@link LeadershipAcquired}'s observers run, and until they succeed it says
     * no. A member whose quorum has not started yet says no.
     */
    public boolean acceptsWrites() {
        QuorumMember current = member;
        if (current == null) {
            return peers.isEmpty();
        }
        if (!current.serving() || !current.confirmLeadership()) {
            return false;
        }
        long term = current.term();
        if (recoveredTerm == term) {
            return true;
        }
        synchronized (this) {
            if (recoveredTerm != term) {
                try {
                    takeover.fire(new LeadershipAcquired(term, Instant.now()));
                } catch (RuntimeException e) {
                    LOG.warnf("Leading term %d, but recovery failed, so no write is taken yet: %s", term, e);
                    return false;
                }
                recoveredTerm = term;
                LOG.infof("Member %s leads term %d and takes writes", current.memberId(), term);
            }
        }
        return true;
    }

    // A new leader recovers within seconds, whether or not a write comes in.
    @Scheduled(every = "2s", identity = "quorum-takeover", concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void takeOver() {
        if (member != null) {
            revive();
            acceptsWrites();
        }
    }

    /**
     * Starts the member again from its disk if Ratis has closed its server, as it does after a
     * pause longer than raft.server.close.threshold (60 s): the process lives on, and without this
     * the member is lost to the quorum until the pod restarts, answering health with its old term.
     */
    private void revive() {
        QuorumMember current = member;
        if (stopping || current.serving()) {
            return;
        }
        if (notServingSince == null) {
            notServingSince = Instant.now();
        }
        LOG.warnf(
                "quorum member %s: Ratis server %s, starting it again from its disk",
                current.memberId(), current.state());
        try {
            current.close();
        } catch (IOException | RuntimeException e) {
            LOG.debugf("closing the old member: %s", e);
        }
        try {
            member = QuorumMember.start(settings);
            notServingSince = null;
            recoveredTerm = -1;
            LOG.infof("quorum member %s serves again", settings.memberId());
        } catch (IOException | RuntimeException e) {
            LOG.warnf("quorum member %s could not start again, retrying: %s", settings.memberId(), e);
        }
    }

    /** Since when this member's Ratis server has not been running; empty while it serves, or with no quorum. */
    public Optional<Instant> notServingSince() {
        return Optional.ofNullable(notServingSince);
    }

    QuorumMember member() {
        return member;
    }

    /** The term this member is in, as it last heard; {@code 0} with no quorum or while it does not serve. */
    public long term() {
        QuorumMember current = member;
        return current == null || !current.serving() ? 0 : current.term();
    }

    /**
     * Throws unless this member may write now, as {@link #acceptsWrites()} decides: called right
     * before a fault goes into Kubernetes, so a member that stopped leading since its plan started
     * injects nothing.
     */
    public void requireWrites() {
        if (!acceptsWrites()) {
            throw new com.bmscomp.kates.engine.NotLeaderException(leaderId());
        }
    }

    /** The leader this member knows of, or null with no quorum or during an election. */
    public String leaderId() {
        QuorumMember current = member;
        return current == null || !current.serving() ? null : current.leaderId();
    }

    /** What {@code /api/health} shows. */
    public Map<String, Object> health() {
        QuorumMember current = member;
        Map<String, Object> health = new LinkedHashMap<>();
        health.put("enabled", current != null);
        if (current != null) {
            health.put("state", current.state().name());
            health.put("memberId", current.memberId());
        }
        if (current != null && current.serving()) {
            health.put("leader", current.isLeader());
            health.put("leaderId", current.leaderId());
            health.put("term", current.term());
            health.put("appliedIndex", current.appliedIndex());
        }
        return health;
    }
}
