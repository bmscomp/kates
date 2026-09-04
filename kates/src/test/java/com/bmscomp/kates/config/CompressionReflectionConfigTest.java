package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.*;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;

import io.quarkus.runtime.annotations.RegisterForReflection;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * Guards the compression codecs against the native image losing them.
 *
 * <p>lz4-java does not reference its implementations directly. {@code
 * LZ4Factory} builds a class name — {@code "net.jpountz.lz4.LZ4" + impl +
 * "Compressor"} for impl in JNI, JavaSafe, JavaUnsafe — calls {@code
 * Class.forName} on it and reads its static {@code INSTANCE} field.
 * {@code XXHashFactory}, which Kafka's LZ4 framing uses for checksums, does the
 * same.
 *
 * <p>A native image contains only what was registered. When only the JNI
 * variants were, the fallback threw {@code ClassNotFoundException:
 * net.jpountz.lz4.LZ4JavaSafeCompressor} wrapped in an AssertionError, and every
 * benchmark using lz4 — the default compression — failed on native while
 * passing on the JVM.
 *
 * <p>The classes are package-private, so they cannot be listed as
 * {@code targets}; they are registered by name on {@link
 * CompressionReflectionConfig}, and nothing else validates that list. Hence this
 * test.
 */
class CompressionReflectionConfigTest {

    /** Exactly the names the two factories construct at runtime. */
    private static List<String> requiredNames() {
        List<String> names = new ArrayList<>();
        for (String impl : List.of("JNI", "JavaSafe", "JavaUnsafe")) {
            names.add("net.jpountz.lz4.LZ4" + impl + "Compressor");
            names.add("net.jpountz.lz4.LZ4HC" + impl + "Compressor");
            names.add("net.jpountz.lz4.LZ4" + impl + "FastDecompressor");
            names.add("net.jpountz.lz4.LZ4" + impl + "SafeDecompressor");
            names.add("net.jpountz.xxhash.XXHash32" + impl);
            names.add("net.jpountz.xxhash.XXHash64" + impl);
            names.add("net.jpountz.xxhash.StreamingXXHash32" + impl + "$Factory");
            names.add("net.jpountz.xxhash.StreamingXXHash64" + impl + "$Factory");
        }
        return names;
    }

    private static RegisterForReflection registration() {
        RegisterForReflection annotation = CompressionReflectionConfig.class.getAnnotation(RegisterForReflection.class);
        assertNotNull(annotation, "CompressionReflectionConfig exists to carry this annotation");
        return annotation;
    }

    @Test
    @DisplayName("every lz4 and xxhash implementation the factories can pick is registered")
    void allImplementationsAreRegistered() {
        Set<String> registered = Set.of(registration().classNames());

        List<String> missing =
                requiredNames().stream().filter(n -> !registered.contains(n)).toList();

        assertTrue(
                missing.isEmpty(),
                "These classes are resolved by name at runtime and would throw\n"
                        + "ClassNotFoundException in the NATIVE image only. Add them to\n"
                        + "CompressionReflectionConfig:\n  " + String.join("\n  ", missing));
    }

    @Test
    @DisplayName("registered names exist on the classpath — a typo fails identically at runtime")
    void registeredNamesResolve() {
        List<String> unresolvable = new ArrayList<>();
        for (String name : registration().classNames()) {
            try {
                Class.forName(name, false, Thread.currentThread().getContextClassLoader());
            } catch (ClassNotFoundException e) {
                unresolvable.add(name);
            }
        }
        assertTrue(
                unresolvable.isEmpty(), "CompressionReflectionConfig names classes that do not exist: " + unresolvable);
    }

    @Test
    @DisplayName("the INSTANCE field the factories read is exposed by the registration")
    void instanceFieldIsReachable() {
        // LZ4Factory does getField("INSTANCE"). Registering the class without
        // field access puts it in the image with its INSTANCE unreadable, which
        // fails in exactly the same way as not registering it at all.
        assertTrue(registration().fields(), "fields = true is what makes INSTANCE readable");
    }

    @Test
    @DisplayName("the codecs actually resolve here, so the startup check has something to prove")
    void factoriesResolveOnTheJvm() {
        assertNotNull(net.jpountz.lz4.LZ4Factory.fastestInstance());
        assertNotNull(net.jpountz.lz4.LZ4Factory.safeInstance());
        assertNotNull(net.jpountz.xxhash.XXHashFactory.fastestInstance());
    }
}
