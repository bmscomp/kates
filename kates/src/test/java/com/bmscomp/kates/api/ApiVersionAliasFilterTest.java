package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;

import io.quarkus.test.junit.QuarkusTest;
import io.restassured.response.Response;
import org.junit.jupiter.api.Test;

/**
 * Proves /api/v1 is a working, non-breaking alias: the versioned path reaches
 * the same handler as the unversioned one, and unknown versioned paths still
 * 404. Assertions compare the versioned vs unversioned status rather than a
 * fixed code, because /api/health returns 500 without a reachable broker in
 * the unit environment — the point is that both paths resolve to the same
 * handler, whatever it returns. Security is disabled in the %test profile.
 */
@QuarkusTest
class ApiVersionAliasFilterTest {

    @Test
    void versionedPathReachesSameHandlerAsUnversioned() {
        Response direct = given().when().get("/api/health");
        Response aliased = given().when().get("/api/v1/health");
        // Route resolved (not 404) and both hit the same handler → same status.
        assertNotEquals(404, direct.statusCode(), "/api/health should resolve");
        assertEquals(
                direct.statusCode(),
                aliased.statusCode(),
                "/api/v1/health must alias to /api/health (same handler, same status)");
    }

    @Test
    void versionedUnknownPathStill404s() {
        given().when().get("/api/v1/definitely-not-a-route").then().statusCode(404);
    }
}
