package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type checkResult struct {
	Name   string
	Passed bool
	Detail string
}

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Aliases: []string{"preflight", "check"},
	Short:   "Pre-flight cluster readiness checklist",
	Example: "  kates doctor",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		var checks []checkResult

		output.Banner("KATES Doctor", "Pre-flight cluster readiness")
		fmt.Println()

		health, err := apiClient.Health(ctx)
		if err != nil {
			checks = append(checks, checkResult{"API Reachable", false, err.Error()})
			renderChecks(checks)
			return nil
		}
		checks = append(checks, checkResult{"API Reachable", true, "Connected"})

		if health.Kafka != nil && health.Kafka.Status == "UP" {
			checks = append(checks, checkResult{"Kafka Connected", true, health.Kafka.BootstrapServers})
		} else {
			msg := "Kafka unreachable"
			if health.Kafka != nil {
				msg = health.Kafka.Message
			}
			checks = append(checks, checkResult{"Kafka Connected", false, msg})
		}

		cluster, err := apiClient.ClusterInfo(ctx)
		if err == nil && cluster != nil {
			count := 0
			if brokers := cluster.Brokers; brokers != nil {
				count = len(brokers)
			}
			checks = append(checks, checkResult{
				"Broker Count ≥ 3",
				count >= 3,
				fmt.Sprintf("%d brokers detected", count),
			})
		} else {
			checks = append(checks, checkResult{"Broker Count ≥ 3", false, "Could not query cluster"})
		}

		clusterHealth, err := apiClient.ClusterCheck(ctx)
		if err == nil && clusterHealth != nil {
			urp := clusterHealth.PartitionHealth.UnderReplicated
			offline := clusterHealth.PartitionHealth.Offline
			healthy := urp == 0 && offline == 0
			detail := "All replicas in sync"
			if !healthy {
				detail = fmt.Sprintf("%d under-replicated, %d offline", urp, offline)
			}
			checks = append(checks, checkResult{"ISR Health", healthy, detail})
		} else {
			checks = append(checks, checkResult{"ISR Health", false, "Could not check ISR"})
		}

		topics, err := apiClient.Topics(ctx)
		if err == nil {
			checks = append(checks, checkResult{
				"Topics Available",
				len(topics) > 0,
				fmt.Sprintf("%d topics found", len(topics)),
			})
		} else {
			checks = append(checks, checkResult{"Topics Available", false, "Could not list topics"})
		}

		backends, err := apiClient.Backends(ctx)
		if err == nil {
			checks = append(checks, checkResult{
				"Benchmark Backends",
				len(backends) > 0,
				fmt.Sprintf("%v", backends),
			})
		} else {
			checks = append(checks, checkResult{"Benchmark Backends", false, "No backends available"})
		}

		renderChecks(checks)
		return nil
	},
}

func renderChecks(checks []checkResult) {
	rows := make([][]string, len(checks))
	passed := 0
	for i, c := range checks {
		status := "PASS"
		if !c.Passed {
			status = "FAILED"
		} else {
			passed++
		}
		rows[i] = []string{c.Name, status, c.Detail}
	}
	output.Table([]string{"Check", "Status", "Detail"}, rows)

	summary := fmt.Sprintf("%d/%d checks passed", passed, len(checks))
	if passed == len(checks) {
		output.Success("✓ " + summary + " — cluster is ready for testing!")
	} else {
		output.Warn("⚠ " + summary + " — review failing checks above")
	}
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
