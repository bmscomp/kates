package com.klster.kates.chaos.litmus;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

@JsonIgnoreProperties(ignoreUnknown = true)
public class ChaosResultStatus {

    public ExperimentStatus experimentStatus;

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class ExperimentStatus {
        public String verdict;
        public String failStep;
        public String phase;
        public String probeSuccessPercentage;
    }
}
