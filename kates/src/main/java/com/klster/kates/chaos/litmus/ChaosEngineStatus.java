package com.klster.kates.chaos.litmus;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

@JsonIgnoreProperties(ignoreUnknown = true)
public class ChaosEngineStatus {

    public String engineStatus;
}
