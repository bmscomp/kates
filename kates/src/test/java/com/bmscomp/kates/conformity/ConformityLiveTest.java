package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.stream.Collectors;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientBuilder;
import io.fabric8.kubernetes.client.vertx.VertxHttpClientFactory;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

import com.bmscomp.kates.conformity.ConformityReport.Result;
import com.bmscomp.kates.conformity.ConformityReport.Status;
import com.bmscomp.kates.conformity.ConformityReport.Verdict;

/**
 * Holds the cluster CI · Integration deploys on kind to
 * cli/examples/conformity/kind-ci.yaml, through the kubeconfig the job leaves
 * behind: every rule must pass. It reads Strimzi's real resources, so it
 * catches what the mock server cannot, such as a status field Strimzi names
 * differently. It runs only where KATES_CONFORMITY_LIVE=true; kafka.source is
 * cr, so it needs no Kafka connection.
 */
@EnabledIfEnvironmentVariable(named = "KATES_CONFORMITY_LIVE", matches = "true")
class ConformityLiveTest {

    @Test
    void theCiClusterConformsToItsReference() throws Exception {
        KafkaReference reference = new KafkaReferenceReader()
                .read(json(Files.readString(Path.of("..", "cli", "examples", "conformity", "kind-ci.yaml"))));
        try (KubernetesClient client = new KubernetesClientBuilder()
                .withHttpClientFactory(new VertxHttpClientFactory())
                .build()) {

            ConformityReport report = new ConformityCheck(client, null, "kafka", "krafter").check(reference);

            String gaps = report.results().stream()
                    .filter(r -> r.status() != Status.PASS)
                    .map(ConformityLiveTest::line)
                    .collect(Collectors.joining("\n"));
            assertEquals(Verdict.PASS, report.verdict(), () -> "rules that do not pass:\n" + gaps);
            assertEquals("", gaps, "every rule passes, warn rules included");
            assertEquals(
                    KafkaReferenceReader.ruleIds(reference).size(),
                    report.results().size());
            assertNotNull(report.target().clusterId(), "the Kafka CR's status.clusterId");
        }
    }

    private static String line(Result r) {
        return r.status() + " " + r.rule() + ": expected " + r.expected() + ", observed " + r.observed()
                + (r.subjects() == null ? "" : " " + r.subjects())
                + (r.detail() == null ? "" : " (" + r.detail() + ")");
    }
}
