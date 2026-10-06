package com.bmscomp.kates.chaos;

import java.util.List;
import java.util.Optional;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import org.jboss.logging.Logger;

/**
 * The node the Kates API runs on. A NODE_DRAIN evicts every pod on its node,
 * so a drain of this one would evict the Kates API in the middle of the run
 * it is driving; the litmus-crd provider and the safety guard keep drains
 * off it.
 *
 * <p>Looked up once, from the Kates API's own pod: the one named HOSTNAME,
 * which Kubernetes sets to the pod's name, in the namespace the client runs
 * in. Outside a pod, as in dev mode, there is no such pod, the node is
 * unknown, and drains are kept off no node. A lookup that fails is tried
 * again at the next drain.
 */
@ApplicationScoped
public class KatesNode {

    private static final Logger LOG = Logger.getLogger(KatesNode.class);

    @Inject
    KubernetesClient client;

    /** Null until a lookup has answered, whether it found the node or not. */
    private volatile Optional<String> node;

    /** The node the Kates API runs on, or empty when it is not known. */
    public Optional<String> name() {
        Optional<String> known = node;
        if (known != null) {
            return known;
        }
        try {
            known = lookUp();
        } catch (RuntimeException e) {
            LOG.warn("Could not look up the node the Kates API runs on, so a NODE_DRAIN may drain it: "
                    + e.getMessage());
            return Optional.empty();
        }
        node = known;
        return known;
    }

    private Optional<String> lookUp() {
        String pod = podName();
        String namespace = client.getNamespace();
        if (pod == null || pod.isBlank() || namespace == null) {
            LOG.info("The Kates API is not in a pod, so its node is unknown and a NODE_DRAIN may drain it");
            return Optional.empty();
        }
        Pod self = client.pods().inNamespace(namespace).withName(pod).get();
        String nodeName =
                self != null && self.getSpec() != null ? self.getSpec().getNodeName() : null;
        if (nodeName == null || nodeName.isBlank()) {
            LOG.info("No pod named " + pod + " runs on a node in namespace " + namespace
                    + ", so the Kates API's node is unknown and a NODE_DRAIN may drain it");
            return Optional.empty();
        }
        LOG.info("The Kates API runs on node " + nodeName + ", which a NODE_DRAIN never drains");
        return Optional.of(nodeName);
    }

    /** The Kates API's pod name, which Kubernetes sets as the HOSTNAME. */
    String podName() {
        return System.getenv("HOSTNAME");
    }

    /**
     * Why a drain of the Kates API's node is refused. {@code how} says how the
     * drain came to aim at it, such as {@link #picked}.
     */
    public static String refusal(String how, String node) {
        return "NODE_DRAIN: " + how + " " + node + ", the node the Kates API runs on, and draining it would evict"
                + " the Kates API in the middle of the run";
    }

    /** How a drain came to aim at a node: the pods it picked run there, in name order. */
    public static String picked(List<String> pods) {
        List<String> sorted = pods.stream().sorted().toList();
        return sorted.size() == 1
                ? "pod " + sorted.getFirst() + " runs on"
                : "pods " + String.join(", ", sorted) + " run on";
    }
}
