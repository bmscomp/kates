package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmscomp/kates/cli/output"
)

// deployFiles are where deploy writes the values it detected and its error
// log. In a Kates checkout that is the working directory, as it always was:
// the Makefile and the docs read .build/values-detected.yaml there. Anywhere
// else it is kates/deploy under the user state directory ($XDG_STATE_HOME,
// else ~/.local/state), so a deploy run from $HOME leaves nothing there.
func deployFiles() (values, errorLog string, err error) {
	if _, err := os.Stat(filepath.Join(operatorChartDir, "Chart.yaml")); err == nil {
		return filepath.Join(".build", "values-detected.yaml"), "deploy-error.log", nil
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("finding the user state directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "kates", "deploy")
	return filepath.Join(dir, "values-detected.yaml"), filepath.Join(dir, "deploy-error.log"), nil
}

// saveDeployError keeps a failed deploy's error in errorLog and says where.
func saveDeployError(errorLog string, deployErr error) {
	if os.MkdirAll(filepath.Dir(errorLog), 0o755) == nil && os.WriteFile(errorLog, []byte(deployErr.Error()), 0o644) == nil {
		output.Hint("The error is also in " + errorLog)
	}
}
