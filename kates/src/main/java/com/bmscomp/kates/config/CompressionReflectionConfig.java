package com.bmscomp.kates.config;

import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;

import io.quarkus.runtime.StartupEvent;
import io.quarkus.runtime.annotations.RegisterForReflection;
import org.jboss.logging.Logger;

/**
 * Keeps the lz4 codec in the native image, and proves at startup that it is
 * there.
 *
 * <p>lz4-java never names its implementations in code. {@code LZ4Factory}
 * builds {@code "net.jpountz.lz4.LZ4" + impl + "Compressor"} for impl in JNI,
 * JavaUnsafe, JavaSafe, calls {@code Class.forName} on it and reads the static
 * {@code INSTANCE} field, falling through the three in that order.
 * {@code XXHashFactory} does the same for the checksums in Kafka's LZ4 framing.
 * Static analysis sees none of it, so nothing is in the image unless it is
 * registered by name.
 *
 * <p>This was first attempted in {@code META-INF/native-image/reflect-config.json}
 * and did not take: the built image still threw
 * {@code ClassNotFoundException: net.jpountz.lz4.LZ4JavaSafeCompressor} on the
 * first record of every benchmark, from all three implementations, which is
 * what a registration that was never read looks like. Quarkus's own
 * {@code @RegisterForReflection} is applied by a build step rather than found by
 * scanning, and the SASL classes registered that way in {@link
 * KafkaSecurityConfig} have always survived into the image.
 *
 * <p>{@code fields = true} is the load-bearing part alongside the names: the
 * class being present is not enough when the factory reads {@code INSTANCE}
 * through {@code getField}.
 */
@ApplicationScoped
@RegisterForReflection(
        classNames = {
            "net.jpountz.lz4.LZ4JNICompressor",
            "net.jpountz.lz4.LZ4JNIFastDecompressor",
            "net.jpountz.lz4.LZ4JNISafeDecompressor",
            "net.jpountz.lz4.LZ4HCJNICompressor",
            "net.jpountz.lz4.LZ4JavaSafeCompressor",
            "net.jpountz.lz4.LZ4JavaSafeFastDecompressor",
            "net.jpountz.lz4.LZ4JavaSafeSafeDecompressor",
            "net.jpountz.lz4.LZ4HCJavaSafeCompressor",
            "net.jpountz.lz4.LZ4JavaUnsafeCompressor",
            "net.jpountz.lz4.LZ4JavaUnsafeFastDecompressor",
            "net.jpountz.lz4.LZ4JavaUnsafeSafeDecompressor",
            "net.jpountz.lz4.LZ4HCJavaUnsafeCompressor",
            "net.jpountz.xxhash.XXHash32JNI",
            "net.jpountz.xxhash.XXHash64JNI",
            "net.jpountz.xxhash.StreamingXXHash32JNI$Factory",
            "net.jpountz.xxhash.StreamingXXHash64JNI$Factory",
            "net.jpountz.xxhash.XXHash32JavaSafe",
            "net.jpountz.xxhash.XXHash64JavaSafe",
            "net.jpountz.xxhash.StreamingXXHash32JavaSafe$Factory",
            "net.jpountz.xxhash.StreamingXXHash64JavaSafe$Factory",
            "net.jpountz.xxhash.XXHash32JavaUnsafe",
            "net.jpountz.xxhash.XXHash64JavaUnsafe",
            "net.jpountz.xxhash.StreamingXXHash32JavaUnsafe$Factory",
            "net.jpountz.xxhash.StreamingXXHash64JavaUnsafe$Factory"
        },
        fields = true,
        methods = true)
public class CompressionReflectionConfig {

    private static final Logger LOG = Logger.getLogger(CompressionReflectionConfig.class);

    /**
     * Resolves each codec once at startup so a packaging failure is a line in
     * the log at boot, not an AssertionError twenty minutes into a benchmark
     * that then reads as a Kafka problem.
     */
    void verifyCodecs(@Observes StartupEvent event) {
        List<String> broken = new ArrayList<>();

        try {
            LOG.infof(
                    "lz4 available: %s",
                    net.jpountz.lz4.LZ4Factory.fastestInstance().toString());
        } catch (Throwable t) {
            // Throwable: lz4-java reports a missing implementation as an
            // AssertionError wrapping ClassNotFoundException.
            broken.add("lz4 (" + t + ")");
        }

        try {
            LOG.infof(
                    "xxhash available: %s",
                    net.jpountz.xxhash.XXHashFactory.fastestInstance().toString());
        } catch (Throwable t) {
            broken.add("xxhash (" + t + ")");
        }

        if (!broken.isEmpty()) {
            LOG.errorf(
                    "Compression codecs missing from this build: %s."
                            + " Every benchmark using them will fail on its first record."
                            + " See kates/docs/native-image.md.",
                    String.join(", ", broken));
        }
    }
}
