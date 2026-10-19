package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.util.Comparator;
import java.util.List;
import java.util.Optional;

import io.fabric8.kubernetes.api.model.Quantity;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * How a live value is compared with a declared constraint, kind by kind. Each
 * constraint is read as a reference writes it, so the bare and the map forms
 * are covered too.
 */
class ConstraintTest {

    private final KafkaReferenceReader reader = new KafkaReferenceReader();

    private KafkaReference.Spec spec(String spec) throws IOException {
        return reader.read(json(
                        "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\nspec: "
                                + spec))
                .spec();
    }

    /** Kubernetes quantities compare by amount, whatever their suffix. */
    @ParameterizedTest(name = "{0} for {1}: {2}")
    @CsvSource(delimiter = '|', textBlock = """
            {min: 8Gi}             | 8000Mi    | false
            {min: 8Gi}             | 8Gi       | true
            {min: 8Gi}             | 8589934592 | true
            {min: 8Gi}             | 9G        | true
            8Gi                    | 8192Mi    | true
            8Gi                    | 8G        | false
            {min: "2"}             | 500m      | false
            {min: "2"}             | 2000m     | true
            {min: 500m, max: 2}    | 1         | true
            {min: 500m, max: 2}    | 2001m     | false
            {oneOf: [1Gi, 2Gi]}    | 2048Mi    | true
            {oneOf: [1Gi, 2Gi]}    | 1.5Gi     | false
            """)
    void quantities(String constraint, String observed, boolean holds) throws IOException {
        Constraint<Quantity> memory = spec("{brokers: {requests: {memory: " + constraint + "}}}")
                .brokers()
                .requests()
                .memory();

        assertEquals(holds, memory.holds(new Quantity(observed), Comparator.naturalOrder()));
    }

    /** Versions compare part by part, a missing part counts as 0, and a build suffix is dropped. */
    @ParameterizedTest(name = "{0} for {1}: {2}")
    @CsvSource(delimiter = '|', textBlock = """
            {min: "4.2.0"}                 | 4.2              | true
            {min: "4.2.0"}                 | 4.1.9            | false
            {min: "4.2.0"}                 | 4.10.0           | true
            {min: "4.2.0", max: "4.3.9"}   | 4.3.10           | false
            {min: "1.32.0"}                | v1.34.11+k3s1    | true
            {min: "1.32.0"}                | v1.31.4-eks-1234 | false
            "4.2"                          | 4.2.0            | true
            "v4.2.1"                       | 4.2.1            | true
            {oneOf: ["4.1", "4.2"]}        | 4.2.0-rc1        | true
            """)
    void versions(String constraint, String observed, boolean holds) throws IOException {
        Constraint<Version> kafka =
                spec("{versions: {kafka: " + constraint + "}}").versions().kafka();

        assertEquals(holds, kafka.holds(Version.parse(observed).orElseThrow(), Comparator.naturalOrder()));
    }

    /** A version the cluster reports that does not start with one is a parse failure, not an order. */
    @ParameterizedTest(name = "\"{0}\"")
    @ValueSource(strings = {"", "abc", "4.x", "4..2", ".4.2", "4.2.", "99999999999.1"})
    void reportsAVersionItCannotRead(String reported) {
        assertEquals(Optional.empty(), Version.parse(reported));
    }

    @Test
    void readsAVersionWithoutItsBuildSuffix() {
        assertEquals(
                List.of(1, 34, 11), Version.parse("v1.34.11+k3s1").orElseThrow().parts());
        assertEquals(Optional.empty(), Version.parse(null));
    }

    /** A broker config compares text for equals and oneOf, and numbers for min and max. */
    @ParameterizedTest(name = "{0} for {1}: {2}")
    @CsvSource(delimiter = '|', textBlock = """
            false                  | false   | true
            false                  | FALSE   | true
            false                  | true    | false
            "False"                | false   | true
            3                      | 3       | true
            3                      | 3.0     | false
            {min: 2}               | 3       | true
            {min: 2}               | 2       | true
            {min: 2}               | 1       | false
            {min: 2}               | two     | false
            {max: 0.5}             | 0.5     | true
            {max: 0.5}             | 0.51    | false
            {min: 1, max: 3}       | ' 2 '   | true
            {oneOf: [lz4, zstd]}   | zstd    | true
            {oneOf: [lz4, zstd]}   | ZSTD    | false
            {equals: producer}     | producer | true
            """)
    void brokerConfigs(String constraint, String observed, boolean holds) throws IOException {
        var config = spec("{kafka: {config: {some.setting: " + constraint + "}}}")
                .kafka()
                .config();

        assertEquals(holds, Constraint.configHolds(config.get("some.setting"), observed));
    }

    @Test
    void integersBooleansAndText() throws IOException {
        KafkaReference.Role brokers =
                spec("{brokers: {count: {min: 3}, perNode: {max: 1}, antiAffinity: {oneOf: [required, preferred]}}}")
                        .brokers();
        KafkaReference.Role controllers =
                spec("{controllers: {dedicated: true}}").controllers();

        assertTrue(brokers.count().holds(3, Comparator.naturalOrder()));
        assertFalse(brokers.count().holds(2, Comparator.naturalOrder()));
        assertFalse(brokers.perNode().holds(2, Comparator.naturalOrder()));
        assertTrue(brokers.antiAffinity().holds("preferred", Comparator.naturalOrder()));
        assertFalse(brokers.antiAffinity().holds("none", Comparator.naturalOrder()));
        assertTrue(controllers.dedicated().holds(true, Comparator.naturalOrder()));
        assertFalse(controllers.dedicated().holds(false, Comparator.naturalOrder()));
    }
}
