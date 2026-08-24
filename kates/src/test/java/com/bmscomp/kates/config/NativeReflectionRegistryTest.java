package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.lang.reflect.Modifier;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.stream.Stream;

import io.quarkus.runtime.annotations.RegisterForReflection;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * Guards the native image against silent serialization failures.
 *
 * <p>In a native build, Jackson can only see a class's properties if that class
 * was registered for reflection. Quarkus registers what it can discover from
 * resource method signatures — but almost every endpoint here returns
 * {@code Response.ok(...)}, which is opaque, so nothing is discovered. An
 * unregistered DTO then serializes as an empty object or throws at runtime,
 * and ONLY in native mode: the JVM tests all pass, and the failure appears
 * after a release tag has already been cut.
 *
 * <p>This test walks the compiled DTO packages and fails when a payload class
 * is not registered, so adding a DTO without registering it breaks the build
 * here rather than in production.
 */
class NativeReflectionRegistryTest {

    /** Packages whose classes cross the wire as JSON or YAML. */
    private static final List<String> PAYLOAD_PACKAGES = List.of(
            "com/bmscomp/kates/domain",
            "com/bmscomp/kates/report",
            "com/bmscomp/kates/export",
            "com/bmscomp/kates/trogdor");

    /**
     * Classes that live in those packages but never get serialized: CDI beans,
     * JAX-RS resources, JPA entities and exceptions are excluded automatically;
     * anything else needing an exemption is named here with a reason.
     */
    private static final Set<String> NOT_SERIALIZED = Set.of(
            // Internal carrier between the engine and the SLA evaluator; never
            // leaves the process.
            "com.bmscomp.kates.domain.SlaMetrics");

    @Test
    @DisplayName("every DTO in a payload package is registered for reflection")
    void allPayloadTypesAreRegistered() throws Exception {
        Set<Class<?>> registered = registeredTargets();
        List<String> missing = new ArrayList<>();

        for (Class<?> candidate : payloadClasses()) {
            if (NOT_SERIALIZED.contains(candidate.getName())) {
                continue;
            }
            if (!registered.contains(candidate)) {
                missing.add(candidate.getName());
            }
        }

        assertTrue(
                missing.isEmpty(),
                "These classes are serialized but not registered for reflection, so they would\n"
                        + "fail or serialize empty in the NATIVE image only. Add them to\n"
                        + "NativeReflectionConfig (or annotate them with @RegisterForReflection):\n  "
                        + String.join("\n  ", missing));
    }

    /** Targets of every {@code @RegisterForReflection} holder in the config package. */
    private static Set<Class<?>> registeredTargets() {
        Set<Class<?>> targets = new LinkedHashSet<>();
        for (Class<?> holder : List.of(NativeReflectionConfig.class, NativePayloadReflectionConfig.class)) {
            RegisterForReflection annotation = holder.getAnnotation(RegisterForReflection.class);
            assertNotNull(annotation, holder.getSimpleName() + " must carry @RegisterForReflection");
            targets.addAll(List.of(annotation.targets()));
        }
        return targets;
    }

    /** Every concrete, serializable class in the payload packages. */
    private static List<Class<?>> payloadClasses() throws Exception {
        Path classesRoot = Path.of("target", "classes");
        assertTrue(Files.isDirectory(classesRoot), "run after compilation: " + classesRoot.toAbsolutePath());

        List<Class<?>> found = new ArrayList<>();
        for (String pkg : PAYLOAD_PACKAGES) {
            Path dir = classesRoot.resolve(pkg);
            if (!Files.isDirectory(dir)) {
                continue;
            }
            try (Stream<Path> files = Files.walk(dir)) {
                for (Path file :
                        files.filter(f -> f.toString().endsWith(".class")).toList()) {
                    String className = classesRoot
                            .relativize(file)
                            .toString()
                            .replace(java.io.File.separatorChar, '.')
                            .replaceAll("\\.class$", "");
                    Class<?> type = load(className);
                    if (type != null && isSerializedPayload(type)) {
                        found.add(type);
                    }
                }
            }
        }
        assertFalse(found.isEmpty(), "the scan found no payload classes — the heuristic is broken");
        return found;
    }

    private static Class<?> load(String className) {
        try {
            return Class.forName(className, false, Thread.currentThread().getContextClassLoader());
        } catch (Throwable ignored) {
            return null; // Not loadable in a plain unit test; nothing to assert about it.
        }
    }

    private static boolean isSerializedPayload(Class<?> type) {
        if (!Modifier.isPublic(type.getModifiers())
                || type.isInterface()
                || type.isAnnotation()
                || type.isEnum()
                || type.isAnonymousClass()
                || type.isLocalClass()
                || type.isSynthetic()
                || Modifier.isAbstract(type.getModifiers())) {
            return false;
        }
        if (Throwable.class.isAssignableFrom(type)) {
            return false;
        }
        // A nested class must be static to be instantiable by Jackson.
        if (type.getEnclosingClass() != null && !Modifier.isStatic(type.getModifiers())) {
            return false;
        }
        return !isManagedComponent(type);
    }

    /** CDI beans, JAX-RS resources and JPA entities are not JSON payloads. */
    private static boolean isManagedComponent(Class<?> type) {
        return hasAnnotation(type, "jakarta.enterprise.context.ApplicationScoped")
                || hasAnnotation(type, "jakarta.enterprise.context.RequestScoped")
                || hasAnnotation(type, "jakarta.inject.Singleton")
                || hasAnnotation(type, "jakarta.ws.rs.Path")
                || hasAnnotation(type, "jakarta.persistence.Entity")
                || hasAnnotation(type, "io.quarkus.scheduler.Scheduled");
    }

    private static boolean hasAnnotation(Class<?> type, String annotationName) {
        for (java.lang.annotation.Annotation annotation : type.getAnnotations()) {
            if (annotation.annotationType().getName().equals(annotationName)) {
                return true;
            }
        }
        return false;
    }

    @Test
    @DisplayName("playbook YAML resources are shipped in the native image")
    void playbookResourcesAreIncludedInNativeBuild() throws IOException {
        // DisruptionPlaybookCatalog loads playbooks/*.yaml from the classpath at
        // startup. Native images ship only the resources matched by
        // quarkus.native.resources.includes, so without a pattern the catalog is
        // silently EMPTY in native mode: every playbook endpoint 404s while the
        // JVM build works perfectly.
        String properties = Files.readString(Path.of("src", "main", "resources", "application.properties"));
        String includes = properties
                .lines()
                .filter(line -> line.startsWith("quarkus.native.resources.includes="))
                .findFirst()
                .orElseThrow(() -> new AssertionError("quarkus.native.resources.includes is not configured"));

        assertTrue(
                includes.contains("playbooks/"),
                "playbooks/*.yaml must be in quarkus.native.resources.includes; found: " + includes);

        try (Stream<Path> playbooks = Files.list(Path.of("src", "main", "resources", "playbooks"))) {
            assertTrue(playbooks.anyMatch(p -> p.toString().endsWith(".yaml")), "no playbook YAML files found");
        }
    }
}
