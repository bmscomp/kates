package com.bmscomp.kates.security;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;
import io.quarkus.grpc.GlobalInterceptor;

/**
 * gRPC counterpart of {@link ApiKeyAuthenticationMechanism}, which covers HTTP
 * routes only: gRPC calls were unauthenticated before this interceptor.
 * Accepts the key via {@code authorization: Bearer <key>} or
 * {@code x-api-key: <key>} metadata, and refuses a call whose key no
 * principal holds ({@link ApiKeys}).
 */
@ApplicationScoped
@GlobalInterceptor
public class GrpcApiKeyInterceptor implements ServerInterceptor {

    private static final Metadata.Key<String> AUTHORIZATION =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> X_API_KEY =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    @Inject
    ApiKeys keys;

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        if (!keys.securityEnabled()) {
            return next.startCall(call, headers);
        }

        String key = ApiKeys.presented(headers.get(AUTHORIZATION), headers.get(X_API_KEY));
        if (keys.resolve(key).isEmpty()) {
            call.close(
                    Status.UNAUTHENTICATED.withDescription(
                            "Missing or invalid API key. Provide it via 'authorization: Bearer <key>'"
                                    + " or 'x-api-key: <key>' metadata."),
                    new Metadata());
            return new ServerCall.Listener<>() {};
        }
        return next.startCall(call, headers);
    }
}
