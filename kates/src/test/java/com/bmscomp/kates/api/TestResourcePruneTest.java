package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static java.util.stream.Collectors.toMap;
import static org.hamcrest.Matchers.is;
import static org.junit.jupiter.api.Assertions.*;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.path.json.JsonPath;
import io.restassured.specification.RequestSpecification;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.AuditService;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * DELETE /api/tests, which deletes the finished runs created before an
 * instant, for the chart's cleanup CronJob and kates test prune. The CronJob
 * listed the runs itself, asked for a status no run has, and picked ids out
 * of the list with a pattern its answer never matched: it never deleted one.
 *
 * <p>The runs here are created in 2001, older than any run another test
 * stores, so each count here is of this test's runs alone, and each test
 * deletes its runs after it. The test profile keeps TestCleanupScheduler off
 * runs that old. The runs that have not finished are PENDING and STOPPING:
 * the timeout reaper fails a RUNNING run past its deadline, as one from 2001
 * is, at whatever moment it runs, and a FAILED run is one to prune.
 */
@QuarkusTest
class TestResourcePruneTest {

    private static final Instant ERA = Instant.parse("2001-01-01T00:00:00Z");
    private static final Instant CUTOFF = day(10);
    private static final String AN_INSTANT = "an ISO-8601 instant such as 2026-09-07T00:00:00Z";

    @Inject
    TestRunRepository repository;

    @Inject
    AuditService auditService;

    @TestHTTPResource("/api/tests")
    URI tests;

    private final List<String> stored = new ArrayList<>();
    private Instant started;

    @BeforeEach
    void start() {
        started = Instant.now();
    }

    @AfterEach
    void deleteTheRuns() {
        stored.forEach(repository::delete);
        stored.clear();
    }

    @Test
    void createdBeforeIsRequired() {
        String old = store(TaskStatus.DONE, day(1));

        refused(given(), "createdBefore is required: " + AN_INSTANT);
        refused(given().queryParam("createdBefore", " "), "createdBefore is required: " + AN_INSTANT);

        assertStored(old);
    }

    @ParameterizedTest
    @ValueSource(strings = {"2001-01-11", "2001-01-11T00:00:00", "30d", "yesterday"})
    void createdBeforeMustBeAnInstant(String value) {
        String old = store(TaskStatus.DONE, day(1));

        refused(given().queryParam("createdBefore", value), "createdBefore must be " + AN_INSTANT);

        assertStored(old);
    }

    /**
     * Only a finished run can be pruned, so a status naming any other refuses
     * the whole call, even beside DONE, rather than being dropped from it.
     */
    @ParameterizedTest
    @ValueSource(strings = {"RUNNING", "PENDING", "STOPPING", "CANCELLED", "DONE,FAILED"})
    void aStatusOtherThanDoneOrFailedIsRefused(String value) {
        String old = store(TaskStatus.DONE, day(1));

        refused(
                given().queryParam("createdBefore", CUTOFF.toString()).queryParam("status", "DONE", value),
                "status must be DONE or FAILED: only finished runs can be pruned");

        assertStored(old);
    }

    @ParameterizedTest
    @ValueSource(strings = {"0", "-1", "1001", "ten"})
    void aLimitOutsideOneToAThousandIsRefused(String value) {
        String old = store(TaskStatus.DONE, day(1));

        refused(
                given().queryParam("createdBefore", CUTOFF.toString()).queryParam("limit", value),
                "limit must be from 1 to 1000");

        assertStored(old);
    }

    /**
     * Bound as a boolean, dryRun=yes would be false, and the call would delete
     * what the caller meant only to count.
     */
    @ParameterizedTest
    @ValueSource(strings = {"yes", "1", "on"})
    void aDryRunOtherThanTrueOrFalseIsRefused(String value) {
        String old = store(TaskStatus.DONE, day(1));

        refused(
                given().queryParam("createdBefore", CUTOFF.toString()).queryParam("dryRun", value),
                "dryRun must be true or false");

        assertStored(old);
    }

    /**
     * A dry run counts and deletes nothing. The whole answer is checked, as
     * the CronJob reads it: compact, so its sed finds "deleted" and
     * "remaining" without a JSON parser.
     */
    @Test
    void aDryRunCountsAndDeletesNothing() {
        List<String> old = List.of(
                store(TaskStatus.DONE, day(1)), store(TaskStatus.FAILED, day(2)), store(TaskStatus.DONE, day(3)));

        String answer = given().queryParam("createdBefore", CUTOFF.toString())
                .queryParam("dryRun", "true")
                .when()
                .delete("/api/tests")
                .then()
                .statusCode(200)
                .extract()
                .asString();

        assertEquals(
                "{\"createdBefore\":\"2001-01-11T00:00:00Z\",\"statuses\":[\"DONE\",\"FAILED\"],\"dryRun\":true,"
                        + "\"matched\":3,\"deleted\":0,\"remaining\":3}",
                answer);
        old.forEach(this::assertStored);
        assertEquals(Map.of(), auditRows());
    }

