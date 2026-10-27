package com.bmscomp.kates.config;

import java.util.ArrayList;
import java.util.List;
import java.util.regex.Pattern;
import jakarta.inject.Singleton;

import io.fabric8.kubernetes.client.Config;
import io.quarkus.kubernetes.client.KubernetesConfigCustomizer;
import org.jboss.logging.Logger;

/**
 * Keeps a {@code NO_PROXY} written for curl from stopping Kates at start-up.
 *
 * <p>The Kubernetes client reads {@code NO_PROXY} from the environment and,
 * when it builds its HTTP client, throws {@code MalformedURLException: NO_PROXY
 * URL contains invalid entry} for any entry holding a character other than a
 * letter, a digit or {@code . - / :}. The common {@code *.svc.cluster.local}
 * form is one: corporate proxies, CI runners and cloud sandboxes set it, and
 * then every bean that injects the client fails, so the application does not
 * start at all.
 *
 * <p>The client matches an entry as a host suffix, so {@code *.example.com}
 * means exactly {@code .example.com} to it, and a lone {@code *} (no proxy for
 * anything) means the empty suffix. This rewrites those two forms and leaves
 * every entry the client already accepts untouched. An entry that is still
 * invalid after that, with a space or an underscore say, is dropped with a
 * warning: the client then proxies that host, which is what it would do for a
 * host the list does not name, rather than refusing to start.
 *
 * <p>{@link KubernetesConfigCustomizer} beans run after the extension builds
 * the {@link Config} and before the client builds its HTTP client, which is
 * where the entries are checked.
 */
@Singleton
public class KubernetesNoProxyCustomizer implements KubernetesConfigCustomizer {

    private static final Logger LOG = Logger.getLogger(KubernetesNoProxyCustomizer.class);

    /** The characters the client refuses, as fabric8's HttpClientUtils defines them. */
    private static final Pattern INVALID = Pattern.compile("[^\\da-zA-Z.\\-/:]+");

    @Override
    public void customize(Config config) {
        String[] entries = config.getNoProxy();
        if (entries != null) {
            config.setNoProxy(normalize(entries));
        }
    }

    static String[] normalize(String[] entries) {
        List<String> out = new ArrayList<>(entries.length);
        for (String entry : entries) {
            if (entry == null || !INVALID.matcher(entry).find()) {
                out.add(entry);
                continue;
            }
            String rewritten = entry.trim();
            if (rewritten.equals("*")) {
                rewritten = "";
            } else if (rewritten.startsWith("*.")) {
                rewritten = rewritten.substring(1);
            }
            if (INVALID.matcher(rewritten).find()) {
                LOG.warnf(
                        "NO_PROXY entry '%s' is not a host, domain or address the Kubernetes client understands;"
                                + " ignoring it, so the client may reach that host through the proxy",
                        entry);
                continue;
            }
            out.add(rewritten);
        }
        return out.toArray(new String[0]);
    }
}
