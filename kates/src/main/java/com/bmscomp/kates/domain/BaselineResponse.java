package com.bmscomp.kates.domain;

import java.time.Instant;

public record BaselineResponse(
    String testType,
    String runId,
    Instant setAt
) {}
