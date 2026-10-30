package com.bmscomp.kates.compliance;

import static com.bmscomp.kates.compliance.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.stream.Collectors;

import io.fabric8.kubernetes.client.KubernetesClient;
import io.fabric8.kubernetes.client.KubernetesClientBuilder;
import io.fabric8.kubernetes.client.vertx.VertxHttpClientFactory;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

import com.bmscomp.kates.compliance.ComplianceReport.Result;
import com.bmscomp.kates.compliance.ComplianceReport.Status;
import com.bmscomp.kates.compliance.ComplianceReport.Verdict;

/**
 * Holds the cluster CI · Integration deploys on kind to
 * cli/examples/conformity/kind-ci.yaml, through the kubeconfig the job leaves
 * behind: every rule must pass. It reads Strimzi's real resources, so it
 * catches what the mock server cannot, such as a status field Strimzi names
 * differently. It runs only where KATES_COMPLIANCE_LIVE=true; kafka.source is
 * cr, so it needs no Kafka connection.
 */
@EnabledIfEnvironmentVariable(named = "KATES_COMPLIANCE_LIVE", matches = "true")
class ComplianceLiveTest {

    @Test
    void theCiClusterConformsToItsReference() throws Exception {
        KafkaReference reference = new KafkaReferenceReader()
                .read(json(Files.readString(Path.of("..", "cli", "examples", "conformity", "kind-ci.yaml"))));
        try (KubernetesClient client = new KubernetesClientBuilder()
                .withHttpClientFactory(new VertxHttpClientFactory())
                .build()) {

            ComplianceReport report = new ComplianceCheck(client, null, "kafka", "krafter").check(reference);

            String gaps = report.results().stream()
                    .filter(r -> r.status() != Status.PASS)
                    .map(ComplianceLiveTest::line)
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
