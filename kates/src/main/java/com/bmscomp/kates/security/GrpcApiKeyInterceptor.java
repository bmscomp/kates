package com.bmscomp.kates.security;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import jakarta.enterprise.context.ApplicationScoped;

import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;
import io.quarkus.grpc.GlobalInterceptor;
import org.eclipse.microprofile.config.ConfigProvider;

/**
 * gRPC counterpart of {@link ApiKeyAuthFilter}: the JAX-RS filter does not
 * cover gRPC calls, which were previously unauthenticated. Honors the same
 * config keys and accepts the key via {@code authorization: Bearer <key>}
 * or {@code x-api-key: <key>} metadata.
 */
@ApplicationScoped
@GlobalInterceptor
public class GrpcApiKeyInterceptor implements ServerInterceptor {

    private static final Metadata.Key<String> AUTHORIZATION =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> X_API_KEY =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        if (!securityEnabled()) {
            return next.startCall(call, headers);
        }

        String token = extractToken(headers);
        String apiKey = configuredKey();
        if (token == null || apiKey.isBlank() || !constantTimeEquals(apiKey, token)) {
            call.close(
                    Status.UNAUTHENTICATED.withDescription(
                            "Missing or invalid API key. Provide it via 'authorization: Bearer <key>'"
                                    + " or 'x-api-key: <key>' metadata."),
                    new Metadata());
            return new ServerCall.Listener<>() {};
        }
        return next.startCall(call, headers);
    }

    private String extractToken(Metadata headers) {
        String authHeader = headers.get(AUTHORIZATION);
        if (authHeader != null && authHeader.startsWith("Bearer ")) {
            return authHeader.substring(7).trim();
        }
        String apiKeyHeader = headers.get(X_API_KEY);
        if (apiKeyHeader != null && !apiKeyHeader.isBlank()) {
            return apiKeyHeader.trim();
        }
        return null;
    }

    private boolean securityEnabled() {
        return ConfigProvider.getConfig()
                .getOptionalValue("kates.api.security-enabled", Boolean.class)
                .orElse(true);
    }

    private String configuredKey() {
        return ConfigProvider.getConfig()
                .getOptionalValue("kates.api.key", String.class)
                .orElse("");
    }

    private static boolean constantTimeEquals(String expected, String provided) {
        return MessageDigest.isEqual(
                expected.getBytes(StandardCharsets.UTF_8), provided.getBytes(StandardCharsets.UTF_8));
    }
}
