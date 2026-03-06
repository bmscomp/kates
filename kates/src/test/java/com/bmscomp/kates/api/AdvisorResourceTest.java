package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.AdvisorService;
import com.bmscomp.kates.service.TestRunRepository;

import java.util.Optional;

@QuarkusTest
class AdvisorResourceTest {

    @InjectMock
    TestRunRepository repository;

    @InjectMock
    AdvisorService advisorService;

    @Test
    void analyzeReturnsRecommendations() {
        TestRun run = new TestRun(TestType.LOAD, null).withId("run-1");
        Mockito.when(repository.findById("run-1")).thenReturn(Optional.of(run));
        Mockito.when(advisorService.analyze(run)).thenReturn(List.of(
                new AdvisorService.Recommendation("HIGH", "Increase batch.size", "Set batch.size=65536", "Current value too low")));

        var response = RestAssured.given()
                .when().get("/api/tests/run-1/advisor")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals("HIGH", response.getString("[0].severity"));
    }

    @Test
    void analyzeReturns404ForMissingRun() {
        Mockito.when(repository.findById("missing")).thenReturn(Optional.empty());

        RestAssured.given()
                .when().get("/api/tests/missing/advisor")
                .then().statusCode(404);
    }
}
