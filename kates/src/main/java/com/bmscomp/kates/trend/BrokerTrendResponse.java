package com.bmscomp.kates.trend;

import java.util.List;

import com.fasterxml.jackson.annotation.JsonInclude;

/**
 * Trend response scoped to a single broker's projected metrics across test runs.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class BrokerTrendResponse {

    private int brokerId;
    private String testType;
    private String metric;
    private double baseline;
    private List<TrendResponse.DataPoint> dataPoints;
    private List<TrendResponse.Regression> regressions;

    public int getBrokerId() {
        return brokerId;
    }

    public void setBrokerId(int brokerId) {
        this.brokerId = brokerId;
    }

    public String getTestType() {
        return testType;
    }

    public void setTestType(String testType) {
        this.testType = testType;
    }

    public String getMetric() {
        return metric;
    }

    public void setMetric(String metric) {
        this.metric = metric;
    }

    public double getBaseline() {
        return baseline;
    }

    public void setBaseline(double baseline) {
        this.baseline = baseline;
    }

    public List<TrendResponse.DataPoint> getDataPoints() {
        return dataPoints;
    }

    public void setDataPoints(List<TrendResponse.DataPoint> dataPoints) {
        this.dataPoints = dataPoints;
    }

    public List<TrendResponse.Regression> getRegressions() {
        return regressions;
    }

    public void setRegressions(List<TrendResponse.Regression> regressions) {
        this.regressions = regressions;
    }
}
