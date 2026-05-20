package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/klster/kates-cli/pkg/operator"
	"github.com/spf13/cobra"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Run the Kates Environment Operator",
	Long:  `Starts the operator to continuously monitor the Kubernetes environment (nodes, storage) and dynamically adapt the Kafka cluster to infrastructure changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ns, _ := cmd.Flags().GetString("namespace")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle graceful shutdown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		op := operator.NewKatesOperator(ns)
		return op.Start(ctx)
	},
}

func init() {
	operatorCmd.Flags().StringP("namespace", "n", "kafka", "Namespace to monitor")
	rootCmd.AddCommand(operatorCmd)
}
