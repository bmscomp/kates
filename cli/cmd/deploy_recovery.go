package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/bmscomp/kates/cli/output"
)

// DeployError wraps a component deployment failure with context for retry/display.
type DeployError struct {
	Component string
	Group     string
	Err       error
	Retryable bool
}

func (e *DeployError) Error() string {
	return fmt.Sprintf("%s: %v", e.Component, e.Err)
}

// retryableHelmDeploy wraps a deploy function with interactive retry support.
// When the deploy fails, it prompts the user to retry (if TTY is available).
// In non-TTY environments, it fails immediately.
func retryableHelmDeploy(ctx context.Context, componentID string, maxRetries int, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			dl.Printf("    ⟳ Retrying %s (attempt %d/%d)...\n", componentID, attempt, maxRetries)
			// Brief pause before retry
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}

		if err := fn(); err != nil {
			lastErr = err
			dl.Printf("    %s %s failed: %v\n", output.ErrorStyle.Render("✖"), componentID, err)

			if attempt >= maxRetries {
				break
			}

			// Check if we can prompt the user
			// One confirm implementation for the whole CLI. confirm refuses
			// without a terminal, which covers the old explicit TTY check.
			fmt.Println()
			ok, err := confirm(fmt.Sprintf("%s Retry %s?", output.WarningStyle.Render("⚠"), componentID))
			if err != nil || !ok {
				break
			}
			continue
		}

		// Success
		if attempt > 0 {
			dl.Printf("    %s %s succeeded on retry\n", output.SuccessStyle.Render("✔"), componentID)
		}
		return nil
	}

	return &DeployError{
		Component: componentID,
		Err:       lastErr,
		Retryable: true,
	}
}
