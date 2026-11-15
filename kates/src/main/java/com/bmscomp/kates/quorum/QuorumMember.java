package com.bmscomp.kates.quorum;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import org.apache.ratis.client.RaftClient;
import org.apache.ratis.client.RaftClientConfigKeys;
import org.apache.ratis.conf.Parameters;
import org.apache.ratis.conf.RaftProperties;
import org.apache.ratis.grpc.GrpcConfigKeys;
import org.apache.ratis.grpc.GrpcTlsConfig;
import org.apache.ratis.protocol.Message;
import org.apache.ratis.protocol.RaftGroup;
import org.apache.ratis.protocol.RaftGroupId;
import org.apache.ratis.protocol.RaftPeer;
import org.apache.ratis.protocol.RaftPeerId;
import org.apache.ratis.retry.RetryPolicies;
import org.apache.ratis.security.TlsConf;
import org.apache.ratis.server.DivisionInfo;
import org.apache.ratis.server.RaftServer;
import org.apache.ratis.server.RaftServerConfigKeys;
import org.apache.ratis.server.storage.RaftStorage;
import org.apache.ratis.statemachine.impl.BaseStateMachine;
import org.apache.ratis.thirdparty.io.netty.handler.ssl.SslProvider;
import org.apache.ratis.util.TimeDuration;

/**
 * One member of the Kates quorum: a Ratis server whose peers reach it over mutual TLS. It
 * replicates nothing yet, and only elects a leader.
 */
public final class QuorumMember implements AutoCloseable {

    private static final RaftGroupId GROUP =
            RaftGroupId.valueOf(UUID.nameUUIDFromBytes("kates-quorum".getBytes(StandardCharsets.UTF_8)));
    // Ratis' own 150-300 ms suits a quiet host; a CPU-throttled pod would hold spurious elections.
    private static final TimeDuration ELECTION_MIN = TimeDuration.valueOf(1, TimeUnit.SECONDS);
    private static final TimeDuration ELECTION_MAX = TimeDuration.valueOf(2, TimeUnit.SECONDS);
    // Under the election timeout: past it, another member may lead, so the answer is no.
    private static final TimeDuration CONFIRM_TIMEOUT = TimeDuration.valueOf(800, TimeUnit.MILLISECONDS);

    /** What a member needs: every member's id and address, its own id, a directory, its TLS files. */
    public record Settings(String memberId, List<RaftPeer> peers, Path dir, Path cert, Path key, Path ca) {

        /**
         * Reads {@code id=host:port} entries. Every other field is required, and the files must exist:
         * a member never starts without mutual TLS.
         *
         * @throws IllegalArgumentException naming the setting that is wrong
         */
        public static Settings parse(
                List<String> peers, String memberId, String dir, String cert, String key, String ca) {
            List<RaftPeer> list = new ArrayList<>();
            Set<String> ids = new HashSet<>();
            for (String entry : peers) {
                String[] parts = entry.trim().split("=", 2);
                int colon = parts.length == 2 ? parts[1].lastIndexOf(':') : -1;
                if (parts[0].isBlank() || colon < 1) {
                    throw new IllegalArgumentException("kates.quorum.peers: '" + entry + "' is not id=host:port");
                }
                checkPort(parts[1].substring(colon + 1));
                if (!ids.add(parts[0])) {
                    throw new IllegalArgumentException("kates.quorum.peers: the id '" + parts[0] + "' is listed twice");
                }
                list.add(RaftPeer.newBuilder()
                        .setId(parts[0])
                        .setAddress(parts[1])
                        .build());
            }
            if (list.isEmpty()) {
                throw new IllegalArgumentException("kates.quorum.peers: no member listed");
            }
            if (memberId == null || !ids.contains(memberId)) {
                throw new IllegalArgumentException(
                        "kates.quorum.member-id: '" + memberId + "' is not among kates.quorum.peers " + ids);
            }
            return new Settings(
                    memberId,
                    List.copyOf(list),
                    required("kates.quorum.dir", dir),
                    file("kates.quorum.tls.cert", cert),
                    file("kates.quorum.tls.key", key),
                    file("kates.quorum.tls.ca", ca));
        }

        RaftPeer self() {
            return peers.stream()
                    .filter(peer -> peer.getId().toString().equals(memberId))
                    .findFirst()
                    .orElseThrow();
        }

        private static void checkPort(String value) {
            try {
                int port = Integer.parseInt(value);
                if (port >= 1 && port <= 65535) {
                    return;
                }
            } catch (NumberFormatException ignored) {
                // reported below
            }
            throw new IllegalArgumentException("kates.quorum.peers: '" + value + "' is not a port");
        }

        private static Path required(String setting, String value) {
            if (value == null || value.isBlank()) {
                throw new IllegalArgumentException(setting + " is required with kates.quorum.peers");
            }
            return Path.of(value);
        }

