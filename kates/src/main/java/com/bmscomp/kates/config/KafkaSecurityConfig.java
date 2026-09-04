package com.bmscomp.kates.config;

import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.Properties;
import java.util.Set;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.runtime.annotations.RegisterForReflection;
import org.apache.kafka.clients.CommonClientConfigs;
import org.apache.kafka.common.config.SaslConfigs;
import org.apache.kafka.common.config.SslConfigs;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Centralized Kafka security configuration. Supports all major auth modes:
 * PLAINTEXT, SASL_PLAINTEXT (SCRAM/PLAIN), SSL (mTLS), SASL_SSL, and OAUTHBEARER.
 *
 * All Kafka client-building services inject this bean and call {@link #apply(Properties)}
 * instead of duplicating auth setup.
 */
@ApplicationScoped
@RegisterForReflection(
        classNames = {
            "org.apache.kafka.common.security.authenticator.SaslClientCallbackHandler",
            "org.apache.kafka.common.security.authenticator.AbstractLogin$DefaultLoginCallbackHandler",
            "org.apache.kafka.common.security.authenticator.DefaultLogin",
            "org.apache.kafka.common.security.scram.ScramLoginModule",
            "org.apache.kafka.common.security.scram.internals.ScramSaslClient$ScramSaslClientFactory",
            "org.apache.kafka.common.security.scram.internals.ScramSaslClient",
            "org.apache.kafka.common.security.scram.internals.ScramSaslServer",
            "org.apache.kafka.common.security.scram.internals.ScramSaslServer$ScramSaslServerFactory",
            "org.apache.kafka.common.security.plain.PlainLoginModule",
            "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule",
            "org.apache.kafka.common.security.oauthbearer.internals.OAuthBearerSaslClient$OAuthBearerSaslClientFactory"
        })
public class KafkaSecurityConfig {

    private static final Logger LOG = Logger.getLogger(KafkaSecurityConfig.class);

    /** SASL mechanisms whose login module needs a username and password. */
    private static final Set<String> CREDENTIAL_MECHANISMS = Set.of("SCRAM-SHA-512", "SCRAM-SHA-256", "PLAIN");

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
            @ConfigProperty(name = "kates.kafka.security.protocol", defaultValue = "PLAINTEXT") String securityProtocol,
            @ConfigProperty(name = "kates.kafka.sasl.mechanism") Optional<String> saslMechanism,
            @ConfigProperty(name = "kates.kafka.sasl.username") Optional<String> saslUsername,
            @ConfigProperty(name = "kates.kafka.sasl.password") Optional<String> saslPassword,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.token-endpoint-url")
                    Optional<String> oauthTokenEndpointUrl,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.client-id") Optional<String> oauthClientId,
            @ConfigProperty(name = "kates.kafka.sasl.oauthbearer.client-secret") Optional<String> oauthClientSecret,
            @ConfigProperty(name = "kates.kafka.ssl.truststore.location") Optional<String> sslTruststoreLocation,
            @ConfigProperty(name = "kates.kafka.ssl.truststore.password") Optional<String> sslTruststorePassword,
            @ConfigProperty(name = "kates.kafka.ssl.keystore.location") Optional<String> sslKeystoreLocation,
            @ConfigProperty(name = "kates.kafka.ssl.keystore.password") Optional<String> sslKeystorePassword) {
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
        validate();
    }

    /**
     * Refuses a SASL protocol that has no credentials to go with it.
     *
     * <p>This used to be silent: {@link #applySasl} set {@code security.protocol}
     * and {@code sasl.mechanism} and then skipped {@code sasl.jaas.config}
     * whenever the username or password was absent. Kafka does not treat a
     * missing JAAS entry as "no authentication" — it falls back to the JVM-wide
     * JAAS file, finds nothing there, and every client constructor dies with
     * {@code Could not find a 'KafkaClient' entry in the JAAS configuration.
     * System property 'java.security.auth.login.config' is not set}. That
     * message points at a JVM security file nobody in this project has ever
     * touched, rather than at the unset {@code KATES_KAFKA_SASL_PASSWORD} that
     * actually caused it.
     *
     * <p>It also fails late and loudly: the reactive-messaging channels build
     * their clients from a StartupEvent observer, so an unset secret takes the
     * whole application down at boot with that same misdirecting stack trace.
     *
     * <p>Degrading to PLAINTEXT instead would be worse — a deployment whose
     * secret failed to mount would quietly start talking to the broker
     * unauthenticated. So: fail, and say which property is missing.
     */
    private void validate() {
        if (!securityProtocol.startsWith("SASL")) {
            return;
        }
        String mechanism = saslMechanism.orElse("SCRAM-SHA-512");
        if (!CREDENTIAL_MECHANISMS.contains(mechanism)) {
            return;
        }
        if (saslUsername.isPresent() && saslPassword.isPresent()) {
            return;
        }
        List<String> missing = new ArrayList<>(2);
        if (saslUsername.isEmpty()) {
            missing.add("kates.kafka.sasl.username (KATES_KAFKA_SASL_USERNAME)");
        }
        if (saslPassword.isEmpty()) {
            missing.add("kates.kafka.sasl.password (KATES_KAFKA_SASL_PASSWORD)");
        }
        throw new IllegalStateException("kates.kafka.security.protocol=" + securityProtocol
                + " with kates.kafka.sasl.mechanism=" + mechanism + " requires credentials, but "
                + String.join(" and ", missing) + " is not set. Set it, or set "
                + "kates.kafka.security.protocol=PLAINTEXT (KATES_KAFKA_SECURITY_PROTOCOL=PLAINTEXT) "
                + "for a broker without authentication.");
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

        // The credential mechanisms are unconditional here on purpose: the
        // constructor has already refused a SASL protocol without credentials,
        // so there is no case left in which this emits security.protocol and
        // sasl.mechanism but no sasl.jaas.config. That combination is what sent
        // Kafka to the JVM's JAAS file and killed boot with an error naming a
        // file this project does not use.
        switch (mechanism) {
            case "SCRAM-SHA-512", "SCRAM-SHA-256" -> {
                props.put(
                        SaslConfigs.SASL_JAAS_CONFIG,
                        "org.apache.kafka.common.security.scram.ScramLoginModule required "
                                + "username=" + jaasQuote(saslUsername.orElseThrow()) + " "
                                + "password=" + jaasQuote(saslPassword.orElseThrow()) + ";");
                LOG.infof("SASL/%s enabled for user: %s", mechanism, saslUsername.orElseThrow());
            }
            case "PLAIN" -> {
                props.put(
                        SaslConfigs.SASL_JAAS_CONFIG,
                        "org.apache.kafka.common.security.plain.PlainLoginModule required "
                                + "username=" + jaasQuote(saslUsername.orElseThrow()) + " "
                                + "password=" + jaasQuote(saslPassword.orElseThrow()) + ";");
                LOG.infof("SASL/PLAIN enabled for user: %s", saslUsername.orElseThrow());
            }
            case "OAUTHBEARER" -> {
                StringBuilder jaas = new StringBuilder(
                        "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required");
                oauthTokenEndpointUrl.ifPresent(
                        url -> jaas.append(" oauth.token.endpoint.uri=").append(jaasQuote(url)));
                oauthClientId.ifPresent(id -> jaas.append(" oauth.client.id=").append(jaasQuote(id)));
                oauthClientSecret.ifPresent(
                        secret -> jaas.append(" oauth.client.secret=").append(jaasQuote(secret)));
                jaas.append(";");
                props.put(SaslConfigs.SASL_JAAS_CONFIG, jaas.toString());
                props.put(
                        SaslConfigs.SASL_LOGIN_CALLBACK_HANDLER_CLASS,
                        "org.apache.kafka.common.security.oauthbearer.secured.OAuthBearerLoginCallbackHandler");
                LOG.info("SASL/OAUTHBEARER enabled");
            }
            default -> LOG.warnf("Unknown SASL mechanism: %s", mechanism);
        }
    }

    /**
     * Quotes a value for a JAAS config entry, escaping backslashes and double
     * quotes.
     *
     * <p>These values were concatenated into the JAAS string raw. A password
     * containing {@code "} closed the quoted value early and produced a config
     * the login module rejects — authentication then failed with a parse error
     * that points nowhere near the actual cause. Backslash must be escaped
     * first, or it would double-escape the quotes added after it.
     */
    static String jaasQuote(String value) {
        String escaped = value.replace("\\", "\\\\").replace("\"", "\\\"");
        return "\"" + escaped + "\"";
    }

    private void applySsl(Properties props) {
        sslTruststoreLocation.ifPresent(loc -> {
            props.put(SslConfigs.SSL_TRUSTSTORE_LOCATION_CONFIG, loc);
            sslTruststorePassword.ifPresent(pwd -> props.put(SslConfigs.SSL_TRUSTSTORE_PASSWORD_CONFIG, pwd));
            LOG.info("SSL truststore configured");
        });

        sslKeystoreLocation.ifPresent(loc -> {
            props.put(SslConfigs.SSL_KEYSTORE_LOCATION_CONFIG, loc);
            sslKeystorePassword.ifPresent(pwd -> props.put(SslConfigs.SSL_KEYSTORE_PASSWORD_CONFIG, pwd));
            LOG.info("SSL keystore configured (mTLS)");
        });
    }

    public String getSecurityProtocol() {
        return securityProtocol;
    }
}
