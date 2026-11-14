package com.bmscomp.kates.quorum;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.not;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import io.quarkus.test.junit.QuarkusMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** A member that does not lead answers every write 503, and still serves reads and its drain. */
@QuarkusTest
class QuorumGateTest {

    @BeforeEach
    void follower() {
        Quorum quorum = mock(Quorum.class);
        when(quorum.acceptsWrites()).thenReturn(false);
        when(quorum.isLeader()).thenReturn(false);
        when(quorum.leaderId()).thenReturn("kates-2");
        QuarkusMock.installMockForType(quorum, Quorum.class);
    }

    @Test
    void aWriteIsRefusedWithTheLeadersName() {
        given().contentType(ContentType.JSON)
                .body("{\"type\":\"LOAD\"}")
                .when()
                .post("/api/tests")
                .then()
                .statusCode(503)
                .header("Retry-After", not(""))
                .body("message", containsString("kates-2"));
    }

    @Test
    void theFilterAnswersBeforeAnyResourceDoes() {
        // A delete of no run would be 404: only the gate makes it 503.
        given().when().delete("/api/tests/no-such-run").then().statusCode(503);
    }

    @Test
    void readsAndTheDrainStillWork() {
        given().when().get("/api/tests").then().statusCode(200);
        given().contentType(ContentType.JSON)
                .body("{\"draining\":false}")
                .when()
                .put("/api/drain")
                .then()
                .statusCode(200);
    }
}
