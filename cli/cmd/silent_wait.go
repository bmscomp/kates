package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func waitComponentReadySilent(ctx context.Context, id, namespace, selector string, timeout time.Duration) error {
	dl.StartComponent(id, timeout)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "kubectl", "get", "pods", "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.phase}{','}{end}").Output()
		if err == nil {
			phases := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, p := range phases {
				if p != "" {
					count++
					if p != "Running" && p != "Succeeded" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				dl.FinishComponent(id, true)
				return nil
			}
		}

		select {
		case <-ctx.Done():
			dl.FinishComponent(id, false)
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	dl.FinishComponent(id, false)
	return fmt.Errorf("timeout waiting for %s to become ready", id)
}

func waitCustomResourceReadySilent(ctx context.Context, id, kind, namespace, selector string, timeout time.Duration) error {
	dl.StartComponent(id, timeout)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "kubectl", "get", kind, "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.conditions[?(@.type==\"Ready\")].status}{','}{end}").Output()
		if err == nil {
			statuses := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, s := range statuses {
				if s != "" {
					count++
					if s != "True" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				dl.FinishComponent(id, true)
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	dl.FinishComponent(id, false)
	return fmt.Errorf("timeout waiting for %s to become ready", id)
}

func waitKafkaUsersReadySilent(ctx context.Context, id, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, id, "kafkauser", namespace, "strimzi.io/cluster=krafter", timeout)
}

func waitConnectorReadySilent(ctx context.Context, id, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, id, "kafkaconnector", namespace, "strimzi.io/cluster=krafter-connect", timeout)
}
