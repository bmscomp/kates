package com.klster.kates.trogdor.spec;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

@JsonInclude(JsonInclude.Include.NON_NULL)
public abstract class TrogdorSpec {

    @JsonProperty("class")
    private String specClass;

    private long startMs;
    private long durationMs;

    protected TrogdorSpec(String specClass, long durationMs) {
        this.specClass = specClass;
        this.startMs = System.currentTimeMillis();
        this.durationMs = durationMs;
    }

    public String getSpecClass() {
        return specClass;
    }

    public void setSpecClass(String specClass) {
        this.specClass = specClass;
    }

    public long getStartMs() {
        return startMs;
    }

    public void setStartMs(long startMs) {
        this.startMs = startMs;
    }

    public long getDurationMs() {
        return durationMs;
    }

    public void setDurationMs(long durationMs) {
        this.durationMs = durationMs;
    }
}
