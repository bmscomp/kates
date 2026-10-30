package com.bmscomp.kates.compliance;

import com.fasterxml.jackson.annotation.JsonProperty;

/** What a constraint that does not hold does to the check: fail it, or only warn. */
public enum Severity {
    @JsonProperty("fail")
    FAIL,
    @JsonProperty("warn")
    WARN
}
