package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// The four commands that place the stack take the same scope flags, with
// the same defaults. They used to declare their own copies, and the sets
// drifted: clean had no --ui-ns, kyverno apply had none at all.
func TestDeployScopeFlags_SameOnEveryCommand(t *testing.T) {
	commands := map[string]*cobra.Command{"deploy": deployCmd, "deploy status": deployStatusCmd, "clean": cleanCmd, "kyverno apply": kyvernoApplyCmd}
	for name, cmd := range commands {
		for _, flag := range deployScopeFlagNames {
			f := cmd.Flags().Lookup(flag)
			if f == nil {
				t.Errorf("%s has no --%s", name, flag)
				continue
			}
			want := deployCmd.Flags().Lookup(flag).DefValue
			if flag == "topology" && name == "clean" {
				want = "" // clean's empty topology means both
			}
			if f.DefValue != want {
				t.Errorf("%s --%s defaults to %q, deploy to %q", name, flag, f.DefValue, want)
			}
		}
	}
}

// clean's flags bind clean's own variables, so parsing clean never moves
// deploy's; deploy status and kyverno apply share deploy's.
func TestDeployScopeFlags_Bindings(t *testing.T) {
	prevClean, prevDeploy := cleanKafkaUINS, deployKafkaUINS
	t.Cleanup(func() {
		cleanKafkaUINS, deployKafkaUINS = prevClean, prevDeploy
		for _, cmd := range []*cobra.Command{cleanCmd, deployStatusCmd} {
			f := cmd.Flags().Lookup("ui-ns")
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	if err := cleanCmd.Flags().Set("ui-ns", "team-ui"); err != nil {
		t.Fatal(err)
	}
	if cleanKafkaUINS != "team-ui" || deployKafkaUINS == "team-ui" {
		t.Errorf("clean --ui-ns: clean=%q deploy=%q", cleanKafkaUINS, deployKafkaUINS)
	}
	if err := deployStatusCmd.Flags().Set("ui-ns", "status-ui"); err != nil {
		t.Fatal(err)
	}
	if deployKafkaUINS != "status-ui" {
		t.Errorf("deploy status --ui-ns must bind deploy's variable, got %q", deployKafkaUINS)
	}
}
