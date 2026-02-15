package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	scaffoldType   string
	scaffoldOutput string
)

var testScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Generate a ready-to-edit YAML scenario template",
	Example: `  kates test scaffold --type LOAD
  kates test scaffold --type STRESS -o stress-test.yaml
  kates test scaffold --type SPIKE
  kates test scaffold --type ENDURANCE`,
	RunE: func(cmd *cobra.Command, args []string) error {
		t := strings.ToUpper(scaffoldType)
		tmpl, ok := scaffoldTemplates[t]
		if !ok {
			return cmdErr(fmt.Sprintf("Unknown test type: %s. Use 'kates test types' to list available types.", scaffoldType))
		}

		content := tmpl()

		if scaffoldOutput != "" {
			if err := os.WriteFile(scaffoldOutput, []byte(content), 0644); err != nil {
				return cmdErr("Failed to write file: " + err.Error())
			}
			output.Success(fmt.Sprintf("Scaffold written to %s", scaffoldOutput))
			output.Hint("Edit the file, then run: kates test apply -f " + scaffoldOutput)
			return nil
		}

		fmt.Println(content)
		return nil
	},
}

var scaffoldTemplates = map[string]func() string{
	"LOAD":       scaffoldLoad,
	"STRESS":     scaffoldStress,
	"SPIKE":      scaffoldSpike,
	"ENDURANCE":  scaffoldEndurance,
	"VOLUME":     scaffoldVolume,
	"CAPACITY":   scaffoldCapacity,
	"ROUND_TRIP": scaffoldRoundTrip,
	"INTEGRITY":  scaffoldIntegrity,
}

func scaffoldLoad() string {
	return `# LOAD TEST — Steady-state throughput with producers and consumers
# Measures baseline performance under a constant, controlled load.
#
# Usage: kates test apply -f load-test.yaml --wait

scenarios:
  - name: "Baseline Load Test"
    type: LOAD
    spec:
      records: 1000000
      parallelProducers: 2
      numConsumers: 2
      recordSizeBytes: 1024
      durationSeconds: 300
      topic: "load-benchmark"
      # Producer tuning
      acks: "all"
      batchSize: 65536
      lingerMs: 5
      compressionType: "lz4"
      # Consumer tuning
      consumerGroup: "load-cg"
      fetchMinBytes: 1
      fetchMaxWaitMs: 500
      # Topic settings
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
    validate:
      maxP99LatencyMs: 50
      minThroughputRecPerSec: 10000
`
}

func scaffoldStress() string {
	return `# STRESS TEST — Graduated ramp to find the breaking point
# Increases throughput in steps until the cluster saturates.
#
# Usage: kates test apply -f stress-test.yaml --wait

scenarios:
  - name: "Throughput Ramp – 10K"
    type: STRESS
    spec:
      records: 500000
      recordSizeBytes: 1024
      targetThroughput: 10000
      durationSeconds: 60
      topic: "stress-benchmark"
      acks: "1"
      batchSize: 131072
      lingerMs: 10
      compressionType: "lz4"
      partitions: 6

  - name: "Throughput Ramp – 50K"
    type: STRESS
    spec:
      records: 500000
      recordSizeBytes: 1024
      targetThroughput: 50000
      durationSeconds: 60
      topic: "stress-benchmark"
      acks: "1"
      batchSize: 131072
      lingerMs: 10
      partitions: 6
    validate:
      maxP99LatencyMs: 100

  - name: "Throughput Ramp – Unlimited"
    type: STRESS
    spec:
      records: 1000000
      recordSizeBytes: 1024
      durationSeconds: 60
      topic: "stress-benchmark"
      acks: "1"
      batchSize: 131072
      lingerMs: 10
      partitions: 6
`
}

func scaffoldSpike() string {
	return `# SPIKE TEST — Baseline → burst → recovery
# Validates cluster behavior under sudden traffic spikes.
#
# Usage: kates test apply -f spike-test.yaml --wait

scenarios:
  - name: "Spike – Baseline Phase"
    type: LOAD
    spec:
      records: 60000
      targetThroughput: 1000
      durationSeconds: 60
      topic: "spike-benchmark"
      partitions: 6
    validate:
      maxP99LatencyMs: 20

  - name: "Spike – Burst Phase"
    type: STRESS
    spec:
      records: 1500000
      parallelProducers: 3
      durationSeconds: 120
      topic: "spike-benchmark"
      acks: "1"
      batchSize: 131072
      lingerMs: 10
      partitions: 6

  - name: "Spike – Recovery Phase"
    type: LOAD
    spec:
      records: 60000
      targetThroughput: 1000
      durationSeconds: 60
      topic: "spike-benchmark"
      partitions: 6
    validate:
      maxP99LatencyMs: 30
`
}

