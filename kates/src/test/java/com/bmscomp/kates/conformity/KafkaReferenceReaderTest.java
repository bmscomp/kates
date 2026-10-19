package com.bmscomp.kates.conformity;

import static com.bmscomp.kates.conformity.KafkaReferenceExamplesTest.json;
import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.LocalDate;
import java.util.Map;
import java.util.Set;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

import com.bmscomp.kates.api.ApiError;

/**
 * A reference the check could not run as written is refused with a 400 whose
 * fieldErrors name the path of what is wrong, as the CLI prints it:
 * {@code prod-3az.yaml: spec.brokers.cuont: unknown field}.
 */
class KafkaReferenceReaderTest {

    private static final String HEADER =
            "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\n";

    private final KafkaReferenceReader reader = new KafkaReferenceReader();

    private KafkaReference read(String spec) throws IOException {
        return reader.read(json(HEADER + "spec: " + spec));
    }

    private ApiError refusal(String spec) {
        return assertThrows(InvalidReferenceException.class, () -> read(spec)).getError();
    }

    /** The eight broken variants the draft schema refuses (spec §3.9), then the rest of the reader's checks. */
    @ParameterizedTest(name = "{0}")
    @CsvSource(delimiter = '|', textBlock = """
            {brokers: {cuont: 3}}                                   | spec.brokers.cuont                     | unknown field
            {versions: {kafka: {min: 4.10}}}                        | spec.versions.kafka.min                | quote the version ("4.1"): unquoted, it is a number
            {brokers: {count: {severity: warn}}}                    | spec.brokers.count                     | needs equals, min, max or oneOf
            {brokers: {count: {equals: 3, min: 1}}}                 | spec.brokers.count                     | equals stands alone: drop min, max and oneOf
            {brokers: {count: 3}, waivers: [{rule: brokers.count, reason: r, by: me}]} | spec.waivers[0].until | required
            {brokers: {count: 3, dedicated: true}}                  | spec.brokers.dedicated                 | unknown field: only controllers take dedicated
            {brokers: {requests: {memory: {min: 8GB}}}}             | spec.brokers.requests.memory.min       | expected a quantity such as 8Gi, 500m or 2
            {kafka: {listeners: {require: [{name: tls}]}}}          | spec.kafka.listeners.require[0]        | lists no property to check: type, port, tls or auth
            {versions: {kafka: 4}}                                  | spec.versions.kafka                    | quote the version ("4"): unquoted, it is a number
            {versions: {kafka: 4.x}}                                | spec.versions.kafka                    | expected a version such as "4.2.0"
            {brokers: {count: {oneOf: [3, 5], max: 5}}}             | spec.brokers.count                     | oneOf stands alone: drop min and max
            {brokers: {count: {min: 1, at: 2}}}                     | spec.brokers.count.at                  | unknown field
            {brokers: {count: {min: 1, severity: error}}}           | spec.brokers.count.severity            | expected fail or warn
            {brokers: {count: {oneOf: []}}}                         | spec.brokers.count.oneOf               | lists no value
            {brokers: {count: null, zones: 3}}                      | spec.brokers.count                     | a value is required
            {kafka: {listeners: {every: {tls: null}}}}              | spec.kafka.listeners.every.tls         | a value is required
            {brokers: {count: {min: 5, max: 3}}}                    | spec.brokers.count                     | min is above max, so nothing can meet it
            {brokers: {requests: {memory: {min: 8Gi, max: 4000Mi}}}} | spec.brokers.requests.memory          | min is above max, so nothing can meet it
            {versions: {kafka: {min: "4.10", max: "4.9"}}}          | spec.versions.kafka                    | min is above max, so nothing can meet it
            {kafka: {config: {num.partitions: {min: 12, max: 6}}}}  | spec.kafka.config[num.partitions]      | min is above max, so nothing can meet it
            {brokers: {count: {oneOf: [3, x]}}}                     | spec.brokers.count.oneOf[1]            | expected a whole number
            {brokers: {count: "3"}}                                 | spec.brokers.count                     | expected a whole number
            {brokers: {count: 3.5}}                                 | spec.brokers.count                     | expected a whole number
            {brokers: {count: 10001}}                               | spec.brokers.count                     | must be from 0 to 10000
            {brokers: {count: 99999999999}}                         | spec.brokers.count                     | number out of range
            {brokers: {requests: {cpu: -1}}}                        | spec.brokers.requests.cpu              | expected a quantity such as 8Gi, 500m or 2
            {brokers: {antiAffinity: always}}                       | spec.brokers.antiAffinity              | must be one of required, preferred, none
            {brokers: {antiAffinity: {min: required}}}              | spec.brokers.antiAffinity.min          | min and max need a number, a quantity or a version
            {controllers: {dedicated: "true"}}                      | spec.controllers.dedicated             | expected true or false
            {controllers: {dedicated: {oneOf: [true]}}}             | spec.controllers.dedicated.oneOf       | true or false takes equals, not oneOf
            {brokers: {storage: {class: {oneOf: [gp3, 7]}}}}        | spec.brokers.storage.class.oneOf[1]    | expected text
            {brokers: {storage: {type: nfs}}}                       | spec.brokers.storage.type              | must be one of ephemeral, persistent-claim, jbod
            {kafka: {config: {min.insync.replicas: {min: two}}}}    | spec.kafka.config[min.insync.replicas].min | a broker config's min and max are numbers
            {kafka: {config: {min.insync.replicas: [2]}}}           | spec.kafka.config[min.insync.replicas] | expected text, a number, or true or false
            {kafka: {config: {Min.ISR: 2}}}                         | spec.kafka.config[Min.ISR]             | must be a broker config name, such as min.insync.replicas
            {kafka: {listeners: {require: [{name: tls, port: 70000}]}}} | spec.kafka.listeners.require[0].port | must be from 1 to 65535
            {kafka: {listeners: {require: [{name: tls, tls: true}, {name: tls, port: 9093}]}}} | spec.kafka.listeners.require[1].name | names a listener listed before it
            {kafka: {listeners: {require: [{name: tls, tls: {equals: true, severity: warn}}]}}} | spec.kafka.listeners.require[0].tls | set severity on the listener, not on its properties
            {kafka: {listeners: {require: [{name: TLS-1, tls: true}]}}} | spec.kafka.listeners.require[0].name | must be a Strimzi listener name: up to 11 lower-case letters and digits
            {kafka: {listeners: {every: {auth: anonymous}}}}        | spec.kafka.listeners.every.auth        | must be one of none, tls, scram-sha-512, oauth, custom
            {kafka: {source: admin, rackAwareness: true}}           | spec.kafka.source                      | must be one of cr, brokers
            {target: {platform: confluent}, brokers: {count: 3}}    | spec.target.platform                   | must be strimzi, the only platform in v1alpha1
            {target: {name: Krafter}, brokers: {count: 3}}          | spec.target.name                       | must be a DNS label: up to 63 lower-case letters, digits and -
            {zoneLabel: topology.kubernetes.io/zone}                | spec                                   | declares no rule
            {brokers: {count: 3}, waivers: [{rule: brokers.zones, reason: r, by: me, until: 2026-12-31}]} | spec.waivers[0].rule | names no rule this reference declares
            {brokers: {count: 3}, waivers: [{rule: brokers.count, reason: r, by: me, until: 2026-12-31}, {rule: brokers.count, reason: r, by: you, until: 2027-01-31}]} | spec.waivers[1].rule | waives the rule spec.waivers[0] waives
            {brokers: {count: 3}, waivers: [{rule: target.name, reason: r, by: me, until: 2026-12-31}]} | spec.waivers[0].rule | names no rule this reference declares
            {brokers: {count: 3}, waivers: [{rule: brokers.count, reason: "", by: me, until: 2026-12-31}]} | spec.waivers[0].reason | must be 1 to 500 characters
            {brokers: {count: 3}, waivers: [{rule: brokers.count, reason: r, by: me, until: 2026-02-30}]} | spec.waivers[0].until | expected a date such as 2026-12-31
            """)
    void refusesWithThePathOfWhatIsWrong(String spec, String path, String reason) {
        ApiError error = refusal(spec);

        assertEquals(400, error.getStatus());
        assertEquals(
                reason,
                error.getFieldErrors().get(path),
                () -> error.getFieldErrors().toString());
        assertTrue(error.getMessage().contains(path + ": " + reason), error.getMessage());
    }

