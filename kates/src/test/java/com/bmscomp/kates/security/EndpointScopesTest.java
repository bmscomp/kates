package com.bmscomp.kates.security;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.lang.annotation.Annotation;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.net.URISyntaxException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.stream.Stream;
import jakarta.annotation.security.DenyAll;
import jakarta.annotation.security.PermitAll;
import jakarta.annotation.security.RolesAllowed;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PATCH;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.PUT;

import io.grpc.ServiceDescriptor;
import io.quarkus.security.Authenticated;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.api.HealthResource;
import com.bmscomp.kates.grpc.proto.ClusterServiceGrpc;
import com.bmscomp.kates.grpc.proto.HealthServiceGrpc;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;

/**
 * Every REST endpoint names what it needs, and none that changes anything
 * needs only read. quarkus.security.jaxrs.deny-unannotated-endpoints refuses
 * an endpoint that names nothing at run time; this test says so at build time,
 * with the endpoint's name. The same for gRPC: every Kates RPC has a scope.
 */
class EndpointScopesTest {

    private static final Set<Class<? extends Annotation>> VERBS =
            Set.of(GET.class, POST.class, PUT.class, DELETE.class, PATCH.class);
    private static final Set<Class<? extends Annotation>> WRITES =
            Set.of(POST.class, PUT.class, DELETE.class, PATCH.class);

    /** Writes that read: the cost estimate computes, and the disruption POST checks chaos:run in code. */
    private static final Set<String> WRITES_THAT_READ =
            Set.of("CostResource.estimate", "DisruptionResource.executeDisruption");

    private static List<Class<?>> resources() throws IOException, URISyntaxException {
        Path root = Path.of(HealthResource.class
                .getProtectionDomain()
                .getCodeSource()
                .getLocation()
                .toURI());
        List<Class<?>> out = new ArrayList<>();
        try (Stream<Path> files = Files.walk(root)) {
            for (Path p : files.filter(f -> f.toString().endsWith(".class")).toList()) {
                String name = root.relativize(p).toString().replace('/', '.').replaceAll("\\.class$", "");
                if (!name.startsWith("com.bmscomp.kates.") || name.contains("$")) {
                    continue;
                }
                Class<?> c;
                try {
                    c = Class.forName(name, false, EndpointScopesTest.class.getClassLoader());
                } catch (ClassNotFoundException | LinkageError e) {
                    continue;
                }
                if (!c.isInterface() && c.isAnnotationPresent(jakarta.ws.rs.Path.class)) {
                    out.add(c);
                }
            }
        }
        return out;
    }

    private static boolean hasVerb(Method m, Set<Class<? extends Annotation>> verbs) {
        return verbs.stream().anyMatch(m::isAnnotationPresent);
    }

    private static boolean names(java.lang.reflect.AnnotatedElement e) {
        return e.isAnnotationPresent(RolesAllowed.class)
                || e.isAnnotationPresent(PermitAll.class)
                || e.isAnnotationPresent(DenyAll.class)
                || e.isAnnotationPresent(Authenticated.class);
    }

    @Test
    void everyEndpointNamesItsScopeAndNoWriteNeedsOnlyRead() throws Exception {
        List<Class<?>> resources = resources();
        assertTrue(resources.size() >= 20, "found only " + resources.size() + " resource classes");
        List<String> problems = new ArrayList<>();
        int endpoints = 0;
        for (Class<?> c : resources) {
            for (Method m : c.getDeclaredMethods()) {
                if (!Modifier.isPublic(m.getModifiers()) || !hasVerb(m, VERBS)) {
                    continue;
                }
                endpoints++;
                String name = c.getSimpleName() + "." + m.getName();
                java.lang.reflect.AnnotatedElement source = names(m) ? m : c;
                if (!names(source)) {
                    problems.add(name + " names no scope (@RolesAllowed, @PermitAll or @Authenticated)");
                    continue;
                }
                if (hasVerb(m, WRITES) && !WRITES_THAT_READ.contains(name)) {
                    RolesAllowed roles = source.getAnnotation(RolesAllowed.class);
                    if (roles == null || Set.of(roles.value()).contains(Scopes.READ)) {
                        problems.add(name + " changes something but needs only read");
                    }
                }
            }
        }
        assertTrue(endpoints > 100, "found only " + endpoints + " endpoints");
        assertEquals(List.of(), problems);
    }

    /**
     * A resource is a CDI bean, and a class-level @RolesAllowed holds every
     * one of its methods to the role, not only its endpoints: an observer or a
     * scheduled method then runs with no request identity and is refused, as
     * EventStreamResource's lifecycle observer was, so the event stream sent
     * nothing. Each such method says @PermitAll.
     */
    @Test
    void noMethodBesidesTheEndpointsInheritsTheClassScope() throws Exception {
        List<String> problems = new ArrayList<>();
        for (Class<?> c : resources()) {
            if (!names(c)) {
                continue;
            }
            for (Method m : c.getDeclaredMethods()) {
                int mod = m.getModifiers();
                if (Modifier.isPrivate(mod) || Modifier.isStatic(mod) || m.isSynthetic() || hasVerb(m, VERBS)) {
                    continue;
                }
                if (!names(m)) {
                    problems.add(c.getSimpleName() + "." + m.getName()
                            + " is not an endpoint but inherits the class's scope; mark it @PermitAll");
                }
            }
        }
        assertEquals(List.of(), problems);
    }

    @Test
    void everyKatesRpcHasAScope() {
        List<String> missing = new ArrayList<>();
        for (ServiceDescriptor service : List.of(
                TestServiceGrpc.getServiceDescriptor(),
                ClusterServiceGrpc.getServiceDescriptor(),
                HealthServiceGrpc.getServiceDescriptor())) {
            for (var method : service.getMethods()) {
                if (!GrpcApiKeyInterceptor.METHOD_SCOPES.containsKey(method.getFullMethodName())) {
                    missing.add(method.getFullMethodName());
                }
            }
        }
        assertEquals(List.of(), missing);
        assertEquals(Scopes.ADMIN, GrpcApiKeyInterceptor.scopeOf("kates.TestService/SomethingNew"));
        assertEquals(Scopes.READ, GrpcApiKeyInterceptor.scopeOf("grpc.health.v1.Health/Check"));
    }
}
