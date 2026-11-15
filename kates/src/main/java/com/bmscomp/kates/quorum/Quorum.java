package com.bmscomp.kates.quorum;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;

import io.quarkus.runtime.ShutdownEvent;
import io.quarkus.runtime.StartupEvent;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * The quorum this Kates belongs to. Without {@code kates.quorum.peers} there is none: the instance
 * is alone, and {@link #isLeader()} is always true.
 */
@ApplicationScoped
public class Quorum {

    private static final Logger LOG = Logger.getLogger(Quorum.class);

    @ConfigProperty(name = "kates.quorum.peers")
    Optional<List<String>> peers;

    // Unset: the pod name, which is what the operator names a StatefulSet's members.
    @ConfigProperty(name = "kates.quorum.member-id")
    Optional<String> memberId;

    @ConfigProperty(name = "kates.quorum.dir")
    Optional<String> dir;

    @ConfigProperty(name = "kates.quorum.tls.cert")
    Optional<String> cert;

    @ConfigProperty(name = "kates.quorum.tls.key")
    Optional<String> key;

    @ConfigProperty(name = "kates.quorum.tls.ca")
    Optional<String> ca;

    private volatile QuorumMember member;

    void start(@Observes StartupEvent event) throws IOException {
        if (peers.isEmpty()) {
            return;
        }
        QuorumMember.Settings settings = QuorumMember.Settings.parse(
                peers.get(),
                memberId.orElse(System.getenv("HOSTNAME")),
                dir.orElse(null),
                cert.orElse(null),
                key.orElse(null),
                ca.orElse(null));
        member = QuorumMember.start(settings);
        LOG.infof(
                "quorum member %s started, %d members",
                settings.memberId(), settings.peers().size());
    }

    void stop(@Observes ShutdownEvent event) {
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

    /** Whether this instance leads: always, with no quorum; otherwise once the quorum says so. */
    public boolean isLeader() {
        QuorumMember current = member;
        return current == null || current.isLeader();
    }

    /** What {@code /api/health} shows. */
    public Map<String, Object> health() {
        QuorumMember current = member;
        Map<String, Object> health = new LinkedHashMap<>();
        health.put("enabled", current != null);
        if (current != null) {
            health.put("memberId", current.memberId());
            health.put("leader", current.isLeader());
            health.put("leaderId", current.leaderId());
            health.put("term", current.term());
        }
        return health;
    }
}