func scaffoldEndurance() string {
	return `# ENDURANCE TEST — Long-running soak test
# Detects memory leaks, GC pressure, and log segment issues over time.
#
# Usage: kates test apply -f endurance-test.yaml --wait

scenarios:
  - name: "1-Hour Endurance Run"
    type: ENDURANCE
    spec:
      targetThroughput: 5000
      recordSizeBytes: 1024
      durationSeconds: 3600
      topic: "endurance-benchmark"
      parallelProducers: 2
      numConsumers: 2
      consumerGroup: "endurance-cg"
      acks: "all"
      compressionType: "lz4"
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
    validate:
      maxP99LatencyMs: 100
      minThroughputRecPerSec: 4000
`
}

func scaffoldVolume() string {
	return `# VOLUME TEST — Large message and high count workloads
# Tests both large message handling and high message count throughput.
#
# Usage: kates test apply -f volume-test.yaml --wait

scenarios:
  - name: "Volume – Large Messages (100KB)"
    type: VOLUME
    spec:
      records: 50000
      recordSizeBytes: 102400
      durationSeconds: 300
      topic: "volume-large"
      acks: "all"
      batchSize: 131072
      partitions: 6

  - name: "Volume – High Count (5M × 1KB)"
    type: VOLUME
    spec:
      records: 5000000
      recordSizeBytes: 1024
      durationSeconds: 300
      topic: "volume-count"
      acks: "1"
      batchSize: 131072
      lingerMs: 10
      compressionType: "zstd"
      partitions: 6
`
}

func scaffoldCapacity() string {
	return `# CAPACITY TEST — Step-probe to find maximum throughput
# Increases load in steps to identify the cluster's saturation point.
#
# Usage: kates test apply -f capacity-test.yaml --wait

scenarios:
  - name: "Capacity Probe – 5K msg/s"
    type: CAPACITY
    spec:
      records: 200000
      targetThroughput: 5000
      recordSizeBytes: 1024
      durationSeconds: 60
      topic: "capacity-benchmark"
      partitions: 6
    validate:
      maxP99LatencyMs: 20

  - name: "Capacity Probe – 20K msg/s"
    type: CAPACITY
    spec:
      records: 200000
      targetThroughput: 20000
      recordSizeBytes: 1024
      durationSeconds: 60
      topic: "capacity-benchmark"
      partitions: 6
    validate:
      maxP99LatencyMs: 50

  - name: "Capacity Probe – 80K msg/s"
    type: CAPACITY
    spec:
      records: 200000
      targetThroughput: 80000
      recordSizeBytes: 1024
      durationSeconds: 60
      topic: "capacity-benchmark"
      partitions: 6

  - name: "Capacity Probe – Unlimited"
    type: CAPACITY
    spec:
      records: 500000
      recordSizeBytes: 1024
      durationSeconds: 60
      topic: "capacity-benchmark"
      partitions: 6
`
}

func scaffoldRoundTrip() string {
	return `# ROUND TRIP TEST — End-to-end produce-then-consume latency
# Measures the full path from producer send to consumer receive.
#
# Usage: kates test apply -f roundtrip-test.yaml --wait

scenarios:
  - name: "Round Trip Latency Test"
    type: ROUND_TRIP
    spec:
      records: 100000
      targetThroughput: 1000
      recordSizeBytes: 1024
      durationSeconds: 120
      topic: "roundtrip-benchmark"
      acks: "all"
      partitions: 6
      replicationFactor: 3
    validate:
      maxP99LatencyMs: 30
      maxAvgLatencyMs: 10
`
}

func scaffoldIntegrity() string {
	return `# INTEGRITY TEST — Data loss and duplication verification
# Produces sequenced records, consumes them back, and reconciles
# to detect lost, duplicated, or out-of-order records.
#
# Usage: kates test apply -f integrity-test.yaml --wait

scenarios:
  - name: "Data Integrity Verification"
    type: INTEGRITY
    spec:
      records: 500000
      parallelProducers: 1
      numConsumers: 1
      recordSizeBytes: 512
      durationSeconds: 300
      topic: "integrity-benchmark"
      acks: "all"
      batchSize: 65536
      lingerMs: 5
      compressionType: "lz4"
      consumerGroup: "integrity-cg"
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
    validate:
      maxDataLossPercent: 0
`
}

func init() {
	testScaffoldCmd.Flags().StringVar(&scaffoldType, "type", "LOAD", "Test type to scaffold (LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY)")
	testScaffoldCmd.Flags().StringVarP(&scaffoldOutput, "output", "o", "", "Write scaffold to file instead of stdout")
	testCmd.AddCommand(testScaffoldCmd)
}