    /** A field left out declares no rule, and equal bounds are one exact value. */
    @Test
    void acceptsLeftOutFieldsAndEqualBounds() throws IOException {
        KafkaReference reference = read("{brokers: {count: {min: 3, max: 3}}}");

        assertNull(reference.spec().brokers().zones());
        assertEquals(Set.of("brokers.count"), KafkaReferenceReader.ruleIds(reference));
    }

    /** A required listener's rule id is its name; a waiver names it so. */
    @Test
    void waivesARequiredListenerByName() throws IOException {
        KafkaReference reference = read("{kafka: {listeners: {require: [{name: tls, port: 9093}]}},"
                + " waivers: [{rule: 'kafka.listeners.require[tls]', reason: r, by: me, until: 2026-12-31}]}");

        assertEquals(
                "kafka.listeners.require[tls]",
                reference.spec().waivers().get(0).rule());
    }

    /** An expired waiver makes its rule count again; it does not stop the check. */
    @Test
    void acceptsAWaiverWhoseDateHasPassed() throws IOException {
        KafkaReference reference =
                read("{brokers: {count: 3}, waivers: [{rule: brokers.count, reason: r, by: me, until: 2020-01-31}]}");

        assertEquals(
                LocalDate.of(2020, 1, 31), reference.spec().waivers().get(0).until());
    }

