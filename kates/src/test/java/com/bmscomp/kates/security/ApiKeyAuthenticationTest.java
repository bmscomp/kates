package com.bmscomp.kates.security;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.contains;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.hasItems;
import static org.hamcrest.Matchers.nullValue;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Map;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

/**
 * API-key authentication over HTTP, with security on (the default %test
 * profile switches it off): missing key → 401, wrong key → 403, the right key
 * in either header → 200, with the bodies the old JAX-RS filter sent. Public
 * paths stay open, a wrong key on them included; /q/openapi, which the filter
 * never saw, now needs the key.
 */
@QuarkusTest
@TestProfile(ApiKeyAuthenticationTest.SecurityEnabledProfile.class)
class ApiKeyAuthenticationTest {

    private static final String KEY = "test-secret-key-for-filter-test";

    public static class SecurityEnabledProfile implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of("kates.api.security-enabled", "true", "kates.api.key", KEY);
        }
    }

    @Test
    void missingKeyIsRejectedWith401() {
        given().when()
                .get("/api/webhooks")
                .then()
                .statusCode(401)
                .body("status", equalTo(401))
                .body("error", equalTo("Missing API key"))
                .body(
                        "message",
                        equalTo("Provide a token via 'Authorization: Bearer <key>' or 'X-API-Key: <key>' header"));
    }

    @Test
    void wrongKeyIsRejectedWith403() {
        given().header("X-API-Key", "not-the-key")
                .when()
                .get("/api/webhooks")
                .then()
                .statusCode(403)
                .body("status", equalTo(403))
                .body("error", equalTo("Invalid API key"))
                .body("message", equalTo("The provided API key is not valid"));
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
    void theVersionedPathNeedsTheKeyToo() {
        given().when().get("/api/v1/webhooks").then().statusCode(401);
        given().header("X-API-Key", KEY).when().get("/api/v1/webhooks").then().statusCode(200);
    }

    @Test
    void publicHealthPathNeedsNoKey() {
        // /api/health is public: it must reach the handler without a key, i.e.
        // it is NOT rejected by authentication (401/403). The handler itself
        // returns 500 without a reachable broker in the unit environment, which
        // still proves the request got past authentication.
        assertNotRefused(given().when().get("/api/health").then().extract().statusCode());
        // A wrong key is never looked at on a public path.
        assertNotRefused(given().header("X-API-Key", "not-the-key")
                .when()
                .get("/api/health")
                .then()
                .extract()
                .statusCode());
    }

    @Test
    void openApiNeedsTheKey() {
        // As on the REST endpoints: 401 without a key, 403 with a wrong one.
        given().when().get("/q/openapi").then().statusCode(401);
        given().header("X-API-Key", "not-the-key")
                .when()
                .get("/q/openapi")
                .then()
                .statusCode(403);
        given().header("X-API-Key", KEY).when().get("/q/openapi").then().statusCode(200);
    }

    @Test
    void probesAndMetricsNeedNoKey() {
        assertNotRefused(given().when().get("/q/health/live").then().extract().statusCode());
        assertNotRefused(given().when().get("/q/metrics").then().extract().statusCode());
    }

    @Test
    void whoAmINamesTheLegacyPrincipal() {
        given().header("Authorization", "Bearer " + KEY)
                .when()
                .get("/api/whoami")
                .then()
                .statusCode(200)
                .body("principal", equalTo("legacy"))
                .body("principalType", equalTo("human"))
                .body("scopes", contains(Scopes.ALL.toArray()))
                .body("scopes", hasItems("read", "admin", "chaos:run"))
                .body("allowedClusterIds", nullValue())
                .body("securityEnabled", equalTo(true));
        given().when().get("/api/whoami").then().statusCode(401);
        given().header("X-API-Key", "not-the-key")
                .when()
                .get("/api/whoami")
                .then()
                .statusCode(403);
    }

    private static void assertNotRefused(int status) {
        assertTrue(
                status != 401 && status != 403, "a public path must not be refused by authentication, got " + status);
    }
}