        private static Path file(String setting, String value) {
            Path path = required(setting, value);
            if (!Files.isReadable(path)) {
                throw new IllegalArgumentException(setting + ": " + path + " is not a readable file");
            }
            return path;
        }
    }

    private final RaftServer server;
    private final String memberId;

    private final RaftClient client;
    private final RaftPeerId selfId;

    private QuorumMember(RaftServer server, RaftClient client, RaftPeerId selfId, String memberId) {
        this.server = server;
        this.client = client;
        this.selfId = selfId;
        this.memberId = memberId;
    }

    /** Starts the member. Its term and vote are kept in {@code dir}, which must outlive a restart. */
    public static QuorumMember start(Settings settings) throws IOException {
        RaftPeer self = settings.self();
        String address = self.getAddress();
        RaftProperties properties = new RaftProperties();
        GrpcConfigKeys.Server.setPort(properties, Integer.parseInt(address.substring(address.lastIndexOf(':') + 1)));
        RaftServerConfigKeys.setStorageDir(properties, List.of(settings.dir().toFile()));
        RaftServerConfigKeys.Rpc.setTimeoutMin(properties, ELECTION_MIN);
        RaftServerConfigKeys.Rpc.setTimeoutMax(properties, ELECTION_MAX);
        // A read on the leader proves it still leads: within its lease, or after a majority answers.
        RaftServerConfigKeys.Read.setOption(properties, RaftServerConfigKeys.Read.Option.LINEARIZABLE);
        RaftServerConfigKeys.Read.setLeaderLeaseEnabled(properties, true);
        RaftServerConfigKeys.Read.setTimeout(properties, CONFIRM_TIMEOUT);
        Parameters parameters = new Parameters();
        // The JDK's TLS, not the netty-tcnative Ratis' jar carries, whose library a native image cannot load.
        GrpcConfigKeys.TLS.setConf(
                parameters,
                GrpcTlsConfig.newBuilder()
                        .setPrivateKey(new TlsConf.PrivateKeyConf(settings.key().toFile()))
                        .setKeyCertificates(
                                new TlsConf.CertificatesConf(settings.cert().toFile()))
                        .setTrustCertificates(
                                new TlsConf.CertificatesConf(settings.ca().toFile()))
                        .setMutualTls(true)
                        .setSslProvider(SslProvider.JDK)
                        .build());
        RaftGroup group = RaftGroup.valueOf(GROUP, settings.peers());
        RaftServer server = RaftServer.newBuilder()
                .setServerId(self.getId())
                .setGroup(group)
                .setProperties(properties)
                .setParameters(parameters)
                .setStateMachine(new Confirmations())
                .setOption(RaftStorage.StartupOption.RECOVER)
                .build();
        server.start();
        RaftProperties clientProperties = new RaftProperties();
        RaftClientConfigKeys.Rpc.setRequestTimeout(clientProperties, CONFIRM_TIMEOUT);
        RaftClient client = RaftClient.newBuilder()
                .setRaftGroup(group)
                .setProperties(clientProperties)
                .setParameters(parameters)
                .setRetryPolicy(RetryPolicies.noRetry())
                .build();
        return new QuorumMember(server, client, self.getId(), settings.memberId());
    }

    public String memberId() {
        return memberId;
    }

    /** True once this member leads and has committed an entry of its term, so it knows it leads. */
    public boolean isLeader() {
        return info().isLeaderReady();
    }

    /**
     * True only if this member leads now, which {@link #isLeader()} cannot say: a leader cut off
     * from its majority goes on saying it leads until it steps down. This asks the quorum, in a
     * linearizable read served by the leader's lease, or by a majority within the timeout.
     */
    public boolean confirmLeadership() {
        if (!isLeader()) {
            return false;
        }
        try {
            return client.io().sendReadOnly(Message.EMPTY, selfId).isSuccess();
        } catch (IOException e) {
            return false;
        }
    }

    /** The leader this member knows of, or null during an election. */
    public String leaderId() {
        RaftPeerId leader = info().getLeaderId();
        return leader == null ? null : leader.toString();
    }

    public long term() {
        return info().getCurrentTerm();
    }

    /**
     * The last log index this member applied. A member with an empty log does not count as a voter
     * once the quorum has committed anything (Ratis' rule, for a member that lost its disk), so a
     * member counts toward a majority only once it has caught up to some entry.
     */
    public long appliedIndex() {
        return info().getLastAppliedIndex();
    }

    private DivisionInfo info() {
        try {
            return server.getDivision(GROUP).getInfo();
        } catch (IOException e) {
            throw new IllegalStateException("the quorum group is not served", e);
        }
    }

    @Override
    public void close() throws IOException {
        try {
            client.close();
        } finally {
            server.close();
        }
    }

    /** Replicates nothing yet; answers the reads that confirm leadership. */
    private static final class Confirmations extends BaseStateMachine {
        @Override
        public CompletableFuture<Message> query(Message request) {
            return CompletableFuture.completedFuture(Message.EMPTY);
        }
    }
}