    /**
     * Only DONE and FAILED runs created before the cutoff go, oldest first
     * and at most limit per call, and remaining says what the next call has
     * left. Runs that have not finished survive, however old, as do runs
     * created at the cutoff or after it. The runs are stored out of order, so
     * the order is the query's, not the store's.
     */
    @Test
    void deletesTheOldestFinishedRunsFirstUpToTheLimit() {
        String third = store(TaskStatus.DONE, day(3));
        String oldest = store(TaskStatus.DONE, day(1));
        String cancelled = store(TaskStatus.FAILED, day(2));
        List<String> survivors = List.of(
                store(TaskStatus.PENDING, day(0)),
                store(TaskStatus.STOPPING, day(0)),
                store(TaskStatus.DONE, CUTOFF),
                store(TaskStatus.FAILED, day(11)));

        JsonPath first = prune(given().queryParam("limit", "2"));

        assertEquals(List.of("DONE", "FAILED"), first.getList("statuses"));
        assertFalse(first.getBoolean("dryRun"));
        assertEquals(3, first.getLong("matched"));
        assertEquals(2, first.getInt("deleted"));
        assertEquals(1, first.getLong("remaining"));
        assertGone(oldest);
        assertGone(cancelled);
        assertStored(third);

        JsonPath second = prune(given().queryParam("limit", "2"));

        assertEquals(1, second.getLong("matched"));
        assertEquals(1, second.getInt("deleted"));
        assertEquals(0, second.getLong("remaining"));
        assertGone(third);

        JsonPath last = prune(given());

        assertEquals(0, last.getLong("matched"));
        assertEquals(0, last.getInt("deleted"));
        assertEquals(0, last.getLong("remaining"));
        survivors.forEach(this::assertStored);

        String row = "DELETE: retention: created before 2001-01-11T00:00:00Z";
        assertEquals(Map.of(oldest, row, cancelled, row, third, row), auditRows());
    }

    /** status is case-insensitive, and a run of a status not asked for stays. */
    @Test
    void aStatusFilterPrunesOnlyThatStatus() {
        String done = store(TaskStatus.DONE, day(1));
        String failed = store(TaskStatus.FAILED, day(2));

        JsonPath answer = prune(given().queryParam("status", "failed"));

        assertEquals(List.of("FAILED"), answer.getList("statuses"));
        assertEquals(1, answer.getLong("matched"));
        assertEquals(1, answer.getInt("deleted"));
        assertEquals(0, answer.getLong("remaining"));
        assertGone(failed);
        assertStored(done);
        assertEquals(Map.of(failed, "DELETE: retention: created before 2001-01-11T00:00:00Z"), auditRows());
    }

    @Test
    void statusesAreAnsweredDoneThenFailedAndOnce() {
        JsonPath answer =
                prune(given().queryParam("status", "FAILED", "done", "DONE").queryParam("dryRun", "true"));

        assertEquals(List.of("DONE", "FAILED"), answer.getList("statuses"));
    }

    /**
     * The cutoff is the instant given, whatever its offset, and the answer
     * gives it in UTC. 02:00 at +02:00 is midnight UTC: the run created at
     * 01:00 UTC is after it.
     */
    @Test
    void theCutoffIsTheInstantGivenWhateverItsOffset() {
        store(TaskStatus.DONE, Instant.parse("2001-01-10T23:00:00Z"));
        store(TaskStatus.DONE, Instant.parse("2001-01-11T01:00:00Z"));

        JsonPath answer = given().queryParam("createdBefore", "2001-01-11T02:00:00+02:00")
                .queryParam("dryRun", "true")
                .when()
                .delete("/api/tests")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath();

        assertEquals("2001-01-11T00:00:00Z", answer.getString("createdBefore"));
        assertEquals(1, answer.getLong("matched"));
    }

    /** What the CronJob's curl sends: a DELETE with no body and no Content-Type. */
    @Test
    void aDeleteWithNoContentTypeIsAnswered() throws Exception {
        String done = store(TaskStatus.DONE, day(1));

        HttpResponse<String> answer;
        try (HttpClient client = HttpClient.newHttpClient()) {
            answer = client.send(
                    HttpRequest.newBuilder(URI.create(tests + "?createdBefore=" + CUTOFF + "&limit=1000"))
                            .DELETE()
                            .build(),
                    HttpResponse.BodyHandlers.ofString());
        }

        assertEquals(200, answer.statusCode(), answer.body());
        assertTrue(answer.body().contains("\"deleted\":1,\"remaining\":0"), answer.body());
        assertGone(done);
    }

    private static Instant day(int n) {
        return ERA.plus(n, ChronoUnit.DAYS);
    }

    /** Stores a LOAD run with this status, created at {@code createdAt}, for deletion after the test. */
    private String store(TaskStatus status, Instant createdAt) {
        TestRun run = new TestRun(TestType.LOAD, null)
                .withBackend("native")
                .withStatus(status)
                .withCreatedAt(createdAt.toString());
        repository.save(run);
        stored.add(run.getId());
        return run.getId();
    }

    /** Prunes what was created before {@link #CUTOFF}, with whatever else {@code request} asks. */
    private static JsonPath prune(RequestSpecification request) {
        return request.queryParam("createdBefore", CUTOFF.toString())
                .when()
                .delete("/api/tests")
                .then()
                .statusCode(200)
                .body("createdBefore", is(CUTOFF.toString()))
                .extract()
                .jsonPath();
    }

    private static void refused(RequestSpecification request, String message) {
        request.when()
                .delete("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", is(message));
    }

    private void assertStored(String id) {
        assertTrue(isStored(id), "run " + id + " is still stored");
    }

    private void assertGone(String id) {
        assertFalse(isStored(id), "run " + id + " is deleted");
    }

    /**
     * Read in a transaction of its own. Outside one, a test's reads share one
     * persistence context for the whole test, which keeps a run it read even
     * after a call to the API deleted it.
     */
    private boolean isStored(String id) {
        return QuarkusTransaction.requiringNew()
                .call(() -> repository.findById(id).isPresent());
    }

    /**
     * The audit rows this test wrote of its runs, as each run's id to the
     * row's action and details. A run with two rows fails here, on the
     * duplicate key.
     */
    private Map<String, String> auditRows() {
        return auditService.list(500, "test", started.toString()).stream()
                .filter(row -> stored.contains((String) row.get("target")))
                .collect(
                        toMap(row -> (String) row.get("target"), row -> row.get("action") + ": " + row.get("details")));
    }
}
