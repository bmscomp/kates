package com.bmscomp.kates.quorum;

import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.security.GeneralSecurityException;
import java.security.KeyException;
import java.security.PrivateKey;
import javax.net.ssl.SSLEngine;

import com.oracle.svm.core.annotate.Alias;
import com.oracle.svm.core.annotate.Substitute;
import com.oracle.svm.core.annotate.TargetClass;
import org.apache.ratis.thirdparty.io.netty.buffer.ByteBuf;
import org.apache.ratis.thirdparty.io.netty.buffer.ByteBufAllocator;
import org.apache.ratis.thirdparty.io.netty.handler.ssl.JdkApplicationProtocolNegotiator;
import org.apache.ratis.thirdparty.io.netty.util.internal.logging.InternalLoggerFactory;
import org.apache.ratis.thirdparty.io.netty.util.internal.logging.JdkLoggerFactory;

// Native image only. Quarkus links every class at build time (--link-at-build-time, no way to turn it
// off), so each type the Netty and gRPC that Ratis shades can name must resolve, even on paths Kates
// never takes. quarkus-netty's substitutions do this for io.netty only; these do it for Ratis' copy,
// so that no BouncyCastle, Conscrypt or Jetty ALPN/NPN jar is needed. QuorumMember picks the JDK's TLS.

/** Ratis' shaded Netty, as Quarkus' own substitutions treat io.netty: JDK logging, no probing. */
@TargetClass(className = "org.apache.ratis.thirdparty.io.netty.util.internal.logging.InternalLoggerFactory")
final class Target_InternalLoggerFactory {
    @Substitute
    private static InternalLoggerFactory newDefaultFactory(String name) {
        return JdkLoggerFactory.INSTANCE;
    }
}

/** Ratis' shaded gRPC ships without Netty's proxy handler: a peer through an HTTP proxy is not supported. */
@TargetClass(
        className = "org.apache.ratis.thirdparty.io.grpc.netty.ProtocolNegotiators$ProxyProtocolNegotiationHandler")
final class Target_ProxyProtocolNegotiationHandler {
    @Substitute
    protected void protocolNegotiationEventTriggered(
            org.apache.ratis.thirdparty.io.netty.channel.ChannelHandlerContext ctx) {
        throw new UnsupportedOperationException("Ratis' shaded gRPC has no HTTP proxy support");
    }

    @Substitute
    protected void userEventTriggered0(
            org.apache.ratis.thirdparty.io.netty.channel.ChannelHandlerContext ctx, Object evt) {
        throw new UnsupportedOperationException("Ratis' shaded gRPC has no HTTP proxy support");
    }
}

/** ALPN through the JDK's own SSLEngine, never Jetty ALPN, Conscrypt or BouncyCastle JSSE. */
@TargetClass(
        className = "org.apache.ratis.thirdparty.io.netty.handler.ssl.JdkAlpnApplicationProtocolNegotiator$AlpnWrapper")
final class Target_AlpnWrapper {
    @Substitute
    public SSLEngine wrapSslEngine(
            SSLEngine engine, ByteBufAllocator alloc, JdkApplicationProtocolNegotiator negotiator, boolean isServer) {
        return (SSLEngine) (Object) new Target_JdkAlpnSslEngine(engine, negotiator, isServer);
    }
}

@TargetClass(className = "org.apache.ratis.thirdparty.io.netty.handler.ssl.JdkAlpnSslEngine")
final class Target_JdkAlpnSslEngine {
    @Alias
    Target_JdkAlpnSslEngine(SSLEngine engine, JdkApplicationProtocolNegotiator negotiator, boolean isServer) {}
}

/** NPN needs Jetty's NPN agent, which no image has. gRPC negotiates with ALPN. */
@TargetClass(className = "org.apache.ratis.thirdparty.io.netty.handler.ssl.JdkNpnApplicationProtocolNegotiator$1")
final class Target_NpnWrapper {
    @Substitute
    Target_NpnWrapper() {}

    @Substitute
    public SSLEngine wrapSslEngine(SSLEngine engine, JdkApplicationProtocolNegotiator negotiator, boolean isServer) {
        throw new UnsupportedOperationException("NPN is not supported: use ALPN");
    }
}

/** Keys are read by Netty's own PEM reader, as PKCS#8: BouncyCastle, for other formats, is not in the image. */
@TargetClass(className = "org.apache.ratis.thirdparty.io.netty.handler.ssl.SslContext")
final class Target_SslContext {
    @Alias
    private static native PrivateKey getPrivateKeyFromByteBuffer(ByteBuf encodedKey, String keyPassword)
            throws GeneralSecurityException, IOException;

    @Substitute
    static PrivateKey toPrivateKey(File keyFile, String keyPassword, boolean tryBouncyCastle)
            throws GeneralSecurityException, IOException {
        return keyFile == null
                ? null
                : getPrivateKeyFromByteBuffer(Target_PemReader.readPrivateKey(keyFile), keyPassword);
    }

    @Substitute
    protected static PrivateKey toPrivateKey(InputStream keyInputStream, String keyPassword)
            throws GeneralSecurityException, IOException {
        return keyInputStream == null
                ? null
                : getPrivateKeyFromByteBuffer(Target_PemReader.readPrivateKey(keyInputStream), keyPassword);
    }
}

@TargetClass(className = "org.apache.ratis.thirdparty.io.netty.handler.ssl.PemReader")
final class Target_PemReader {
    @Alias
    static native ByteBuf readPrivateKey(File file) throws KeyException;

    @Alias
    static native ByteBuf readPrivateKey(InputStream in) throws KeyException;
}
