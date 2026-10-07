package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var (
	testTypeFlag   string
	testStatusFlag string
	testPageFlag   int
	testSizeFlag   int
	testDryRun     bool
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

		if result.Spec != nil {
			output.SubHeader("Configuration")
			output.KeyValue("Records", fmtNum(float64(result.Spec.Records)))
			output.KeyValue("Producers", fmt.Sprintf("%d", result.Spec.ParallelProducers))
			output.KeyValue("Acks", result.Spec.Acks)
			output.KeyValue("Compression", result.Spec.CompressionType)
			batchSize, lingerMs := "—", "—"
			if result.Spec.BatchSize != nil {
				batchSize = fmtNum(float64(*result.Spec.BatchSize))
			}
			if result.Spec.LingerMs != nil {
				lingerMs = fmt.Sprintf("%d", *result.Spec.LingerMs)
			}
			output.KeyValue("Batch Size", batchSize)
			output.KeyValue("Linger ms", lingerMs)
			output.KeyValue("Partitions", fmt.Sprintf("%d", result.Spec.Partitions))
			output.KeyValue("Replication", fmt.Sprintf("%d", result.Spec.ReplicationFactor))
		}

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

			hasErrors := false
			for _, r := range result.Results {
				if r.Error != "" {
					if !hasErrors {
						output.SubHeader("Failure Details")
						hasErrors = true
					}
					phase := r.PhaseName
					if phase == "" {
						phase = "main"
					}
					output.Error(fmt.Sprintf("[%s] %s", phase, r.Error))

					for _, hint := range matchHints(r.Error) {
						output.Hint("  💡 " + hint)
					}
				}
			}

			var maxThroughput float64
			for _, r := range result.Results {
				if r.ThroughputRecordsPerSec > maxThroughput {
					maxThroughput = r.ThroughputRecordsPerSec
				}
			}
			if maxThroughput > 0 {
				fmt.Println()
				// Only draw a bar when the run had a target rate: a bar needs
				// a meaningful ceiling, and the old hardcoded 100k made high
				// throughput render RED (the consumption palette read "good"
				// as "dangerous") while any slow test looked healthily green.
				// The rate is spec.throughput, what the producers honoured;
				// targetThroughput is only the name a request may give it.
				if result.Spec != nil && result.Spec.Throughput > 0 {
					output.MetricBarDir("Throughput", maxThroughput, float64(result.Spec.Throughput), true)
				} else {
					output.KeyValue("Peak Throughput", fmtNum(maxThroughput)+" rec/s")
				}
			}

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
					if rto, ok := ir.MeasuredMaxRtoMs(); ok && rto > 0 {
						output.KeyValue("Max RTO", fmt.Sprintf("%.0f ms", rto))
					}
					// A measured zero RPO is shown; an unmeasured one says so
					// rather than disappearing or reading as zero.
					if rpo, ok := ir.MeasuredRpoMs(); ok {
						output.KeyValue("RPO", fmt.Sprintf("%.0f ms", rpo))
					} else {
						output.KeyValue("RPO", "not measured")
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
						var listed int64
						for _, lr := range ir.LostRanges {
							listed += lr.Count
							lostRows = append(lostRows, []string{
								fmt.Sprintf("%d", lr.FromSeq),
								fmt.Sprintf("%d", lr.ToSeq),
								fmt.Sprintf("%d", lr.Count),
							})
						}
						output.Table([]string{"From Seq", "To Seq", "Count"}, lostRows)
						// The Kates API lists at most the first 1,000 ranges,
						// while Lost counts every lost record.
						if listed < ir.LostRecords {
							output.Hint(fmt.Sprintf("  (showing the first %d ranges: %d of the %d lost records)",
								len(ir.LostRanges), listed, ir.LostRecords))
						}
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
		} else if isStaleResult(result.Results) {
			output.SubHeader("Diagnosis")
			output.Warn(fmt.Sprintf("Test finished with status %s but recorded 0 data", strings.ToUpper(result.Status)))
			fmt.Println()
			output.Warn("Possible reasons:")
			output.Hint("  • Backend pod restarted while the test was running (in-memory state lost)")
			output.Hint("  • Kafka producer/consumer failed to connect (check broker connectivity)")
			output.Hint("  • Topic creation failed (partition count or replication factor too high)")
			output.Hint("  • SASL authentication error (credentials expired or misconfigured)")
			output.Hint("  • Test duration too short for record count (increase --duration)")
			fmt.Println()
			output.Hint("Next steps:")
			output.Hint("  kubectl logs -n kates -l app=kates --tail=50")
			output.Hint("  kates doctor")
		} else {
			effectiveStatus := strings.ToUpper(result.Status)
			if effectiveStatus == "DONE" || effectiveStatus == "COMPLETED" {
				effectiveStatus = "FAILED"
			}
			output.SubHeader("Diagnosis")
			output.Error(fmt.Sprintf("Test finished with status %s but produced no results", strings.ToUpper(result.Status)))
			fmt.Println()
			output.Warn("Possible reasons:")
			output.Hint("  • Backend pod restarted while the test was running (in-memory state lost)")
			output.Hint("  • Kafka producer/consumer failed to connect (check broker connectivity)")
			output.Hint("  • Topic creation failed (partition count or replication factor too high)")
			output.Hint("  • SASL authentication error (credentials expired or misconfigured)")
			output.Hint("  • Test duration too short for record count (increase --duration)")
			fmt.Println()
			output.Hint("Next steps:")
			output.Hint("  kubectl logs -n kates -l app=kates --tail=50")
			output.Hint("  kates doctor")
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
		// IsInteractive is the one guard: the previous check looked at stdout
		// only, so `echo | kates test create` (TTY stdout, piped stdin) sent
		// huh hunting for input on a pipe. It also ignored isTesting and
		// TERM=dumb.
		if cmd.Flags().NFlag() == 0 && IsInteractive() {
			if err := runInteractiveTestCreate(); err != nil {
				return err
			}
		}

		upperType := strings.ToUpper(createType)
		if !isValidTestType(upperType) {
			return cmdErr(fmt.Sprintf("Unknown test type %q. Valid types: %s",
				createType, strings.Join(validTestTypes, ", ")))
		}
		req := &client.CreateTestRequest{
			TestType: upperType,
		}
		if createBackend != "" {
			req.Backend = createBackend
		}
		// A 0 is a setting for these three, not an absent value, so each is
		// sent when given, 0 included. --linger-ms 0 used to be left out, and
		// a LOAD run lingered its type's 5 ms.
		batchSize := givenInt(cmd, "batch-size", createBatchSize)
		lingerMs := givenInt(cmd, "linger-ms", createLingerMs)
		fetchMaxWaitMs := givenInt(cmd, "fetch-max-wait-ms", createFetchMaxWaitMs)
		if hasSpecOverrides() || batchSize != nil || lingerMs != nil || fetchMaxWaitMs != nil {
			req.Spec = &client.TestSpec{
				Records:           createRecords,
				ParallelProducers: createProducers,
				RecordSizeBytes:   createRecordSize,
				DurationMs:        createDuration * 1000,
				Topic:             createTopic,
				Acks:              createAcks,
				BatchSize:         batchSize,
				LingerMs:          lingerMs,
				CompressionType:   createCompression,
				NumConsumers:      createConsumers,
				ReplicationFactor: createReplicationFactor,
				Partitions:        createPartitions,
				MinInsyncReplicas: createMinISR,
				ConsumerGroup:     createConsumerGroup,
				TargetThroughput:  createThroughput,
				FetchMinBytes:     createFetchMinBytes,
				FetchMaxWaitMs:    fetchMaxWaitMs,
			}
		}

		if testDryRun {
			printDryRun("Would create test", req)
			return nil
		}

		ctx := commandContext(cmd)
		result, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			return cmdErr("Failed to create test: " + err.Error())
		}

		if outputMode == "json" {
			if !createWait {
				output.JSON(result)
				return nil
			}
			// json + --wait previously returned HERE, before waiting at all —
			// the flag was silently ignored. Follow silently, then emit the
			// FINAL state, which is the object a json consumer actually wants.
			status, err := pollUntilDonePlain(ctx, result.ID, io.Discard)
			if stoppedWaiting(ctx, err) {
				return stopStartedRun(result.ID)
			}
			if err != nil {
				return cmdErr("Lost track of test " + truncID(result.ID) + ": " + err.Error())
			}
			final, gerr := apiClient.GetTest(context.Background(), result.ID)
			if gerr != nil {
				return cmdErr("Failed to fetch final state: " + gerr.Error())
			}
			output.JSON(final)
			if isFailedStatus(status) {
				return cmdErr("test " + truncID(result.ID) + " finished " + status)
			}
			return nil
		}

		output.Success("Test created successfully")
		output.KeyValue("ID", result.ID)
		output.KeyValue("Type", result.TestType)
		if !createWait {
			output.KeyValue("Status", output.StatusBadge(result.Status))
		}

		if createWait {
			fmt.Println()
			status, err := pollUntilDone(ctx, result.ID)
			if stoppedWaiting(ctx, err) {
				// It started the run, so stopping means cancelling it: this
				// used to exit 0 and leave the test running.
				return stopStartedRun(result.ID)
			}
			if err != nil {
				// Unknown outcome is not success. Exiting 0 here is how CI
				// stayed green while load tests failed.
				return cmdErr("Lost track of test " + truncID(result.ID) + ": " + err.Error())
			}
			if isFailedStatus(status) {
				return cmdErr("test " + truncID(result.ID) + " finished " + status +
					" — details: kates test get " + result.ID)
			}
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

var testCancelCmd = &cobra.Command{
	Use:   "cancel <id>...",
	Short: "Cancel pending or running test runs",
	Long: `Cancel one or more test runs that are pending or running. Their tasks stop,
the run gives back its place among the runs the Kates API allows at once, and
it is stored as FAILED, each unfinished task with the error "Cancelled by
user".

A run that has already finished cannot be cancelled; kates says so and exits 1.
Ctrl-C while test apply, test create or replay waits cancels the run it started
the same way.`,
	Example: `  kates test cancel 69acdf31
  kates test cancel 69acdf31 7c1e2b90 -o json`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		results := make([]testCancelResult, 0, len(args))
		failed := 0
		for _, id := range args {
			r := testCancelResult{ID: id}
			if err := apiClient.CancelTest(ctx, id); err != nil {
				failed++
				r.Error = err.Error()
				if outputMode != "json" {
					output.Error("Not cancelled: " + id + ": " + err.Error())
				}
			} else {
				r.Cancelled = true
				if outputMode != "json" {
					output.Success("Cancelled: " + id)
				}
			}
			results = append(results, r)
		}
		if outputMode == "json" {
			output.JSON(results)
		}
		if failed > 0 {
			// Each failure has been said already, per run.
			return &silentErr{msg: fmt.Sprintf("%d of %d run(s) not cancelled", failed, len(args))}
		}
		return nil
	},
}

// testCancelResult is one run's row in kates test cancel -o json.
type testCancelResult struct {
	ID        string `json:"id"`
	Cancelled bool   `json:"cancelled"`
	Error     string `json:"error,omitempty"`
}

func runInteractiveTestCreate() error {
	var recordsStr, durationStr string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Test Type").
				Options(
					huh.NewOption("LOAD (Maximum throughput)", "LOAD"),
					huh.NewOption("STRESS (Find breaking point)", "STRESS"),
					huh.NewOption("ENDURANCE (Sustained over time)", "ENDURANCE"),
					huh.NewOption("VOLUME", "VOLUME"),
				).
				Value(&createType),
			huh.NewInput().
				Title("Number of Records").
				Description("Leave blank if specifying duration").
				Validate(optionalPositiveInt).
				Value(&recordsStr),
			huh.NewInput().
				Title("Duration (seconds)").
				Description("Leave blank if specifying records").
				Validate(optionalPositiveInt).
				Value(&durationStr),
			huh.NewConfirm().
				Title("Wait for completion?").
				Value(&createWait),
		),
	)

	if err := form.Run(); err != nil {
		return err
	}

	// The form validated these, so Atoi cannot fail here — but the old code
	// silently discarded errors, meaning a typo like "10k" created a test
	// with zero records and no explanation.
	if recordsStr != "" {
		createRecords, _ = strconv.Atoi(recordsStr)
	}
	if durationStr != "" {
		createDuration, _ = strconv.Atoi(durationStr)
	}
	return nil
}

