# Tutorial: CI/CD Quality Gate for Kafka Resilience

This tutorial shows you how to integrate Kates into your CI/CD pipeline so that a Kafka performance regression or resilience failure automatically blocks the deployment. By the end, you will have a GitHub Actions workflow that runs a resilience test, checks the grade, and fails the build if the cluster does not meet your SLA.

## The Concept

A CI/CD quality gate works like this:

1. Your pipeline deploys the new application version to a staging Kafka cluster
2. Kates runs a performance test and a disruption test against the staging cluster
3. If the SLA grade is below the threshold, the pipeline fails and the deploy is rejected
4. If the grade meets the threshold, the deploy proceeds to production

The key insight is that Kafka performance regressions are often caused by application changes, not infrastructure changes. A new consumer that joins a group, a producer that increases throughput, a schema change that inflates message size — these can degrade cluster performance. The quality gate catches these regressions before they reach production.

## Prerequisites

- Kates deployed in your staging Kubernetes cluster
- GitHub Actions (or any CI/CD platform that can run shell scripts)
- `curl` and `jq` available in the CI runner

## Step 1: Create the Test Specifications

Create two JSON files in your repository:

**`ci/perf-test.json`** — the performance test:

```json
{
  "type": "LOAD",
  "spec": {
    "topic": "ci-perf-gate",
    "numRecords": 200000,
    "throughput": 25000,
    "recordSize": 1024,
    "partitions": 6,
    "replicationFactor": 3,
    "acks": "all",
    "numProducers": 2
  }
}
```

**`ci/disruption-plan.json`** — the disruption test:

```json
{
  "planName": "ci-resilience-gate",
  "steps": [
    {
      "stepName": "broker-kill",
      "faultSpec": {
        "experimentName": "ci-kill-leader",
        "disruptionType": "POD_KILL",
        "targetTopic": "ci-perf-gate",
        "targetPartition": 0,
        "gracePeriodSec": 0
      },
      "observeAfterSec": 90,
      "steadyStateSec": 10
    }
  ],
  "maxAffectedBrokers": 1,
  "sla": {
    "maxP99LatencyMs": 100,
    "minThroughputRecPerSec": 15000,
    "maxErrorRate": 1.0,
    "maxRtoMs": 120000,
    "maxDataLossPercent": 0
  }
}
```

Commit both files to your repository.

## Step 2: Write the GitHub Actions Workflow

Create `.github/workflows/kafka-gate.yml`:

```yaml
name: Kafka Performance & Resilience Gate

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  kafka-gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Wait for staging deployment
        run: |
          echo "Waiting for staging deploy to stabilize..."
          sleep 30

      - name: Run performance test
        id: perf
        run: |
          RESULT=$(curl -sf -X POST http://kates.staging:8080/api/tests \
            -H 'Content-Type: application/json' \
            -d @ci/perf-test.json)
          RUN_ID=$(echo "$RESULT" | jq -r '.runId')
          echo "run_id=$RUN_ID" >> $GITHUB_OUTPUT

          # Poll until complete
          for i in $(seq 1 60); do
            STATUS=$(curl -sf http://kates.staging:8080/api/tests/$RUN_ID | jq -r '.status')
            if [ "$STATUS" = "DONE" ]; then break; fi
            if [ "$STATUS" = "ERROR" ]; then
              echo "::error::Performance test failed"
              exit 1
            fi
            sleep 5
          done

      - name: Export JUnit XML
        run: |
          mkdir -p test-results
          curl -sf "http://kates.staging:8080/api/tests/${{ steps.perf.outputs.run_id }}/export?format=junit-xml" \
            > test-results/kafka-perf.xml

      - name: Run disruption test
        id: disruption
        run: |
          REPORT=$(curl -sf -X POST http://kates.staging:8080/api/disruptions \
            -H 'Content-Type: application/json' \
            -d @ci/disruption-plan.json)
          REPORT_ID=$(echo "$REPORT" | jq -r '.reportId')
          echo "report_id=$REPORT_ID" >> $GITHUB_OUTPUT

          # Poll until complete
          for i in $(seq 1 60); do
            STATUS=$(curl -sf http://kates.staging:8080/api/disruptions/$REPORT_ID | jq -r '.status')
            if [ "$STATUS" = "COMPLETED" ]; then break; fi
            if [ "$STATUS" = "ERROR" ]; then
              echo "::error::Disruption test failed"
              exit 1
            fi
            sleep 5
          done

      - name: Check resilience grade
        run: |
          GRADE=$(curl -sf http://kates.staging:8080/api/disruptions/${{ steps.disruption.outputs.report_id }} \
            | jq -r '.overallGrade')
          echo "## Kafka Resilience Grade: $GRADE" >> $GITHUB_STEP_SUMMARY

          if [[ "$GRADE" =~ ^[DEF]$ ]]; then
            echo "::error::Resilience grade $GRADE is below minimum threshold (C)"

            # Print violations for debugging
            curl -sf http://kates.staging:8080/api/disruptions/${{ steps.disruption.outputs.report_id }} \
              | jq '.slaVerdict.violations[]'

            exit 1
          fi

          echo "✅ Resilience grade $GRADE meets threshold"

      - name: Publish test results
        if: always()
        uses: dorny/test-reporter@v1
        with:
          name: Kafka Performance
          path: test-results/kafka-perf.xml
          reporter: java-junit
```

