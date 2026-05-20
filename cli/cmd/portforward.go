package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/pkg/theme"
)

// portForwardSpec describes a single kubectl port-forward target.
type portForwardSpec struct {
	Label     string // human-readable name shown in the table
	Namespace string // Kubernetes namespace
	Resource  string // e.g. "service/kates" or "service/krafter-kafka-bootstrap"
	Remote    int    // port inside the cluster
	Local     int    // port on localhost
	URL       string // optional browser URL hint
}

// buildPortForwardSpecs returns the set of forwards for the current deploy configuration.
// Only specs for components that were actually deployed are included.
func buildPortForwardSpecs(kafkaNS, appNS, jaegerNS string) []portForwardSpec {
	specs := []portForwardSpec{
		{
			Label:     "Kates REST API",
			Namespace: appNS,
			Resource:  "service/kates",
			Remote:    8080,
			Local:     8080,
			URL:       "http://localhost:8080/api/health",
		},
		{
			Label:     "Kates gRPC",
			Namespace: appNS,
			Resource:  "service/kates",
			Remote:    9000,
			Local:     9000,
		},
		{
			Label:     "Kafka Bootstrap",
			Namespace: kafkaNS,
			Resource:  "service/krafter-kafka-bootstrap",
			Remote:    9092,
			Local:     9092,
		},
	}

	if deployWithMonitoring {
		specs = append(specs, portForwardSpec{
			Label:     "Jaeger UI",
			Namespace: jaegerNS,
			Resource:  "service/jaeger-query",
			Remote:    16686,
			Local:     16686,
			URL:       "http://localhost:16686",
		})
	}

	if deployWithSchemaRegistry == "apicurio" {
		specs = append(specs, portForwardSpec{
			Label:     "Apicurio Schema Registry",
			Namespace: kafkaNS,
			Resource:  "service/apicurio-registry",
			Remote:    80,
			Local:     8081,
			URL:       "http://localhost:8081/ui",
		})
	}

	return specs
}

// RunPortForwards starts kubectl port-forward for every spec, prints a live
// status table, and blocks until SIGINT/SIGTERM or all forwarders die.
func RunPortForwards(ctx context.Context, kafkaNS, appNS, jaegerNS string) {
	specs := buildPortForwardSpecs(kafkaNS, appNS, jaegerNS)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Secondary)
	labelStyle := lipgloss.NewStyle().Foreground(theme.Text).Width(28)
	portStyle := lipgloss.NewStyle().Foreground(theme.Info)
	urlStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	okStyle := lipgloss.NewStyle().Foreground(theme.Success).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(theme.Error).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	fmt.Println()
	fmt.Println(headerStyle.Render("  ⇄  Port Forwarding"))
	fmt.Println(dimStyle.Render("  ─────────────────────────────────────────────────────"))

	for _, s := range specs {
		urlHint := ""
		if s.URL != "" {
			urlHint = "  " + urlStyle.Render(s.URL)
		}
		fmt.Printf("  %s  %s → localhost:%d%s\n",
			labelStyle.Render(s.Label),
			portStyle.Render(fmt.Sprintf(":%d", s.Remote)),
			s.Local,
			urlHint,
		)
	}
	fmt.Println(dimStyle.Render("  ─────────────────────────────────────────────────────"))
	fmt.Println(dimStyle.Render("  Press Ctrl+C to stop all port-forwards"))
	fmt.Println()

	// cancelCtx is cancelled when the user presses Ctrl+C or when we want to
	// tear everything down.
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Capture SIGINT / SIGTERM to gracefully shut down all forwarders.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\n  Stopping port-forwards...")
			cancel()
		case <-cancelCtx.Done():
		}
	}()

	var wg sync.WaitGroup
	for _, spec := range specs {
		spec := spec // capture for goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			runPortForward(cancelCtx, spec, okStyle, errStyle, dimStyle)
		}()
	}

	wg.Wait()
}

// runPortForward runs a single kubectl port-forward with automatic restart on crash.
func runPortForward(ctx context.Context, spec portForwardSpec, okStyle, errStyle, dimStyle lipgloss.Style) {
	pfArg := fmt.Sprintf("%d:%d", spec.Local, spec.Remote)
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		attempt++
		cmd := exec.CommandContext(ctx,
			"kubectl", "port-forward",
			spec.Resource,
			pfArg,
			"-n", spec.Namespace,
		)
		cmd.Stdout = nil
		cmd.Stderr = nil

		if attempt == 1 {
			fmt.Printf("  %s  %s  localhost:%d\n",
				okStyle.Render("▶"),
				dimStyle.Render(spec.Label),
				spec.Local,
			)
		}

		if err := cmd.Run(); err != nil {
			select {
			case <-ctx.Done():
				// Cancelled by user — exit silently.
				return
			default:
			}
			fmt.Printf("  %s  %s crashed (attempt %d), restarting in 3s...\n",
				errStyle.Render("✖"),
				spec.Label,
				attempt,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}
