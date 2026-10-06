package com.bmscomp.kates.chaos.litmus;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

@JsonIgnoreProperties(ignoreUnknown = true)
public class ChaosEngineSpec {

    public String engineState;
    public String chaosServiceAccount;
    public String annotationCheck;
    public AppInfo appinfo;
    public EngineComponents components;
    public List<Experiment> experiments;

    /** The chaos-runner pod's settings; the operator fills in what is left out. */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class EngineComponents {
        public Runner runner;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Runner {
        public Map<String, String> nodeSelector;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class AppInfo {
        public String appns;
        public String applabel;
        public String appkind;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Experiment {
        public String name;
        public ExperimentSpec spec;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class ExperimentSpec {
        public Components components;
        public List<Probe> probe;
    }

    /** The experiment pod's settings. */
    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Components {
        public List<EnvVar> env;
        public Map<String, String> nodeSelector;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class EnvVar {
        public String name;
        public String value;

        public EnvVar() {}

        public EnvVar(String name, String value) {
            this.name = name;
            this.value = value;
        }
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Probe {
        public String name;
        public String type;
        public String mode = "Continuous";
        public CmdProbe cmdProbe;
        public RunProperties runProperties;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class CmdProbe {
        public CmdProbeInputs inputs;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class CmdProbeInputs {
        public String command;
        public Comparator comparator;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Comparator {
        public String type = "string";
        public String criteria = "contains";
        public String value;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class RunProperties {
        public int probeTimeout = 5;
        public int interval = 5;
        public int retry = 1;

        public RunProperties() {}

        public RunProperties(int probeTimeout, int interval, int retry) {
            this.probeTimeout = probeTimeout;
            this.interval = interval;
            this.retry = retry;
        }
    }
}
