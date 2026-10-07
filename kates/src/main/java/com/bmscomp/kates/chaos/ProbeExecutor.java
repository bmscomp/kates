package com.bmscomp.kates.chaos;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.regex.Pattern;
import java.util.stream.Collectors;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.GenericKubernetesResource;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.dsl.ExecWatch;
import org.jboss.logging.Logger;

/**
 * Evaluates {@link ProbeSpec} probes.
 *
 * <ul>
 *   <li>A {@code kafkaProbe} runs one of the {@link KafkaProbeChecks} over Kates' own Kafka
 *       connection.
 *   <li>A {@code k8sProbe} whose command names {@code kafka} and {@code Ready} prints the Kafka
 *       resource's readiness, {@code Ready=True} when its Ready condition is True. Any other runs as
 *       a cmdProbe.
 *   <li>A {@code cmdProbe}, and a probe of any other type, runs its command with {@code sh -c} in the
 *       first broker pod by name that is Ready and not being deleted. Kates' ServiceAccount needs
 *       {@code get} and {@code create} on {@code pods/exec} in the target namespace for that, which
 *       the kates chart does not grant.
 * </ul>
 *
 * <p>A probe fails, whatever its comparator, when it cannot be evaluated: its command exits non-zero
 * or outlasts {@code timeoutSec}, no pod or Kafka resource can answer it, a numeric comparator meets
 * output that is not a number, or its comparator is not one of {@link #COMPARATORS}. Each of those
 * used to be compared like output — text, or a number read as 0 — so a probe that could not run
 * passed a {@code <=} or a {@code contains}.
 */
@ApplicationScoped
public class ProbeExecutor {

    private static final Logger LOG = Logger.getLogger(ProbeExecutor.class);

    /** The probe types evaluated as such; any other runs as a cmdProbe. */
    public static final List<String> TYPES = List.of("cmdProbe", "k8sProbe", "kafkaProbe");

    /** The comparators a probe can name: the first three compare text, the others numbers. */
    public static final List<String> COMPARATORS =
            List.of("equal", "contains", "notContains", "==", "!=", ">=", "<=", ">", "<");

    private static final List<String> NUMERIC = List.of("==", "!=", ">=", "<=", ">", "<");

    /** A plain decimal number, as a count, a sum or a rate prints. */
    private static final Pattern NUMBER = Pattern.compile("[-+]?(\\d+(\\.\\d*)?|\\.\\d+)([eE][-+]?\\d+)?");

    /** How much of a failed command's stderr its result quotes. */
    private static final int STDERR_TAIL = 400;

    @Inject
    KubernetesClient client;

    @Inject
    KafkaProbeChecks kafkaChecks;