    /** Every limit and kind error is named at once, after the first error Jackson stops at. */
    @Test
    void namesEveryCheckThatFails() {
        ApiError error = refusal("{brokers: {count: 10001, zones: 10001, antiAffinity: always}}");

        assertEquals(
                Map.of(
                        "spec.brokers.count", "must be from 0 to 10000",
                        "spec.brokers.zones", "must be from 0 to 10000",
                        "spec.brokers.antiAffinity", "must be one of required, preferred, none"),
                error.getFieldErrors());
    }

    @Test
    void refusesAnotherApiVersionOrKind() {
        ApiError error = assertThrows(
                        InvalidReferenceException.class,
                        () -> reader.read(json(
                                "apiVersion: v1\nkind: Kafka\nmetadata: {name: t}\n" + "spec: {brokers: {count: 3}}")))
                .getError();

        assertEquals(
                Map.of(
                        "apiVersion", "must be conformity.kates.io/v1alpha1",
                        "kind", "must be KafkaReference"),
                error.getFieldErrors());
    }

    @Test
    void refusesAMissingSpecOrName() {
        ApiError error = assertThrows(
                        InvalidReferenceException.class,
                        () -> reader.read(json("apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\n"
                                + "metadata: {description: d}\n")))
                .getError();

        assertEquals(Map.of("metadata.name", "required", "spec", "required"), error.getFieldErrors());
    }

    /** A JSON client that sends a key twice gets told, rather than having the last one win. */
    @Test
    void refusesADuplicateKey() {
        String body = "{\"apiVersion\":\"conformity.kates.io/v1alpha1\",\"kind\":\"KafkaReference\","
                + "\"metadata\":{\"name\":\"t\"},\"spec\":{\"brokers\":{\"count\":3,\"count\":5}}}";

        ApiError error = assertThrows(
                        InvalidReferenceException.class, () -> reader.read(body.getBytes(StandardCharsets.UTF_8)))
                .getError();

        assertEquals(400, error.getStatus());
        assertTrue(error.getMessage().contains("Duplicate field 'count'"), error.getMessage());
    }

    /** A quantity's number is matched as written: as digits, 1e999999999 would take a gigabyte. */
    @Test
    void refusesAQuantityWithAnExponent() {
        String body = "{\"apiVersion\":\"conformity.kates.io/v1alpha1\",\"kind\":\"KafkaReference\","
                + "\"metadata\":{\"name\":\"t\"},\"spec\":{\"brokers\":{\"requests\":{\"cpu\":1e999999999}}}}";

        ApiError error = assertThrows(
                        InvalidReferenceException.class, () -> reader.read(body.getBytes(StandardCharsets.UTF_8)))
                .getError();

        assertEquals(
                Map.of("spec.brokers.requests.cpu", "expected a quantity such as 8Gi, 500m or 2"),
                error.getFieldErrors());
    }

    @Test
    void refusesABodyThatIsNotJson() {
        ApiError error = assertThrows(
                        InvalidReferenceException.class, () -> reader.read("{".getBytes(StandardCharsets.UTF_8)))
                .getError();

        assertTrue(error.getFieldErrors().get("body").startsWith("not valid JSON: "), error.getMessage());
    }

    @Test
    void refusesABodyOverTheLimitWith413() {
        ApiError error = assertThrows(
                        InvalidReferenceException.class,
                        () -> reader.read(new byte[KafkaReferenceReader.MAX_BYTES + 1]))
                .getError();

        assertEquals(413, error.getStatus());
    }
}
