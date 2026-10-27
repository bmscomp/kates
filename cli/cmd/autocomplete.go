package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/spf13/cobra"
)

// completionTimeout bounds an API call made while completing a command line.
// A completer that waited on the client's own timeouts blocked the shell for
// as long as a blackholed host took to fail; past this, the completer gives
// its fallback list and the shell goes on.
const completionTimeout = 2 * time.Second

// completionContext is the context an API call from a completer runs with.
func completionContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(commandContext(cmd), completionTimeout)
}

func completeRunIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := completionContext(cmd)
	defer cancel()
	result, err := apiClient.ListTests(ctx, "", "", 0, 20)
	if err != nil || result == nil || len(result.Content) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(result.Content))
	for _, t := range result.Content {
		desc := t.ID + "\t" + t.TestType + " " + t.Status
		ids = append(ids, desc)
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

func completeDoneRunIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := completionContext(cmd)
	defer cancel()
	result, err := apiClient.ListTests(ctx, "", "DONE", 0, 20)
	if err != nil || result == nil || len(result.Content) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(result.Content))
	for _, t := range result.Content {
		desc := t.ID + "\t" + t.TestType + " " + t.Status
		ids = append(ids, desc)
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

func completeTopicNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := completionContext(cmd)
	defer cancel()
	topics, err := apiClient.KafkaTopics(ctx)
	if err != nil || len(topics) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(topics))
	for _, t := range topics {
		names = append(names, t.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeGroupIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := completionContext(cmd)
	defer cancel()
	groups, err := apiClient.KafkaGroups(ctx)
	if err != nil || len(groups) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		if id, ok := g["groupId"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

func completeProfileNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".kates", "profiles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeSnapshotNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".kates", "snapshots")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeThemeNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return theme.Names(), cobra.ShellCompDirectiveNoFileComp
}

func completeLookNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return theme.LookNames(), cobra.ShellCompDirectiveNoFileComp
}

func completeTestTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := completionContext(cmd)
	defer cancel()
	types, err := apiClient.TestTypes(ctx)
	if err == nil && len(types) > 0 {
		return types, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"}, cobra.ShellCompDirectiveNoFileComp
}

func registerTestCompletions() {
	testCreateCmd.RegisterFlagCompletionFunc("type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeTestTypes(cmd, args, toComplete)
	})
	testCreateCmd.RegisterFlagCompletionFunc("backend", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		ctx, cancel := completionContext(cmd)
		defer cancel()
		backends, err := apiClient.Backends(ctx)
		if err == nil && len(backends) > 0 {
			return backends, cobra.ShellCompDirectiveNoFileComp
		}
		return []string{"native", "kafka-clients"}, cobra.ShellCompDirectiveNoFileComp
	})
	testCreateCmd.RegisterFlagCompletionFunc("compression", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"none", "gzip", "snappy", "lz4", "zstd"}, cobra.ShellCompDirectiveNoFileComp
	})
	testListCmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"PENDING", "RUNNING", "DONE", "FAILED"}, cobra.ShellCompDirectiveNoFileComp
	})
	testListCmd.RegisterFlagCompletionFunc("type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeTestTypes(cmd, args, toComplete)
	})

	testGetCmd.ValidArgsFunction = completeRunIDs
	testDeleteCmd.ValidArgsFunction = completeRunIDs
	testCancelCmd.ValidArgsFunction = completeRunIDs
	testExportCmd.ValidArgsFunction = completeDoneRunIDs
	testFlameCmd.ValidArgsFunction = completeDoneRunIDs

	testExportCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"csv", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func registerAnalysisCompletions() {
	advisorCmd.ValidArgsFunction = completeDoneRunIDs
	reportCmd.ValidArgsFunction = completeDoneRunIDs
	reportCompareCmd.ValidArgsFunction = completeDoneRunIDs
	explainCmd.ValidArgsFunction = completeDoneRunIDs
	replayCmd.ValidArgsFunction = completeDoneRunIDs
	reportDiffCmd.ValidArgsFunction = completeDoneRunIDs

	badgeCmd.RegisterFlagCompletionFunc("metric", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"grade\tOverall grade badge", "p99\tP99 latency badge", "throughput\tThroughput badge"}, cobra.ShellCompDirectiveNoFileComp
	})
	badgeCmd.RegisterFlagCompletionFunc("type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeTestTypes(cmd, args, toComplete)
	})

	watchEventsCmd.RegisterFlagCompletionFunc("type", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeTestTypes(cmd, args, toComplete)
	})
}

func registerKafkaCompletions() {
	kafkaConsumeCmd.RegisterFlagCompletionFunc("offset", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"latest", "earliest"}, cobra.ShellCompDirectiveNoFileComp
	})

	kafkaTopicCmd.ValidArgsFunction = completeTopicNames
	kafkaConsumeCmd.ValidArgsFunction = completeTopicNames
	kafkaAlterTopicCmd.ValidArgsFunction = completeTopicNames
	kafkaDeleteTopicCmd.ValidArgsFunction = completeTopicNames
	kafkaGroupCmd.ValidArgsFunction = completeGroupIDs
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

func registerProfileCompletions() {
	profileCompareCmd.ValidArgsFunction = completeProfileNames
	profileAssertCmd.ValidArgsFunction = completeProfileNames
}

func registerSnapshotCompletions() {
	snapshotDiffCmd.ValidArgsFunction = completeSnapshotNames
}

func registerThemeCompletions() {
	themePreviewCmd.ValidArgsFunction = completeThemeNames
	themeSetCmd.ValidArgsFunction = completeThemeNames
	lookPreviewCmd.ValidArgsFunction = completeLookNames
	lookSetCmd.ValidArgsFunction = completeLookNames
	_ = rootCmd.RegisterFlagCompletionFunc("look", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return theme.LookNames(), cobra.ShellCompDirectiveNoFileComp
	})
}

func registerCostCompletions() {
	costEstimateCmd.RegisterFlagCompletionFunc("cloud", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{
			"aws\tAmazon Web Services (MSK)",
			"azure\tAzure Event Hubs",
			"gcp\tGoogle Cloud Pub/Sub",
			"confluent\tConfluent Cloud",
		}, cobra.ShellCompDirectiveNoFileComp
	})
}
