package com.bmscomp.kates.config;

import java.util.Optional;
import java.util.Properties;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.apache.kafka.clients.CommonClientConfigs;
import org.apache.kafka.common.config.SaslConfigs;
import org.apache.kafka.common.config.SslConfigs;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import io.quarkus.runtime.annotations.RegisterForReflection;

/**
 * Centralized Kafka security configuration. Supports all major auth modes:
 * PLAINTEXT, SASL_PLAINTEXT (SCRAM/PLAIN), SSL (mTLS), SASL_SSL, and OAUTHBEARER.
 *
 * All Kafka client-building services inject this bean and call {@link #apply(Properties)}
 * instead of duplicating auth setup.
 */
@ApplicationScoped
@RegisterForReflection(classNames = {
    "org.apache.kafka.common.security.authenticator.SaslClientCallbackHandler",
    "org.apache.kafka.common.security.authenticator.AbstractLogin$DefaultLoginCallbackHandler",
    "org.apache.kafka.common.security.authenticator.DefaultLogin",
    "org.apache.kafka.common.security.scram.ScramLoginModule",
    "org.apache.kafka.common.security.scram.internals.ScramSaslClient$ScramSaslClientFactory",
    "org.apache.kafka.common.security.scram.internals.ScramSaslClient",
    "org.apache.kafka.common.security.scram.internals.ScramSaslServer",
    "org.apache.kafka.common.security.scram.internals.ScramSaslServer$ScramSaslServerFactory",
    "org.apache.kafka.common.security.plain.PlainLoginModule",
    "org.apache.kafka.common.security.plain.internals.PlainSaslClient$PlainSaslClientFactory",
    "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule",
    "org.apache.kafka.common.security.oauthbearer.internals.OAuthBearerSaslClient$OAuthBearerSaslClientFactory"
})
public class KafkaSecurityConfig {

    private static final Logger LOG = Logger.getLogger(KafkaSecurityConfig.class);

    private final String securityProtocol;
    private final Optional<String> saslMechanism;
    private final Optional<String> saslUsername;
    private final Optional<String> saslPassword;
    private final Optional<String> oauthTokenEndpointUrl;
    private final Optional<String> oauthClientId;
    private final Optional<String> oauthClientSecret;
    private final Optional<String> sslTruststoreLocation;
    private final Optional<String> sslTruststorePassword;
    private final Optional<String> sslKeystoreLocation;
    private final Optional<String> sslKeystorePassword;

    @Inject
    public KafkaSecurityConfig(
            @ConfigProperty(name = "kates.kafka.security.protocol", defaultValue = "PLAINTEXT")
            String securityProtocol,
            @ConfigProperty(name = "kates.kafka.sasl.mechanism")
            Optional<String> saslMechanism,
            @ConfigProperty(name = "kates.kafka.sasl.username")
            Optional<String> saslUsername,
            @ConfigProperty(name = "kates.kafka.sasl.password")
            Optional<String> saslPassword,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.token-endpoint-url")
            Optional<String> oauthTokenEndpointUrl,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.client-id")
            Optional<String> oauthClientId,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.client-secret")
            Optional<String> oauthClientSecret,
            @ConfigProperty(name = "kates.kafka.ssl.truststore.location")
            Optional<String> sslTruststoreLocation,
            @ConfigProperty(name = "kates.kafka.ssl.truststore.password")
            Optional<String> sslTruststorePassword,
            @ConfigProperty(name = "kates.kafka.ssl.keystore.location")
            Optional<String> sslKeystoreLocation,
            @ConfigProperty(name = "kates.kafka.ssl.keystore.password")
            Optional<String> sslKeystorePassword) {
        this.securityProtocol = securityProtocol;
        this.saslMechanism = saslMechanism;
        this.saslUsername = saslUsername;
        this.saslPassword = saslPassword;
        this.oauthTokenEndpointUrl = oauthTokenEndpointUrl;
        this.oauthClientId = oauthClientId;
        this.oauthClientSecret = oauthClientSecret;
        this.sslTruststoreLocation = sslTruststoreLocation;
        this.sslTruststorePassword = sslTruststorePassword;
        this.sslKeystoreLocation = sslKeystoreLocation;
        this.sslKeystorePassword = sslKeystorePassword;
    }

