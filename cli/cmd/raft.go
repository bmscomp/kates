package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var raftCmd = &cobra.Command{
	Use:   "raft",
	Short: "Inspect the Raft quorum the Kates members run",
}

var raftPsCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show each Kates quorum member's Raft role, term, indexes and lag",
	Long: `Shows every member of the Kates quorum as that member reports itself:
its Raft role, term, commit and applied index, how far its commit index
trails the leader's, and how long ago the leader last heard from it.

Any member answers: it asks each member over the members' TLS port, so the
table is whole whichever pod the request reaches. A member that does not
answer within 800 ms shows as UNREACHABLE with the error.`,
	Example: `  kates raft ps
  kates raft ps -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		q, err := apiClient.Quorum(context.Background())
		if err != nil {
			return cmdErr("Failed to read the quorum: " + err.Error())
		}
		output.Render(outputMode == "json", q, func() { renderRaftPs(q) })
		return nil
	},
}

func renderRaftPs(q *client.QuorumStatus) {
	if !q.Enabled {
		output.Hint("This Kates runs alone: no quorum (kates.quorum.peers is not set).")
		return
	}
	var leader *client.QuorumMember
	for i := range q.Members {
		if q.Members[i].Role == "LEADER" {
			leader = &q.Members[i]
		}
	}
	headers := []string{"MEMBER", "ROLE", "TERM", "COMMIT", "APPLIED", "LAG", "LAST CONTACT"}
	var rows [][]string
	var notes []string
	answered := 0
	for _, m := range q.Members {
		id := output.Printable(m.ID)
		if m.Error != "" {
			rows = append(rows, []string{id, "UNREACHABLE", "-", "-", "-", "-", "-"})
			notes = append(notes, output.Printable(m.Error))
			continue
		}
		answered++
		lag := "-"
		if leader != nil && leader.CommitIndex != nil && m.CommitIndex != nil {
			lag = strconv.FormatInt(*leader.CommitIndex-*m.CommitIndex, 10)
		}
		note := ""
		if m.StorageHealthy != nil && !*m.StorageHealthy {
			note = "storage unhealthy"
		}
		rows = append(rows, []string{id, output.Printable(m.Role), num(m.Term), num(m.CommitIndex),
			num(m.AppliedIndex), lag, ms(m.LastContactMs)})
		notes = append(notes, note)
	}
	output.TableNotes(headers, rows, notes)

	majority := len(q.Members)/2 + 1
	health := fmt.Sprintf("quorum OK (%d/%d)", answered, len(q.Members))
	if answered < majority {
		health = fmt.Sprintf("NO QUORUM (%d/%d answered, %d needed)", answered, len(q.Members), majority)
	}
	if leader == nil {
		output.Warn("no leader · " + health)
		return
	}
	fmt.Fprintf(output.Out, "\n  leader %s for %s · %s · answered by %s\n",
		output.Printable(leader.ID), ms(leader.RoleElapsedMs), health, output.Printable(q.AnsweredBy))
}

// raftNum prints an index or term; -1 (none) and absent print as "-".
func num(v *int64) string {
	if v == nil || *v < 0 {
		return "-"
	}
	return strconv.FormatInt(*v, 10)
}

func ms(v *int64) string {
	if v == nil {
		return "-"
	}
	d := time.Duration(*v) * time.Millisecond
	if d < time.Second {
		return d.String()
	}
	return d.Round(time.Second).String()
}

func init() {
	raftCmd.AddCommand(raftPsCmd)
	rootCmd.AddCommand(raftCmd)
}
