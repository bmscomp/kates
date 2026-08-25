package com.bmscomp.kates.config;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
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
 * <p>A native image contains only what was registered, and only the JNI
 * variants were. So the moment the JNI path was unavailable, the fallback threw
 * {@code ClassNotFoundException: net.jpountz.lz4.LZ4JavaSafeCompressor} wrapped
 * in an AssertionError, and every benchmark using lz4 — the default compression
 * — failed on native while passing on the JVM.
 *
 * <p>The classes are package-private, so they cannot be listed in
 * {@code @RegisterForReflection}; they live in reflect-config.json, which
 * nothing else validates. Hence this test.
 */
class CompressionReflectionConfigTest {

    private static final Path CONFIG =
            Path.of("src", "main", "resources", "META-INF", "native-image", "reflect-config.json");

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

    private static Set<String> registered() throws Exception {
        JsonNode root = new ObjectMapper().readTree(Files.readString(CONFIG));
        return root.findValues("name").stream().map(JsonNode::asText).collect(Collectors.toSet());
    }

    @Test
    @DisplayName("every lz4 and xxhash implementation the factories can pick is registered")
    void allImplementationsAreRegistered() throws Exception {
        Set<String> registered = registered();

        List<String> missing =
                requiredNames().stream().filter(n -> !registered.contains(n)).toList();

        assertTrue(
                missing.isEmpty(),
                "These classes are resolved by name at runtime and would throw\n"
                        + "ClassNotFoundException in the NATIVE image only. Add them to\n"
                        + CONFIG + ":\n  " + String.join("\n  ", missing));
    }

    @Test
    @DisplayName("registered names exist on the classpath — a typo fails identically at runtime")
    void registeredNamesResolve() throws Exception {
        List<String> unresolvable = new ArrayList<>();
        for (String name : registered()) {
            if (!name.startsWith("net.jpountz") && !name.startsWith("org.xerial") && !name.startsWith("com.github")) {
                continue; // application classes are covered by NativeReflectionRegistryTest
            }
            try {
                Class.forName(name, false, Thread.currentThread().getContextClassLoader());
            } catch (ClassNotFoundException e) {
                unresolvable.add(name);
            }
        }
        assertTrue(unresolvable.isEmpty(), "reflect-config.json names classes that do not exist: " + unresolvable);
    }

    @Test
    @DisplayName("the INSTANCE field the factories read is exposed by the registration")
    void instanceFieldIsReachable() throws Exception {
        JsonNode root = new ObjectMapper().readTree(Files.readString(CONFIG));
        List<String> withoutFields = new ArrayList<>();
        for (JsonNode entry : root) {
            String name = entry.path("name").asText();
            if (!name.startsWith("net.jpountz")) {
                continue;
            }
            // LZ4Factory does getField("INSTANCE"); without field access the
            // class is in the image but its INSTANCE cannot be read.
            if (!entry.path("allDeclaredFields").asBoolean(false)
                    && !entry.path("allPublicFields").asBoolean(false)) {
                withoutFields.add(name);
            }
        }
        assertTrue(
                withoutFields.isEmpty(),
                "these entries need allDeclaredFields for the INSTANCE lookup: " + withoutFields);
    }
}
