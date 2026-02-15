package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	testTypeFlag   string
	testStatusFlag string
	testPageFlag   int
	testSizeFlag   int
)

var testCmd = &cobra.Command{
	Use:     "test",
	Aliases: []string{"t"},
	Short:   "Manage performance test runs",
}

var testListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List test runs with optional filters",
	Example: `  kates test list
  kates test list --type LOAD --status DONE
  kates test list --page 0 --size 10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paged, err := apiClient.ListTests(context.Background(), testTypeFlag, testStatusFlag, testPageFlag, testSizeFlag)
		if err != nil {
			return cmdErr("Failed to list tests: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(paged)
			return nil
		}

		output.Header("Test Runs")
		if len(paged.Content) == 0 {
			output.Hint("No test runs found.")
			output.Hint("Start one with: kates test create --type LOAD --records 100000")
			return nil
		}

		rows := make([][]string, 0, len(paged.Content))
		for _, run := range paged.Content {
			rows = append(rows, []string{
				truncID(run.ID),
				run.TestType,
				run.Status,
				run.Backend,
				formatTime(run.CreatedAt),
			})
		}
		output.Table([]string{"ID", "Type", "Status", "Backend", "Created"}, rows)

		totalPages := paged.TotalPages
		if totalPages == 0 && paged.TotalItems > 0 {
			totalPages = (paged.TotalItems + paged.Size - 1) / paged.Size
		}
		output.Hint(fmt.Sprintf("Page %d of %d · %d total runs", paged.Page+1, totalPages, paged.TotalItems))
		return nil
	},
}

var testGetCmd = &cobra.Command{
	Use:     "get <id>",
	Aliases: []string{"show", "inspect"},
	Short:   "Show details of a specific test run",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.GetTest(context.Background(), args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Test Run", fmt.Sprintf("%s · %s", result.TestType, truncID(args[0])))

		output.SubHeader("Details")
		output.KeyValue("ID", result.ID)
		output.KeyValue("Type", result.TestType)
		output.KeyValue("Status", output.StatusBadge(result.Status))
		output.KeyValue("Backend", result.Backend)
		output.KeyValue("Scenario", result.ScenarioName)
		output.KeyValue("Created", formatTime(result.CreatedAt))

		if len(result.Results) > 0 {
			output.SubHeader(fmt.Sprintf("Results (%d phases)", len(result.Results)))
			rows := make([][]string, 0, len(result.Results))
			for _, r := range result.Results {
				phase := r.PhaseName
				if phase == "" {
					phase = "main"
				}
				rows = append(rows, []string{
					phase,
					r.Status,
					fmtNum(r.RecordsSent),
					fmtFloat(r.ThroughputRecordsPerSec, 1),
					fmtFloat(r.AvgLatencyMs, 2),
					fmtFloat(r.P99LatencyMs, 2),
				})
			}
			output.Table(
				[]string{"Phase", "Status", "Records", "Throughput", "Avg Lat.", "P99 Lat."},
				rows,
			)

			for _, r := range result.Results {
				if r.Integrity != nil {
					ir := r.Integrity
					output.SubHeader("Data Integrity")
					output.KeyValue("Sent", fmtNum(float64(ir.TotalSent)))
					output.KeyValue("Acked", fmtNum(float64(ir.TotalAcked)))
					output.KeyValue("Consumed", fmtNum(float64(ir.TotalConsumed)))
					output.KeyValue("Lost", fmtNum(float64(ir.LostRecords)))
					output.KeyValue("Duplicates", fmtNum(float64(ir.DuplicateRecords)))
					output.KeyValue("Data Loss", fmt.Sprintf("%.4f%%", ir.DataLossPercent))
					if ir.ProducerRtoMs > 0 {
						output.KeyValue("Producer RTO", fmt.Sprintf("%.0f ms", ir.ProducerRtoMs))
					}
					if ir.ConsumerRtoMs > 0 {
						output.KeyValue("Consumer RTO", fmt.Sprintf("%.0f ms", ir.ConsumerRtoMs))
					}
					if ir.MaxRtoMs > 0 {
						output.KeyValue("Max RTO", fmt.Sprintf("%.0f ms", ir.MaxRtoMs))
					}
					if ir.RpoMs > 0 {
						output.KeyValue("RPO", fmt.Sprintf("%.0f ms", ir.RpoMs))
					}
					if ir.CrcVerified {
						output.KeyValue("CRC Failures", fmtNum(float64(ir.CrcFailures)))
					}
					if ir.OrderingVerified {
						output.KeyValue("Out of Order", fmtNum(float64(ir.OutOfOrderCount)))
					}
					modes := ""
					if ir.IdempotenceEnabled {
						modes += "idempotent "
					}
					if ir.TransactionsEnabled {
						modes += "transactional "
					}
					if modes != "" {
						output.KeyValue("Mode", modes)
					}
					verdict := ir.Verdict
					if verdict == "" {
						if ir.LostRecords == 0 {
							verdict = "PASS"
						} else {
							verdict = "DATA_LOSS"
						}
					}
					output.KeyValue("Verdict", output.StatusBadge(verdict))
					if len(ir.LostRanges) > 0 {
						output.SubHeader("Lost Ranges")
						lostRows := make([][]string, 0, len(ir.LostRanges))
						for _, lr := range ir.LostRanges {
							lostRows = append(lostRows, []string{
								fmt.Sprintf("%d", lr.FromSeq),
								fmt.Sprintf("%d", lr.ToSeq),
								fmt.Sprintf("%d", lr.Count),
							})
						}
						output.Table([]string{"From Seq", "To Seq", "Count"}, lostRows)
					}
					if len(ir.Timeline) > 0 {
						output.SubHeader("Integrity Timeline")
						maxEvents := 20
						start := 0
						if len(ir.Timeline) > maxEvents {
							start = len(ir.Timeline) - maxEvents
							output.Hint(fmt.Sprintf("  (showing last %d of %d events)", maxEvents, len(ir.Timeline)))
						}
						tlRows := make([][]string, 0, maxEvents)
						for _, ev := range ir.Timeline[start:] {
							tlRows = append(tlRows, []string{
								fmt.Sprintf("%d", ev.TimestampMs),
								ev.Type,
								ev.Detail,
							})
						}
						output.Table([]string{"Timestamp", "Type", "Detail"}, tlRows)
					}
					break
				}
			}
		}
		return nil
	},
}

var (
	createType              string
	createBackend           string
	createRecords           int
	createProducers         int
	createRecordSize        int
	createDuration          int
	createTopic             string
	createAcks              string
	createBatchSize         int
	createLingerMs          int
	createCompression       string
	createConsumers         int
	createReplicationFactor int
	createPartitions        int
	createMinISR            int
	createConsumerGroup     string
	createThroughput        int
	createFetchMinBytes     int
	createFetchMaxWaitMs    int
)

var testCreateCmd = &cobra.Command{
	Use:     "create",
	Aliases: []string{"run", "start"},
	Short:   "Start a new performance test",
	Example: `  kates test create --type LOAD --records 100000
  kates test create --type ENDURANCE --duration 300 --producers 4
  kates test create --type STRESS --records 500000 --acks 1 --compression zstd
  kates test create --type LOAD --records 100000 --consumers 4 --consumer-group perf-cg
  kates test create --type LOAD --records 100000 --throughput 10000 --fetch-min-bytes 1048576`,
	RunE: func(cmd *cobra.Command, args []string) error {
		req := &client.CreateTestRequest{
			TestType: strings.ToUpper(createType),
		}
		if createBackend != "" {
			req.Backend = createBackend
		}
		if hasSpecOverrides() {
			req.Spec = &client.TestSpec{
				Records:           createRecords,
				ParallelProducers: createProducers,
				RecordSizeBytes:   createRecordSize,
				DurationSeconds:   createDuration,
				Topic:             createTopic,
				Acks:              createAcks,
				BatchSize:         createBatchSize,
				LingerMs:          createLingerMs,
				CompressionType:   createCompression,
				NumConsumers:      createConsumers,
				ReplicationFactor: createReplicationFactor,
				Partitions:        createPartitions,
				MinInsyncReplicas: createMinISR,
				ConsumerGroup:     createConsumerGroup,
				TargetThroughput:  createThroughput,
				FetchMinBytes:     createFetchMinBytes,
				FetchMaxWaitMs:    createFetchMaxWaitMs,
			}
		}

		result, err := apiClient.CreateTest(context.Background(), req)
		if err != nil {
			return cmdErr("Failed to create test: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Success("Test created successfully")
		output.KeyValue("ID", result.ID)
		output.KeyValue("Type", result.TestType)
		output.KeyValue("Status", output.StatusBadge(result.Status))

		if createWait {
			fmt.Println()
			pollUntilDone(result.ID)
		} else {
			output.Hint("Track progress: kates test watch " + result.ID)
		}
		return nil
	},
}

var testDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Stop and delete a test run",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := apiClient.DeleteTest(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to delete test: " + err.Error())
		}
		output.Success("Test deleted: " + truncID(args[0]))
		return nil
	},
}

func hasSpecOverrides() bool {
	return createRecords > 0 || createProducers > 0 || createRecordSize > 0 ||
		createDuration > 0 || createTopic != "" || createAcks != "" ||
		createBatchSize > 0 || createLingerMs > 0 || createCompression != "" ||
		createConsumers > 0 || createReplicationFactor > 0 || createPartitions > 0 ||
		createMinISR > 0 || createConsumerGroup != "" || createThroughput > 0 ||
		createFetchMinBytes > 0 || createFetchMaxWaitMs > 0
}

var testTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available test types",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.TestTypes(context.Background())
		if err != nil {
			return cmdErr("Failed to get test types: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(types)
			return nil
		}
		output.Header("Test Types")
		rows := make([][]string, len(types))
		for i, t := range types {
			rows[i] = []string{t, describeType(t)}
		}
		output.Table([]string{"Type", "Description"}, rows)
		return nil
	},
}

var testBackendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "List available benchmark backends",
	RunE: func(cmd *cobra.Command, args []string) error {
		backends, err := apiClient.Backends(context.Background())
		if err != nil {
			return cmdErr("Failed to get backends: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(backends)
			return nil
		}
		output.Header("Benchmark Backends")
		rows := make([][]string, len(backends))
		for i, b := range backends {
			rows[i] = []string{b}
		}
		output.Table([]string{"Backend"}, rows)
		return nil
	},
}

func init() {
	testListCmd.Flags().StringVar(&testTypeFlag, "type", "", "Filter by test type (LOAD, ENDURANCE, BURST, etc.)")
	testListCmd.Flags().StringVar(&testStatusFlag, "status", "", "Filter by status (PENDING, RUNNING, DONE, FAILED)")
	testListCmd.Flags().IntVar(&testPageFlag, "page", 0, "Page number (0-indexed)")
	testListCmd.Flags().IntVar(&testSizeFlag, "size", 20, "Page size")

	testCreateCmd.Flags().StringVar(&createType, "type", "LOAD", "Test type")
	testCreateCmd.Flags().StringVar(&createBackend, "backend", "", "Benchmark backend")
	testCreateCmd.Flags().IntVar(&createRecords, "records", 0, "Number of records to send")
	testCreateCmd.Flags().IntVar(&createProducers, "producers", 0, "Number of parallel producers")
	testCreateCmd.Flags().IntVar(&createRecordSize, "record-size", 0, "Record size in bytes")
	testCreateCmd.Flags().IntVar(&createDuration, "duration", 0, "Duration in seconds")
	testCreateCmd.Flags().StringVar(&createTopic, "topic", "", "Kafka topic name")
	testCreateCmd.Flags().BoolVar(&createWait, "wait", false, "Wait for test to complete and print results")
	testCreateCmd.Flags().StringVar(&createAcks, "acks", "", "Producer acks: 0, 1, or all (default: all)")
	testCreateCmd.Flags().IntVar(&createBatchSize, "batch-size", 0, "Producer batch size in bytes (default: 65536)")
	testCreateCmd.Flags().IntVar(&createLingerMs, "linger-ms", 0, "Producer linger time in ms (default: 5)")
	testCreateCmd.Flags().StringVar(&createCompression, "compression", "", "Compression: none, gzip, snappy, lz4, zstd (default: lz4)")
	testCreateCmd.Flags().IntVar(&createConsumers, "consumers", 0, "Number of parallel consumers")
	testCreateCmd.Flags().IntVar(&createReplicationFactor, "replication-factor", 0, "Topic replication factor (default: 3)")
	testCreateCmd.Flags().IntVar(&createPartitions, "partitions", 0, "Topic partition count (default: 3)")
	testCreateCmd.Flags().IntVar(&createMinISR, "min-isr", 0, "Minimum in-sync replicas (default: 2)")
	testCreateCmd.Flags().StringVar(&createConsumerGroup, "consumer-group", "", "Consumer group name (auto-generated if empty)")
	testCreateCmd.Flags().IntVar(&createThroughput, "throughput", 0, "Target throughput in messages/sec (-1 = unlimited)")
	testCreateCmd.Flags().IntVar(&createFetchMinBytes, "fetch-min-bytes", 0, "Consumer fetch.min.bytes (default: 1)")
	testCreateCmd.Flags().IntVar(&createFetchMaxWaitMs, "fetch-max-wait-ms", 0, "Consumer fetch.max.wait.ms (default: 500)")

	testCmd.AddCommand(testListCmd)
	testCmd.AddCommand(testGetCmd)
	testCmd.AddCommand(testCreateCmd)
	testCmd.AddCommand(testDeleteCmd)
	testCmd.AddCommand(testTypesCmd)
	testCmd.AddCommand(testBackendsCmd)
	rootCmd.AddCommand(testCmd)
}
