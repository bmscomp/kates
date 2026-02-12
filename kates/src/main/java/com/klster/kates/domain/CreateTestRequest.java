package com.klster.kates.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class CreateTestRequest {

    private TestType type;
    private TestSpec spec;

    public CreateTestRequest() {
    }

    public TestType getType() {
        return type;
    }

    public void setType(TestType type) {
        this.type = type;
    }

    public TestSpec getSpec() {
        return spec;
    }

    public void setSpec(TestSpec spec) {
        this.spec = spec;
    }
}
