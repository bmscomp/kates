package com.bmscomp.kates.config;

import java.io.File;

import io.quarkus.runtime.Startup;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import org.jboss.logging.Logger;

/**
 * Explicitly loads JNI native libraries (zstd, lz4, snappy) at application startup.
 * In GraalVM native images, the standard JAR resource extraction used by zstd-jni
 * and similar libraries fails. This loader finds pre-extracted .so files in /app/lib/
 * and loads them via System.load() before any Kafka producer/consumer uses compression.
 */
@ApplicationScoped
@Startup
public class NativeLibraryLoader {

    private static final Logger LOG = Logger.getLogger(NativeLibraryLoader.class);
    private static final String LIB_DIR = "/app/lib";

    @PostConstruct
    void loadNativeLibraries() {
        File libDir = new File(LIB_DIR);
        if (!libDir.isDirectory()) {
            LOG.infof("Native library directory %s not found — skipping (JVM mode or dev)", LIB_DIR);
            return;
        }

        File[] soFiles = libDir.listFiles((dir, name) -> name.endsWith(".so"));
        if (soFiles == null || soFiles.length == 0) {
            LOG.warnf("No .so files found in %s", LIB_DIR);
            return;
        }

        for (File so : soFiles) {
            try {
                System.load(so.getAbsolutePath());
                LOG.infof("Loaded native library: %s", so.getName());
            } catch (UnsatisfiedLinkError e) {
                LOG.warnf("Failed to load %s: %s", so.getName(), e.getMessage());
            }
        }
    }
}
