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
import java.util.concurrent.TimeUnit;

import org.apache.ratis.conf.Parameters;
import org.apache.ratis.conf.RaftProperties;
import org.apache.ratis.grpc.GrpcConfigKeys;
import org.apache.ratis.grpc.GrpcTlsConfig;
import org.apache.ratis.protocol.RaftGroup;
import org.apache.ratis.protocol.RaftGroupId;
import org.apache.ratis.protocol.RaftPeer;
import org.apache.ratis.protocol.RaftPeerId;
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

    private QuorumMember(RaftServer server, String memberId) {
        this.server = server;
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
        RaftServer server = RaftServer.newBuilder()
                .setServerId(self.getId())
                .setGroup(RaftGroup.valueOf(GROUP, settings.peers()))
                .setProperties(properties)
                .setParameters(parameters)
                .setStateMachine(new BaseStateMachine())
                .setOption(RaftStorage.StartupOption.RECOVER)
                .build();
        server.start();
        return new QuorumMember(server, settings.memberId());
    }

    public String memberId() {
        return memberId;
    }

    /** True once this member leads and has committed an entry of its term, so it knows it leads. */
    public boolean isLeader() {
        return info().isLeaderReady();
    }

    /** The leader this member knows of, or null during an election. */
    public String leaderId() {
        RaftPeerId leader = info().getLeaderId();
        return leader == null ? null : leader.toString();
    }

    public long term() {
        return info().getCurrentTerm();
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
        server.close();
    }
}
