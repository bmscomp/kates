package com.bmscomp.kates.config;

import io.quarkus.runtime.annotations.RegisterForReflection;

/**
 * Registers the JDK's default JAAS configuration provider so a native binary
 * can construct a Kafka client at all.
 *
 * <p>Kafka builds a channel for EVERY client — PLAINTEXT included, before any
 * security is configured — and {@code ChannelBuilders.create} calls
 * {@code JaasContext.defaultContext()}, which calls
 * {@code javax.security.auth.login.Configuration.getConfiguration()}, which
 * {@code Class.forName}s {@code sun.security.provider.ConfigFile}. On the JVM
 * that class is simply there. In a native image nothing referenced it
 * statically, so it was not in the image, and the lookup threw
 * {@code ClassNotFoundException} on the first consumer construction.
 *
 * <p>Reactive messaging turns that into a {@code DeploymentException} during
 * {@code StartupEvent}, so the symptom was not a Kafka error at all: the binary
 * built cleanly, then refused to boot, against a broker it had not yet tried to
 * reach. Registering the class for reflection is what pulls it into the image.
 *
 * <p>Class name, not a class literal: {@code sun.security.provider} is not
 * exported, so it cannot be referenced at compile time.
 */
@RegisterForReflection(
        classNames = {"sun.security.provider.ConfigFile"},
        registerFullHierarchy = true)
public class JaasReflectionConfig {}
