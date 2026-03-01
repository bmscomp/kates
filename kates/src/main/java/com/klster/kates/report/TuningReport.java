package com.klster.kates.report;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class TuningReport {

    private String testType;
    private String parameterName;
    private List<TuningStep> steps;
    private int bestStepIndex;
    private String recommendation;

    public String getTestType() {
        return testType;
    }

    public void setTestType(String testType) {
        this.testType = testType;
    }

    public String getParameterName() {
        return parameterName;
    }

    public void setParameterName(String parameterName) {
        this.parameterName = parameterName;
    }

    public List<TuningStep> getSteps() {
        return steps;
    }

    public void setSteps(List<TuningStep> steps) {
        this.steps = steps;
    }

    public int getBestStepIndex() {
        return bestStepIndex;
    }

    public void setBestStepIndex(int bestStepIndex) {
        this.bestStepIndex = bestStepIndex;
    }

    public String getRecommendation() {
        return recommendation;
    }

    public void setRecommendation(String recommendation) {
        this.recommendation = recommendation;
    }

    public static class TuningStep {
        private int stepIndex;
        private String label;
        private Map<String, Object> config;
        private ReportSummary metrics;
        private long topicCleanupMs;

        public int getStepIndex() {
            return stepIndex;
        }

        public void setStepIndex(int stepIndex) {
            this.stepIndex = stepIndex;
        }

        public String getLabel() {
            return label;
        }

        public void setLabel(String label) {
            this.label = label;
        }

        public Map<String, Object> getConfig() {
            return config;
        }

        public void setConfig(Map<String, Object> config) {
            this.config = config;
        }

        public ReportSummary getMetrics() {
            return metrics;
        }

        public void setMetrics(ReportSummary metrics) {
            this.metrics = metrics;
        }

        public long getTopicCleanupMs() {
            return topicCleanupMs;
        }

        public void setTopicCleanupMs(long topicCleanupMs) {
            this.topicCleanupMs = topicCleanupMs;
        }
    }
}