    /**
     * Applies security properties to a Kafka client Properties object.
     * Call this from any service that creates AdminClient, KafkaProducer, or KafkaConsumer.
     */
    public void apply(Properties props) {
        if ("PLAINTEXT".equals(securityProtocol)) {
            return;
        }

        props.put(CommonClientConfigs.SECURITY_PROTOCOL_CONFIG, securityProtocol);

        if (securityProtocol.startsWith("SASL")) {
            applySasl(props);
        }

        if (securityProtocol.contains("SSL")) {
            applySsl(props);
        }
    }

    private void applySasl(Properties props) {
        String mechanism = saslMechanism.orElse("SCRAM-SHA-512");
        props.put(SaslConfigs.SASL_MECHANISM, mechanism);

        switch (mechanism) {
            case "SCRAM-SHA-512", "SCRAM-SHA-256" -> {
                if (saslUsername.isPresent() && saslPassword.isPresent()) {
                    props.put(SaslConfigs.SASL_JAAS_CONFIG,
                            "org.apache.kafka.common.security.scram.ScramLoginModule required "
                            + "username=\"" + saslUsername.get() + "\" "
                            + "password=\"" + saslPassword.get() + "\";");
                    LOG.infof("SASL/%s enabled for user: %s", mechanism, saslUsername.get());
                }
            }
            case "PLAIN" -> {
                if (saslUsername.isPresent() && saslPassword.isPresent()) {
                    props.put(SaslConfigs.SASL_JAAS_CONFIG,
                            "org.apache.kafka.common.security.plain.PlainLoginModule required "
                            + "username=\"" + saslUsername.get() + "\" "
                            + "password=\"" + saslPassword.get() + "\";");
                    LOG.infof("SASL/PLAIN enabled for user: %s", saslUsername.get());
                }
            }
            case "OAUTHBEARER" -> {
                StringBuilder jaas = new StringBuilder(
                        "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required");
                oauthTokenEndpointUrl.ifPresent(url ->
                        jaas.append(" oauth.token.endpoint.uri=\"").append(url).append("\""));
                oauthClientId.ifPresent(id ->
                        jaas.append(" oauth.client.id=\"").append(id).append("\""));
                oauthClientSecret.ifPresent(secret ->
                        jaas.append(" oauth.client.secret=\"").append(secret).append("\""));
                jaas.append(";");
                props.put(SaslConfigs.SASL_JAAS_CONFIG, jaas.toString());
                props.put(SaslConfigs.SASL_LOGIN_CALLBACK_HANDLER_CLASS,
                        "org.apache.kafka.common.security.oauthbearer.secured.OAuthBearerLoginCallbackHandler");
                LOG.info("SASL/OAUTHBEARER enabled");
            }
            default -> LOG.warnf("Unknown SASL mechanism: %s", mechanism);
        }
    }

    private void applySsl(Properties props) {
        sslTruststoreLocation.ifPresent(loc -> {
            props.put(SslConfigs.SSL_TRUSTSTORE_LOCATION_CONFIG, loc);
            sslTruststorePassword.ifPresent(pwd ->
                    props.put(SslConfigs.SSL_TRUSTSTORE_PASSWORD_CONFIG, pwd));
            LOG.info("SSL truststore configured");
        });

        sslKeystoreLocation.ifPresent(loc -> {
            props.put(SslConfigs.SSL_KEYSTORE_LOCATION_CONFIG, loc);
            sslKeystorePassword.ifPresent(pwd ->
                    props.put(SslConfigs.SSL_KEYSTORE_PASSWORD_CONFIG, pwd));
            LOG.info("SSL keystore configured (mTLS)");
        });
    }

    public String getSecurityProtocol() {
        return securityProtocol;
    }
}
