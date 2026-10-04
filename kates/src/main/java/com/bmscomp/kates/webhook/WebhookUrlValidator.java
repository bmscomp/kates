package com.bmscomp.kates.webhook;

import java.net.InetAddress;
import java.net.URI;
import java.net.UnknownHostException;
import java.util.List;
import java.util.Locale;
import java.util.Optional;
import jakarta.enterprise.context.ApplicationScoped;

import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * SSRF guard for user-supplied webhook URLs. Without it the backend will POST
 * to any URL a caller registers — including the cloud metadata endpoint
 * (169.254.169.254), localhost sidecars, or arbitrary internal services.
 *
 * Default policy (tuned for an in-cluster tool whose legitimate targets are
 * cluster-internal, i.e. site-local, addresses):
 * - scheme must be http or https ({@code kates.webhooks.allowed-schemes})
 * - loopback, link-local (covers cloud metadata), wildcard, and multicast
 *   addresses are always rejected — every resolved address is checked
 * - site-local (RFC 1918) addresses are allowed unless
 *   {@code kates.webhooks.block-private-addresses=true}
 * - if {@code kates.webhooks.allowlist} is set (comma-separated URL prefixes),
 *   the URL must match one of them
 *
 * Validation runs at registration time and again before each delivery, so a
 * DNS record that later starts resolving to a blocked address is re-caught.
 */
@ApplicationScoped
public class WebhookUrlValidator {

    @ConfigProperty(name = "kates.webhooks.allowed-schemes", defaultValue = "http,https")
    List<String> allowedSchemes;

    @ConfigProperty(name = "kates.webhooks.block-private-addresses", defaultValue = "false")
    boolean blockPrivateAddresses;

    /** Loopback targets are legitimate in local dev/test only (%dev/%test set this true). */
    @ConfigProperty(name = "kates.webhooks.allow-loopback", defaultValue = "false")
    boolean allowLoopback;

    @ConfigProperty(name = "kates.webhooks.allowlist")
    Optional<List<String>> allowlist;

    /**
     * @throws IllegalArgumentException when the URL violates the policy
     *         (mapped to HTTP 400 by the global exception mapper)
     */
    public void validate(String url) {
        URI uri;
        try {
            uri = URI.create(url);
        } catch (Exception e) {
            throw new IllegalArgumentException("Invalid webhook URL: " + e.getMessage());
        }

        String scheme = uri.getScheme() == null ? "" : uri.getScheme().toLowerCase(Locale.ROOT);
        if (allowedSchemes.stream().noneMatch(scheme::equals)) {
            throw new IllegalArgumentException(
                    "Webhook URL scheme '" + scheme + "' is not allowed (allowed: " + allowedSchemes + ")");
        }

        String host = uri.getHost();
        if (host == null || host.isBlank()) {
            throw new IllegalArgumentException("Webhook URL has no host");
        }

        if (allowlist.isPresent() && !allowlist.get().isEmpty()) {
            boolean matched = allowlist.get().stream().anyMatch(prefix -> url.startsWith(prefix.trim()));
            if (!matched) {
                throw new IllegalArgumentException("Webhook URL does not match any entry of kates.webhooks.allowlist");
            }
        }

        InetAddress[] addresses;
        try {
            addresses = InetAddress.getAllByName(host);
        } catch (UnknownHostException e) {
            throw new IllegalArgumentException("Webhook host cannot be resolved: " + host);
        }

        for (InetAddress address : addresses) {
            if (address.isLoopbackAddress() && !allowLoopback) {
                throw new IllegalArgumentException(
                        "Webhook host resolves to a loopback address (" + address.getHostAddress() + ")");
            }
            if (address.isLinkLocalAddress()) {
                // 169.254.0.0/16 and fe80::/10 — includes cloud metadata endpoints.
                throw new IllegalArgumentException(
                        "Webhook host resolves to a link-local address (" + address.getHostAddress() + ")");
            }
            if (address.isAnyLocalAddress() || address.isMulticastAddress()) {
                throw new IllegalArgumentException(
                        "Webhook host resolves to a non-routable address (" + address.getHostAddress() + ")");
            }
            if (blockPrivateAddresses && address.isSiteLocalAddress()) {
                throw new IllegalArgumentException("Webhook host resolves to a private address ("
                        + address.getHostAddress() + ") and kates.webhooks.block-private-addresses=true");
            }
        }
    }
}
