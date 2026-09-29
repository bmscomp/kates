package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/internal/podrun"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// kates migrate target — scripts/mm2-kafka-cli.sh in Go.
//
// "Is the mirror replicating?" has exactly one honest answer — the target's
// end offsets, read twice, moving — and "what did it create?" has exactly
// one honest source, the brokers: the Topic Operator is unidirectional, so
// MirrorMaker's topics never become KafkaTopic resources. These ask the
// brokers, as the kates-mm2 user, from a pod the broker NetworkPolicy
// admits, with the credential written into the pod and never on a command
// line, using whichever offset tool the target's Kafka line ships.

// migrateTargetOptions are the flags every target subcommand takes.
type migrateTargetOptions struct {
	Namespace, Cluster, User string
	Partitions               bool
	Timeout                  int
	KeepPod                  bool
}

var (
	migrateTargetOpts migrateTargetOptions

	migrateTargetCmd = &cobra.Command{
		Use:   "target",
		Short: "Ask the target's brokers, as the MM2 user: topics, offsets, groups",
	}

	migrateTargetTopicsCmd = &cobra.Command{
		Use:   "topics",
		Short: "List the topics on the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateTarget(cmd.Context(), migrateTargetOpts, "topics", "")
		},
	}
	migrateTargetOffsetsCmd = &cobra.Command{
		Use:   "offsets <topic>",
		Short: "Sum a topic's end offsets on the target (--partitions for the per-partition lines)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateTarget(cmd.Context(), migrateTargetOpts, "offsets", args[0])
		},
	}
	migrateTargetGroupsCmd = &cobra.Command{
		Use:   "groups",
		Short: "List the consumer groups on the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateTarget(cmd.Context(), migrateTargetOpts, "groups", "")
		},
	}
	migrateTargetGroupCmd = &cobra.Command{
		Use:   "group <group>",
		Short: "Describe one consumer group on the target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateTarget(cmd.Context(), migrateTargetOpts, "group", args[0])
		},
	}
)

func init() {
	pf := migrateTargetCmd.PersistentFlags()
	pf.StringVar(&migrateTargetOpts.Namespace, "namespace", migrate.DefaultTargetNamespace, "Namespace of the Kafka cluster")
	pf.StringVar(&migrateTargetOpts.Cluster, "cluster", migrate.DefaultTargetCluster, "Strimzi cluster name")
	pf.StringVar(&migrateTargetOpts.User, "user", migrate.TargetUser, "KafkaUser whose Secret holds the SCRAM password")
	pf.IntVar(&migrateTargetOpts.Timeout, "timeout", 300, "Client pod readiness budget in seconds")
	pf.BoolVar(&migrateTargetOpts.KeepPod, "keep-pod", false, "Leave the client pod running for the next call")
	migrateTargetOffsetsCmd.Flags().BoolVar(&migrateTargetOpts.Partitions, "partitions", false, "Print the per-partition lines too")
	migrateTargetCmd.AddCommand(migrateTargetTopicsCmd, migrateTargetOffsetsCmd, migrateTargetGroupsCmd, migrateTargetGroupCmd)
	migrateCmd.AddCommand(migrateTargetCmd)
}

// targetClientPodName is the pod the target commands run in.
const targetClientPodName = "kates-migrate-target"

// targetClient is a client pod on the target's network as one user.
type targetClient struct {
	pod       podrun.Pod
	tools     podrun.Tools
	bootstrap string
	cfg       string
}