## Step 3: Understand the Pipeline Flow

The workflow does four things:

1. **Submits a LOAD test** to measure baseline performance against the staging cluster. The test creates its own topic, runs the benchmark, and produces a `TestReport`.

2. **Exports JUnit XML** so that the performance results appear in the GitHub Actions test report. SLA violations show up as test failures with the metric name, threshold, and actual value.

3. **Runs a disruption test** that kills the leader for partition 0 and watches the cluster for 90 seconds. The disruption report includes the SLA grade.

4. **Checks the grade** and fails the build if it is D, E, or F. Grades A-C allow the deploy to proceed. The violations are logged for debugging.

## Step 4: Adapt for Other CI/CD Platforms

### Jenkins

```groovy
pipeline {
    agent any
    stages {
        stage('Kafka Gate') {
            steps {
                script {
                    def result = sh(script: """
                        curl -sf -X POST http://kates.staging:8080/api/disruptions \
                          -H 'Content-Type: application/json' \
                          -d @ci/disruption-plan.json
                    """, returnStdout: true)
                    def reportId = readJSON(text: result).reportId
                    // Poll and check grade...
                }
                junit 'test-results/kafka-perf.xml'
            }
        }
    }
}
```

### GitLab CI

```yaml
kafka-gate:
  stage: test
  script:
    - REPORT=$(curl -sf -X POST http://kates.staging:8080/api/disruptions -d @ci/disruption-plan.json)
    - REPORT_ID=$(echo "$REPORT" | jq -r '.reportId')
    - # Poll and check grade...
  artifacts:
    reports:
      junit: test-results/kafka-perf.xml
```

## Step 5: Tune Your Thresholds

Start with conservative thresholds and tighten them as your confidence grows:

| Phase | P99 Latency | Min Throughput | Max Error Rate | Min Grade |
|-------|-------------|----------------|----------------|-----------|
| Initial rollout | 200ms | 5,000 msg/sec | 5% | D (only block on catastrophic failure) |
| Established | 100ms | 15,000 msg/sec | 1% | C |
| Mature | 50ms | 25,000 msg/sec | 0.1% | B |
| Mission-critical | 20ms | 40,000 msg/sec | 0% | A |

The goal is to trend toward tighter thresholds as your cluster configuration stabilizes. Each tightening catches regressions that the previous threshold would have missed.

## What to Try Next

1. **Add trend tracking** — log each grade with the commit SHA and Kafka version to a file for historical analysis
2. **Run on a cron schedule** — use the `DisruptionScheduler` to run resilience tests nightly, independently of deployments
3. **Design a custom playbook** for your specific failure scenarios ([Tutorial: Custom Playbook](custom-playbook.md))