    /**
     * Evaluate a single probe against the cluster.
     */
    public ProbeResult evaluate(ProbeSpec probe, String namespace) {
        long startNanos = System.nanoTime();
        try {
            problem(probe).ifPresent(problem -> {
                throw new ProbeFailure(problem);
            });
            String output = executeProbe(probe, namespace);
            boolean passed = compare(output, probe.expectedOutput(), comparator(probe));
            long durationMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startNanos);

            if (passed) {
                LOG.debugf("Probe '%s' PASSED (%dms)", probe.name(), durationMs);
                return ProbeResult.pass(probe.name(), output, durationMs);
            } else {
                LOG.infof(
                        "Probe '%s' FAILED: output '%s' is not %s '%s'",
                        probe.name(), output, comparator(probe), probe.expectedOutput());
                return ProbeResult.fail(probe.name(), output, durationMs);
            }
        } catch (Exception e) {
            long durationMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startNanos);
            LOG.warnf("Probe '%s' ERROR: %s", probe.name(), e.getMessage());
            return ProbeResult.fail(probe.name(), "Error: " + e.getMessage(), durationMs);
        }
    }

    /**
     * Evaluate all probes and return results.
     */
    public List<ProbeResult> evaluateAll(List<ProbeSpec> probes, String namespace) {
        List<ProbeResult> results = new ArrayList<>();
        for (ProbeSpec probe : probes) {
            results.add(evaluate(probe, namespace));
        }
        return results;
    }

    private static String comparator(ProbeSpec probe) {
        return probe.comparator() != null ? probe.comparator() : "contains";
    }

    /**
     * What makes {@link #evaluate} fail {@code probe} before running anything, if anything: a
     * comparator it doesn't have, no expectedOutput, or one a numeric comparator can't read as a
     * number, and a kafkaProbe command that names no check or gives it the wrong arguments.
     */
    public static Optional<String> problem(ProbeSpec probe) {
        String comparator = comparator(probe);
        if (!COMPARATORS.contains(comparator)) {
            return Optional.of("comparator '" + comparator + "' is not one of " + COMPARATORS);
        }
        if (probe.expectedOutput() == null) {
            return Optional.of("the probe has no expectedOutput to compare with");
        }
        if (NUMERIC.contains(comparator)
                && !NUMBER.matcher(probe.expectedOutput().trim()).matches()) {
            return Optional.of(
                    "expectedOutput is not a number: '" + probe.expectedOutput().trim() + "'");
        }
        if ("kafkaProbe".equals(probe.type())) {
            return KafkaProbeChecks.problem(probe.command());
        }
        return Optional.empty();
    }

    /**
     * Whether {@code actual} passes {@code comparator} against {@code expected}. A numeric comparator
     * throws {@link ProbeFailure} for output that is not one number: {@code 0\n0} or an error message
     * no longer reads as 0.
     */
    static boolean compare(String actual, String expected, String comparator) {
        return switch (comparator) {
            case "equal" -> actual.trim().equals(expected.trim());
            case "contains" -> actual.contains(expected);
            case "notContains" -> !actual.contains(expected);
            case "==" -> number(actual, "output") == number(expected, "expectedOutput");
            case "!=" -> number(actual, "output") != number(expected, "expectedOutput");
            case ">=" -> number(actual, "output") >= number(expected, "expectedOutput");
            case "<=" -> number(actual, "output") <= number(expected, "expectedOutput");
            case ">" -> number(actual, "output") > number(expected, "expectedOutput");
            case "<" -> number(actual, "output") < number(expected, "expectedOutput");
            default -> throw new ProbeFailure("comparator '" + comparator + "' is not one of " + COMPARATORS);
        };
    }

    private static double number(String text, String what) {
        String trimmed = text.trim();
        if (!NUMBER.matcher(trimmed).matches()) {
            throw new ProbeFailure(what + " is not a number: '" + trimmed.replace("\n", "\\n") + "'");
        }
        return Double.parseDouble(trimmed);
    }

    private String executeProbe(ProbeSpec probe, String namespace) {
        String type = probe.type() != null ? probe.type() : "cmdProbe";
        return switch (type) {
            case "kafkaProbe" -> kafkaChecks.run(probe.command(), Duration.ofSeconds(probe.timeoutSec()));
            case "k8sProbe" -> executeK8sProbe(probe, namespace);
            default -> executeCmdProbe(probe, namespace);
        };
    }

    private String executeCmdProbe(ProbeSpec probe, String namespace) {
        String pod = probePod(namespace);
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        ByteArrayOutputStream err = new ByteArrayOutputStream();
        try (ExecWatch watch = client.pods()
                .inNamespace(namespace)
                .withName(pod)
                .writingOutput(out)
                .writingError(err)
                .exec("sh", "-c", probe.command())) {
            // The exit status arrives after the last of the output has been written.
            Integer exitCode = watch.exitCode().get(probe.timeoutSec(), TimeUnit.SECONDS);
            if (exitCode == null) {
                throw new ProbeFailure("the command in pod " + pod + " ended without an exit status");
            }
            if (exitCode != 0) {
                throw new ProbeFailure("the command in pod " + pod + " exited " + exitCode + stderrTail(err));
            }
            return out.toString(StandardCharsets.UTF_8).trim();
        } catch (TimeoutException e) {
            throw new ProbeFailure("the command in pod " + pod + " did not finish within " + probe.timeoutSec() + " s");
        } catch (ExecutionException e) {
            Throwable cause = e.getCause() != null ? e.getCause() : e;
            throw new ProbeFailure("the command in pod " + pod + " failed: " + cause.getMessage(), cause);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new ProbeFailure("the probe was interrupted");
        }
    }

    private static String stderrTail(ByteArrayOutputStream err) {
        String text = err.toString(StandardCharsets.UTF_8).strip();
        if (text.isEmpty()) {
            return "";
        }
        return ": " + (text.length() > STDERR_TAIL ? "…" + text.substring(text.length() - STDERR_TAIL) : text);
    }

    /**
     * The pod a cmdProbe runs in: the first broker by name that is Ready and not being deleted. The
     * first Kafka pod in list order used to be taken, which could be a KRaft controller, with no
     * client listener, or the broker a fault had just deleted.
     */
    String probePod(String namespace) {
        return client
                .pods()
                .inNamespace(namespace)
                .withLabel("strimzi.io/component-type", "kafka")
                .withLabel("strimzi.io/broker-role", "true")
                .list()
                .getItems()
                .stream()
                .filter(p -> p.getMetadata().getDeletionTimestamp() == null)
                .filter(ProbeExecutor::isReady)
                .map(p -> p.getMetadata().getName())
                .sorted()
                .findFirst()
                .orElseThrow(() -> new ProbeFailure(
                        "no Ready broker pod (strimzi.io/broker-role=true) in namespace " + namespace));
    }

    private static boolean isReady(Pod pod) {
        return pod.getStatus() != null
                && pod.getStatus().getConditions() != null
                && pod.getStatus().getConditions().stream()
                        .anyMatch(c -> "Ready".equals(c.getType()) && "True".equals(c.getStatus()));
    }

    private String executeK8sProbe(ProbeSpec probe, String namespace) {
        String command = probe.command() != null ? probe.command() : "";
        if (command.contains("kafka") && command.contains("Ready")) {
            List<GenericKubernetesResource> kafkas = client.genericKubernetesResources("kafka.strimzi.io/v1", "Kafka")
                    .inNamespace(namespace)
                    .list()
                    .getItems();
            return kafkas.stream()
                    .min(Comparator.comparing(k -> k.getMetadata().getName()))
                    .map(k -> readiness(k.getAdditionalProperties().get("status")))
                    .orElseThrow(() -> new ProbeFailure("no Kafka resource in namespace " + namespace));
        }
        return executeCmdProbe(probe, namespace);
    }

    /**
     * A Kafka resource's readiness: {@code Ready=True} when its Ready condition is True; otherwise
     * that condition, or every condition when it has none, with reasons. Strimzi reports a failed
     * reconciliation as a {@code NotReady} condition, which the old {@code contains "Ready"}
     * comparison passed.
     */
    static String readiness(Object status) {
        List<Map<?, ?>> conditions = conditions(status);
        if (conditions.isEmpty()) {
            return "no status conditions";
        }
        Optional<Map<?, ?>> ready =
                conditions.stream().filter(c -> "Ready".equals(c.get("type"))).findFirst();
        if (ready.isPresent() && "True".equals(String.valueOf(ready.get().get("status")))) {
            return "Ready=True";
        }
        List<Map<?, ?>> shown = ready.isPresent() ? List.<Map<?, ?>>of(ready.get()) : conditions;
        return shown.stream().map(ProbeExecutor::condition).collect(Collectors.joining("; "));
    }

    private static List<Map<?, ?>> conditions(Object status) {
        if (!(status instanceof Map<?, ?> fields) || !(fields.get("conditions") instanceof List<?> list)) {
            return List.of();
        }
        List<Map<?, ?>> conditions = new ArrayList<>();
        for (Object c : list) {
            if (c instanceof Map<?, ?> condition) {
                conditions.add(condition);
            }
        }
        return conditions;
    }

    private static String condition(Map<?, ?> c) {
        String text = c.get("type") + "=" + c.get("status");
        Object reason = c.get("reason");
        Object message = c.get("message");
        if (reason == null && message == null) {
            return text;
        }
        return text + " (" + (reason != null ? reason : "") + (reason != null && message != null ? ": " : "")
                + (message != null ? message : "") + ")";
    }
}