// openTargetClient makes the client pod exist with the user's credential
// written into it. The image is the pinned Strimzi Kafka image, the one
// the workers run; the tools are those of its Kafka line.
func openTargetClient(ctx context.Context, o migrateTargetOptions) (*targetClient, error) {
	if err := requireMigrateRepoRoot(); err != nil {
		return nil, err
	}
	pins, err := migrate.ReadPins(".")
	if err != nil {
		return nil, err
	}
	version, err := kafkaversion.Parse(pins.KafkaVersion)
	if err != nil {
		return nil, fmt.Errorf("versions.env STRIMZI_KAFKA_VERSION: %w", err)
	}
	timeout := time.Duration(o.Timeout) * time.Second
	if timeout <= 0 {
		timeout = podrun.DefaultReadyTimeout
	}
	c := &targetClient{
		pod: podrun.Pod{
			Namespace: o.Namespace, Name: targetClientPodName, Image: pins.KafkaImage,
			Labels: map[string]string{"app.kubernetes.io/component": "migrate-cli"},
		},
		tools:     podrun.ToolsFor(version, ""),
		bootstrap: targetBootstrapFor(o.Cluster, o.Namespace),
		cfg:       podrun.DefaultPropertiesPath,
	}
	if err := podrun.Ensure(ctx, defaultRunner, c.pod, timeout); err != nil {
		return nil, err
	}
	pw, err := readSecretKey(ctx, o.Namespace, o.User, "password")
	if err != nil {
		return nil, err
	}
	if err := podrun.WriteFile(ctx, defaultRunner, c.pod, c.cfg, podrun.SASLProperties("SCRAM-SHA-512", o.User, pw)); err != nil {
		return nil, err
	}
	return c, nil
}

// runMigrateTarget is every target subcommand.
func runMigrateTarget(ctx context.Context, o migrateTargetOptions, sub, arg string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c, err := openTargetClient(ctx, o)
	if err != nil {
		return err
	}
	if !o.KeepPod {
		defer func() {
			if err := podrun.Delete(context.Background(), defaultRunner, c.pod); err != nil {
				migrateWarn(err.Error())
			}
		}()
	}
	var argv []string
	switch sub {
	case "topics":
		argv = c.tools.ListTopics(c.bootstrap, c.cfg)
	case "groups":
		argv = c.tools.ListGroups(c.bootstrap, c.cfg)
	case "group":
		argv = c.tools.DescribeGroup(c.bootstrap, c.cfg, arg)
	case "offsets":
		argv = c.tools.EndOffsets(c.bootstrap, c.cfg, arg)
	default:
		return fmt.Errorf("unknown target subcommand %q", sub)
	}
	out, err := podrun.Exec(ctx, defaultRunner, c.pod, argv...)
	if err != nil {
		return fmt.Errorf("no output from the broker at %s as %s: %w", c.bootstrap, o.User, err)
	}
	switch sub {
	case "offsets":
		per, sum, err := podrun.ParseTopicOffsets(out, arg)
		if err != nil {
			if errors.Is(err, podrun.ErrNoOffsets) {
				return fmt.Errorf("no offsets returned for %s on %s — does the topic exist, and can %s read it?", arg, c.bootstrap, o.User)
			}
			return err
		}
		if outputMode == "json" {
			output.JSON(map[string]any{"topic": arg, "partitions": per, "endOffsets": sum})
			return nil
		}
		if o.Partitions {
			parts := make([]int, 0, len(per))
			for p := range per {
				parts = append(parts, p)
			}
			sort.Ints(parts)
			for _, p := range parts {
				fmt.Fprintf(output.Out, "%s:%d:%d\n", arg, p, per[p])
			}
		}
		fmt.Fprintf(output.Out, "%s end offsets: %d\n", arg, sum)
	case "group":
		rows := podrun.ParseGroupDescribe(out)
		if outputMode == "json" {
			output.JSON(map[string]any{"group": arg, "partitions": rows})
			return nil
		}
		if len(rows) == 0 {
			migrateHint(strings.TrimSpace(out))
			return nil
		}
		table := make([][]string, 0, len(rows))
		for _, r := range rows {
			table = append(table, []string{r.Topic, strconv.Itoa(r.Partition), offsetCell(r.CurrentOffset), offsetCell(r.LogEndOffset), offsetCell(r.Lag)})
		}
		output.Table([]string{"TOPIC", "PARTITION", "CURRENT-OFFSET", "LOG-END-OFFSET", "LAG"}, table)
	default:
		names := strings.Fields(out)
		sort.Strings(names)
		if outputMode == "json" {
			output.JSON(map[string]any{sub: names})
			return nil
		}
		if len(names) == 0 {
			return fmt.Errorf("no output from the broker at %s as %s", c.bootstrap, o.User)
		}
		for _, n := range names {
			fmt.Fprintln(output.Out, n)
		}
	}
	return nil
}

func offsetCell(n int64) string {
	if n < 0 {
		return "-"
	}
	return strconv.FormatInt(n, 10)
}
