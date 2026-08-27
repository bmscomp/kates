package com.bmscomp.kates.config;

import java.util.HashMap;
import java.util.Map;
import java.util.Properties;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.smallrye.reactive.messaging.ClientCustomizer;
import org.eclipse.microprofile.config.Config;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Makes the reactive-messaging channels talk to the same broker, with the same
 * credentials, as the rest of the application.
 *
 * <p>Every Kafka client Kates builds itself goes through {@link
 * KafkaSecurityConfig} and {@code kates.kafka.bootstrap-servers}. The SmallRye
 * connector does not: it reads its own {@code bootstrap.servers}, defaulting to
 * {@code localhost:9092}, and knows nothing about SASL unless told separately.
 *
 * <p>Nothing configured it. The Helm chart sets {@code
 * KATES_KAFKA_BOOTSTRAP_SERVERS} and the SASL secret, both of which the
 * connector ignores, so in every deployment the outbox emitter and the webhook
 * consumer dialled localhost. The visible symptom was a steady stream of
 * connection warnings in the logs; the invisible one was that the transactional
 * outbox never drained, so no webhook ever fired for any test run.
 *
 * <p>{@link ClientCustomizer} is the supported way in — SmallRye hands each
 * channel's client configuration to every such bean before the client is built,
 * which lets this reuse {@code KafkaSecurityConfig} rather than restating the
 * SASL and TLS handling in properties, where the JAAS string would have to be
 * assembled by string interpolation and would be wrong for every mechanism but
 * one.
 *
 * <p>Explicit channel configuration still wins. Anything set on {@code
 * mp.messaging.*} or as a global {@code kafka.*} default is left alone, which is
 * what keeps Dev Services working: it publishes its container's address as
 * {@code kafka.bootstrap.servers}, and that is a real choice, not a default.
 */
@ApplicationScoped
public class MessagingKafkaConfigCustomizer implements ClientCustomizer<Map<String, Object>> {

    private static final Logger LOG = Logger.getLogger(MessagingKafkaConfigCustomizer.class);

    /**
     * The connector's own fallback. Treated as "nobody chose this": a real
     * deployment that genuinely runs against a local broker says so through
     * {@code kates.kafka.bootstrap-servers} as well, so overriding it is a
     * no-op there.
     */
    private static final String CONNECTOR_DEFAULT_BOOTSTRAP = "localhost:9092";

    private static final String BOOTSTRAP_SERVERS = "bootstrap.servers";
    private static final String TLS_CONFIGURATION_NAME = "tls-configuration-name";

    private final String bootstrapServers;
    private final KafkaSecurityConfig security;

    @Inject
    public MessagingKafkaConfigCustomizer(
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers,
            KafkaSecurityConfig security) {
        this.bootstrapServers = bootstrapServers;
        this.security = security;
    }

    @Override
    public Map<String, Object> customize(String channel, Config channelConfig, Map<String, Object> config) {
        Map<String, Object> customized = new HashMap<>(config);

        if (!isExplicitlyConfigured(channelConfig, BOOTSTRAP_SERVERS)) {
            customized.put(BOOTSTRAP_SERVERS, bootstrapServers);
            LOG.debugf("Channel %s pointed at %s", channel, bootstrapServers);
        }

        Properties securityProps = new Properties();
        security.apply(securityProps);
        boolean managedTls = isExplicitlyConfigured(channelConfig, TLS_CONFIGURATION_NAME);
        for (String key : securityProps.stringPropertyNames()) {
            // A channel with tls-configuration-name gets its trust and key
            // material from Quarkus's TLS registry, and Quarkus rejects the
            // combination of that and raw ssl.* keys. Adding ours would either
            // trip that check or slip past it depending on which customizer ran
            // first — so leave TLS to the registry and contribute only SASL.
            if (managedTls && key.startsWith("ssl.")) {
                continue;
            }
            // Same rule as the broker address: whatever the channel states for
            // itself is deliberate and survives.
            if (!isExplicitlyConfigured(channelConfig, key)) {
                customized.put(key, securityProps.getProperty(key));
            }
        }

        return customized;
    }

    /**
     * Whether the channel names this key itself, rather than inheriting the
     * connector's default for it.
     *
     * <p>{@code channelConfig} is the merged view of {@code
     * mp.messaging.<type>.<channel>.*}, {@code mp.messaging.connector.*} and the
     * global {@code kafka.*} defaults — everything a human or Dev Services put
     * there, and nothing the connector supplies on its own behalf. The one
     * exception is the bootstrap address, whose connector default leaks into
     * that view, hence the value check.
     */
    private boolean isExplicitlyConfigured(Config channelConfig, String key) {
        String value = channelConfig.getOptionalValue(key, String.class).orElse(null);
        if (value == null || value.isBlank()) {
            return false;
        }
        return !(BOOTSTRAP_SERVERS.equals(key) && CONNECTOR_DEFAULT_BOOTSTRAP.equals(value));
    }
}
