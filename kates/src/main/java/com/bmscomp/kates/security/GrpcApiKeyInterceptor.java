package com.bmscomp.kates.security;

import java.util.Map;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicReference;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.grpc.ForwardingServerCall;
import io.grpc.ForwardingServerCallListener;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;
import io.quarkus.grpc.GlobalInterceptor;

import com.bmscomp.kates.audit.Actor;
import com.bmscomp.kates.service.AuditService;

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

    /** The audit action of each RPC that changes something; the others leave no row. */
    static final Map<String, String> AUDITED = Map.of(
            "kates.TestService/CreateTest", "CREATE",
            "kates.TestService/CancelTest", "CANCEL",
            "kates.TestService/DeleteTest", "DELETE");

    @Inject
    ApiKeys keys;

    @Inject
    AuditService auditService;

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
        String method = call.getMethodDescriptor().getFullMethodName();
        String scope = scopeOf(method);
        String action = AUDITED.get(method);
        if (!principal.get().scopes().contains(scope)) {
            if (action != null) {
                audit(action, method, Status.Code.PERMISSION_DENIED, principal.get());
            }
            call.close(
                    Status.PERMISSION_DENIED.withDescription("The API key's principal "
                            + principal.get().name() + " lacks the " + scope + " scope this method needs."),
                    new Metadata());
            return new ServerCall.Listener<>() {};
        }
        if (action == null) {
            return next.startCall(call, headers);
        }
        return audited(call, headers, next, action, principal.get());
    }

    /**
     * Starts a call to an RPC that changes something so that it leaves an
     * audit row when it ends, as REST calls do: the principal, the run it
     * acted on (the request's id, or the reply's for a run it created) and
     * the status it ended with.
     */
    private <ReqT, RespT> ServerCall.Listener<ReqT> audited(
            ServerCall<ReqT, RespT> call,
            Metadata headers,
            ServerCallHandler<ReqT, RespT> next,
            String action,
            KatesPrincipal principal) {
        String method = call.getMethodDescriptor().getFullMethodName();
        AtomicReference<String> target = new AtomicReference<>(method);
        ServerCall<ReqT, RespT> watched = new ForwardingServerCall.SimpleForwardingServerCall<>(call) {
            @Override
            public void sendMessage(RespT message) {
                String id = idOf(message);
                if (id != null) {
                    target.set(id);
                }
                super.sendMessage(message);
            }

            @Override
            public void close(Status status, Metadata trailers) {
                audit(action, target.get(), status.getCode(), principal);
                super.close(status, trailers);
            }
        };
        return new ForwardingServerCallListener.SimpleForwardingServerCallListener<>(next.startCall(watched, headers)) {
            @Override
            public void onMessage(ReqT message) {
                String id = idOf(message);
                if (id != null) {
                    target.set(id);
                }
                super.onMessage(message);
            }
        };
    }

    private void audit(String action, String target, Status.Code code, KatesPrincipal principal) {
        AuditService.blockingSafe(
                () -> auditService.record(action, "test", target, "gRPC " + code, Actor.of(principal)));
    }

    /** The id field of a protobuf message, or null when it has none or it is empty. */
    static String idOf(Object message) {
        if (message instanceof com.google.protobuf.Message m) {
            var field = m.getDescriptorForType().findFieldByName("id");
            if (field != null && m.getField(field) instanceof String id && !id.isEmpty()) {
                return id;
            }
        }
        return null;
    }
}
