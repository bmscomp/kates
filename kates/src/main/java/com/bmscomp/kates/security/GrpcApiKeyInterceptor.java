package com.bmscomp.kates.security;

import java.util.Map;
import java.util.Optional;
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
 * {@code x-api-key: <key>} metadata, refuses a call whose key no principal
 * holds ({@link ApiKeys}) with UNAUTHENTICATED, and one whose principal lacks
 * the method's scope ({@link #METHOD_SCOPES}) with PERMISSION_DENIED, as the
 * REST endpoints answer 403.
 */
@ApplicationScoped
@GlobalInterceptor
public class GrpcApiKeyInterceptor implements ServerInterceptor {

    private static final Metadata.Key<String> AUTHORIZATION =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> X_API_KEY =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    /**
     * The scope each Kates RPC needs, by full method name, matching its REST
     * counterpart. A Kates method missing here needs admin, so a new one is
     * refused to every other key until it is listed; methods of other
     * services (gRPC's own health check) need read.
     */
    static final Map<String, String> METHOD_SCOPES = Map.ofEntries(
            Map.entry("kates.TestService/CreateTest", Scopes.TEST_RUN),
            Map.entry("kates.TestService/GetTest", Scopes.READ),
            Map.entry("kates.TestService/ListTests", Scopes.READ),
            Map.entry("kates.TestService/CancelTest", Scopes.TEST_RUN),
            Map.entry("kates.TestService/DeleteTest", Scopes.ADMIN),
            Map.entry("kates.ClusterService/GetClusterInfo", Scopes.READ),
            Map.entry("kates.ClusterService/GetClusterTopology", Scopes.READ),
            Map.entry("kates.ClusterService/ListTopics", Scopes.READ),
            Map.entry("kates.ClusterService/GetTopicDetail", Scopes.READ),
            Map.entry("kates.ClusterService/ListConsumerGroups", Scopes.READ),
            Map.entry("kates.HealthService/Check", Scopes.READ));

    static String scopeOf(String fullMethodName) {
        String scope = METHOD_SCOPES.get(fullMethodName);
        if (scope != null) {
            return scope;
        }
        return fullMethodName.startsWith("kates.") ? Scopes.ADMIN : Scopes.READ;
    }

    @Inject
    ApiKeys keys;

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        if (!keys.securityEnabled()) {
            return next.startCall(call, headers);
        }

        String key = ApiKeys.presented(headers.get(AUTHORIZATION), headers.get(X_API_KEY));
        Optional<KatesPrincipal> principal = keys.resolve(key);
        if (principal.isEmpty()) {
            call.close(
                    Status.UNAUTHENTICATED.withDescription(
                            "Missing or invalid API key. Provide it via 'authorization: Bearer <key>'"
                                    + " or 'x-api-key: <key>' metadata."),
                    new Metadata());
            return new ServerCall.Listener<>() {};
        }
        String scope = scopeOf(call.getMethodDescriptor().getFullMethodName());
        if (!principal.get().scopes().contains(scope)) {
            call.close(
                    Status.PERMISSION_DENIED.withDescription("The API key's principal "
                            + principal.get().name() + " lacks the " + scope + " scope this method needs."),
                    new Metadata());
            return new ServerCall.Listener<>() {};
        }
        return next.startCall(call, headers);
    }
}
