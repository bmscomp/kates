package strimzi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Release is a row of helm list -o json.
type Release struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Chart      string `json:"chart"`       // "kafka-ui-0.3.1"
	AppVersion string `json:"app_version"` // the chart's appVersion
	Status     string `json:"status"`      // "deployed", "failed", "pending-upgrade", …
}

// Deployed says whether the release is in the deployed state. helm status
// exits 0 for a failed release too, so a lookup must read the status.
func (r Release) Deployed() bool {
	return r.Status == "deployed"
}

// ListReleases reads helm list -o json: the releases in namespace, or in
// every namespace when it is empty, narrowed by any extra helm list flags
// ("--deployed"). Helm's default listing holds deployed and failed releases,
// so a release mid-install is not among them. It is the one reader of helm
// list; clean, deploy and the operator discovery all go through it, where
// each used to parse the output its own way.
func ListReleases(ctx context.Context, r Runner, namespace string, extra ...string) ([]Release, error) {
	out, err := r.Run(ctx, "helm", ReleaseListArgs(namespace, extra...)...)
	if err != nil {
		return nil, err
	}
	return ParseReleases([]byte(out))
}

// ReleaseListArgs is the helm argument list ListReleases runs.
func ReleaseListArgs(namespace string, extra ...string) []string {
	args := []string{"list", "-A"}
	if namespace != "" {
		args = []string{"list", "-n", namespace}
	}
	return append(append(args, extra...), "-o", "json")
}

// ParseReleases decodes helm list -o json. Helm prints "null" or nothing at
// all for an empty listing.
func ParseReleases(listJSON []byte) ([]Release, error) {
	if strings.TrimSpace(string(listJSON)) == "" {
		return nil, nil
	}
	var releases []Release
	if err := json.Unmarshal(listJSON, &releases); err != nil {
		return nil, fmt.Errorf("parse helm list output: %w", err)
	}
	return releases, nil
}
