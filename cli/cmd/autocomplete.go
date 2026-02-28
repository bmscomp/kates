package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

func registerTestCompletions() {
	testCreateCmd.RegisterFlagCompletionFunc("type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		types, err := apiClient.TestTypes(context.Background())
		if err == nil && len(types) > 0 {
			return types, cobra.ShellCompDirectiveNoFileComp
		}
		return []string{"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"}, cobra.ShellCompDirectiveNoFileComp
	})

	testCreateCmd.RegisterFlagCompletionFunc("backend", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		backends, err := apiClient.Backends(context.Background())
		if err == nil && len(backends) > 0 {
			return backends, cobra.ShellCompDirectiveNoFileComp
		}
		return []string{"native", "kafka-clients"}, cobra.ShellCompDirectiveNoFileComp
	})

	testListCmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"PENDING", "RUNNING", "DONE", "FAILED"}, cobra.ShellCompDirectiveNoFileComp
	})

	testCreateCmd.RegisterFlagCompletionFunc("compression", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"none", "gzip", "snappy", "lz4", "zstd"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func registerKafkaCompletions() {
	kafkaConsumeCmd.RegisterFlagCompletionFunc("offset", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"latest", "earliest"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func registerCtxCompletions() {
	ctxUseCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cfg := loadConfig()
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}

	ctxDeleteCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cfg := loadConfig()
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
