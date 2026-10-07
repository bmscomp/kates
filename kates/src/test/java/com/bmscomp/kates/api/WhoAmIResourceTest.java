package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.contains;
import static org.hamcrest.Matchers.equalTo;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.security.Scopes;

/**
 * With API security off, as in the %test profile, every caller is anonymous
 * with every scope, and whoami says security is off. ApiKeyAuthenticationTest
 * covers the key's principal with security on.
 */
@QuarkusTest
class WhoAmIResourceTest {

    @Test
    void anyCallerIsAnonymousWithEveryScopeWhenSecurityIsOff() {
        given().when()
                .get("/api/whoami")
                .then()
                .statusCode(200)
                .body("principal", equalTo("anonymous"))
                .body("principalType", equalTo("human"))
                .body("scopes", contains(Scopes.ALL.toArray()))
                .body("securityEnabled", equalTo(false));
    }

    @Test
    void openApiIsServedWhenSecurityIsOff() {
        given().when().get("/q/openapi").then().statusCode(200);
    }
}