// optionalPositiveInt validates a wizard field that may be blank or a positive
// integer — rejecting input at the form, where the user can fix it, instead of
// silently dropping it after.
func optionalPositiveInt(s string) error {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fmt.Errorf("enter a positive whole number (or leave blank)")
	}
	return nil
}

// hasSpecOverrides reports whether the flags, or the wizard, set a spec field
// other than the three givenInt reads. A --throughput of -1 counts: given
// alone, it sent no spec at all, so an ENDURANCE run took its type's 5,000
// records/s instead of running unlimited.
func hasSpecOverrides() bool {
	return createRecords > 0 || createProducers > 0 || createRecordSize > 0 ||
		createDuration > 0 || createTopic != "" || createAcks != "" || createCompression != "" ||
		createConsumers > 0 || createReplicationFactor > 0 || createPartitions > 0 ||
		createMinISR > 0 || createConsumerGroup != "" || createThroughput != 0 ||
		createFetchMinBytes > 0
}

// givenInt is the value of the int flag name when the command line gives it,
// 0 included, and nil when it does not, which leaves the type's default.
func givenInt(cmd *cobra.Command, name string, v int) *int {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &v
}

var validTestTypes = []string{
	"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY",
	"ROUND_TRIP", "INTEGRITY",
	"TUNE_REPLICATION", "TUNE_ACKS", "TUNE_BATCHING", "TUNE_COMPRESSION", "TUNE_PARTITIONS",
}

func isValidTestType(t string) bool {
	for _, v := range validTestTypes {
		if v == t {
			return true
		}
	}
	return false
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
	testCreateCmd.Flags().BoolVar(&createWait, "wait", false, "Wait for test to complete and print results; Ctrl-C cancels it")
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
	testCreateCmd.Flags().BoolVar(&testDryRun, "dry-run", false, "Print request JSON without executing")
	interruptible(testCreateCmd)

	testCmd.AddCommand(testListCmd)
	testCmd.AddCommand(testGetCmd)
	testCmd.AddCommand(testCreateCmd)
	testCmd.AddCommand(testDeleteCmd)
	testCmd.AddCommand(testCancelCmd)
	testCmd.AddCommand(testTypesCmd)
	testCmd.AddCommand(testBackendsCmd)
	rootCmd.AddCommand(testCmd)

	registerTestCompletions()
}
