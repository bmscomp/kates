package com.bmscomp.kates.security;

import static io.restassured.RestAssured.given;

import java.util.Map;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

/**
 * The auth filter had no test at all. Security is disabled in the default
 * %test profile, so this profile switches it on with a real key and drives
 * the filter over HTTP: missing key → 401, wrong key → 403, correct key
 * (both header forms) → 200, public paths stay open.
 */
@QuarkusTest
@TestProfile(ApiKeyAuthFilterTest.SecurityEnabledProfile.class)
class ApiKeyAuthFilterTest {

    private static final String KEY = "test-secret-key-for-filter-test";

    public static class SecurityEnabledProfile implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of("kates.api.security-enabled", "true", "kates.api.key", KEY);
        }
    }

    @Test
    void missingKeyIsRejectedWith401() {
        given().when().get("/api/webhooks").then().statusCode(401);
    }

    @Test
    void wrongKeyIsRejectedWith403() {
        given().header("X-API-Key", "not-the-key")
                .when()
                .get("/api/webhooks")
                .then()
                .statusCode(403);
    }

    @Test
    void wrongBearerIsRejectedWith403() {
        given().header("Authorization", "Bearer not-the-key")
                .when()
                .get("/api/webhooks")
                .then()
                .statusCode(403);
    }

    @Test
    void correctBearerTokenIsAccepted() {
        given().header("Authorization", "Bearer " + KEY)
                .when()
                .get("/api/webhooks")
                .then()
                .statusCode(200);
    }

    @Test
    void correctApiKeyHeaderIsAccepted() {
        given().header("X-API-Key", KEY).when().get("/api/webhooks").then().statusCode(200);
    }

    @Test
    void publicHealthPathNeedsNoKey() {
        // /api/health is public: it must reach the handler without a key, i.e.
        // it is NOT rejected by the auth filter (401/403). The handler itself
        // returns 500 without a reachable broker in the unit environment, which
        // still proves the request got past authentication.
        int status = given().when().get("/api/health").then().extract().statusCode();
        org.junit.jupiter.api.Assertions.assertTrue(
                status != 401 && status != 403, "public health path must not be blocked by auth, got " + status);
    }
}
