package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	// namespaceLabelsAnnotation lists the label keys Kates set on a namespace
	// from the user's labels: the only keys it may later remove.
	namespaceLabelsAnnotation = "kates.io/namespace-labels"
	defaultLabelsFile         = ".labels"
	namespaceLabelsEnv        = "KATES_NAMESPACE_LABELS"
	namespaceLabelFlag        = "--namespace-label"
)

// namespaceLabelsFile is --labels-file. Empty reads ./.labels when it exists.
var namespaceLabelsFile string

var (
	labelNamePattern   = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)
	labelPrefixPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	// Kubernetes, Helm, Strimzi and Kates own these prefixes; a user label
	// there could impersonate or overwrite theirs.
	reservedLabelDomains = []string{"kubernetes.io", "k8s.io", "helm.sh", "strimzi.io", "kates.io"}
)

// addLabelsFileFlag registers --labels-file on a command that creates
// namespaces; every such command shares namespaceLabelsFile.
func addLabelsFileFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&namespaceLabelsFile, "labels-file", "", "File of key=value labels for the namespaces Kates creates (default: ./.labels, when it exists)")
}

// namespaceUserLabels merges the user's namespace labels, later sources
// winning per key: the labels file, then KATES_NAMESPACE_LABELS, then
// --namespace-label. sources says where each key's value came from.
func namespaceUserLabels() (labels, sources map[string]string, err error) {
	labels, sources = map[string]string{}, map[string]string{}
	path, required := namespaceLabelsFile, true
	if path == "" {
		path, required = defaultLabelsFile, false
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := parseNamespaceLabelsFile(path, string(data), labels, sources); err != nil {
			return nil, nil, err
		}
	case required || !errors.Is(err, fs.ErrNotExist):
		return nil, nil, fmt.Errorf("labels file: %w", err)
	}
	if raw := strings.TrimSpace(os.Getenv(namespaceLabelsEnv)); raw != "" {
		if err := addDeployNamespaceLabelSpecs(labels, sources, raw, namespaceLabelsEnv); err != nil {
			return nil, nil, err
		}
	}
	for _, raw := range deployNamespaceLabels {
		if err := addDeployNamespaceLabelSpecs(labels, sources, raw, namespaceLabelFlag); err != nil {
			return nil, nil, err
		}
	}
	return labels, sources, nil
}

// parseNamespaceLabelsFile reads key=value lines. Blank lines and # comment
// lines are skipped; nothing is unquoted, so a quote or # in a value fails
// validation instead of being silently kept or dropped.
func parseNamespaceLabelsFile(path, data string, labels, sources map[string]string) error {
	firstLine := map[string]int{}
	for i, line := range strings.Split(data, "\n") {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		where := fmt.Sprintf("%s:%d", path, n)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s: invalid namespace label %q: expected key=value", where, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if first, seen := firstLine[key]; seen {
			return fmt.Errorf("%s: label %q is already set on line %d", where, key, first)
		}
		if err := validateNamespaceLabel(key, value); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		firstLine[key] = n
		labels[key], sources[key] = value, where
	}
	return nil
}

func addDeployNamespaceLabelSpecs(labels, sources map[string]string, raw, source string) error {
	for _, spec := range strings.Split(raw, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		key, value, ok := strings.Cut(spec, "=")
		if !ok {
			return fmt.Errorf("%s: invalid namespace label %q: expected key=value", source, spec)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if err := validateNamespaceLabel(key, value); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		labels[key], sources[key] = value, source
	}
	return nil
}

// validateNamespaceLabel applies the API server's label syntax, so a bad
// label fails here rather than halfway through a deploy, and refuses the
// prefixes Kubernetes and the charts Kates installs own.
func validateNamespaceLabel(key, value string) error {
	name := key
	if prefix, rest, ok := strings.Cut(key, "/"); ok {
		name = rest
		switch {
		case prefix == "":
			return fmt.Errorf("label key %q: the prefix before '/' is empty", key)
		case len(prefix) > 253 || !labelPrefixPattern.MatchString(prefix):
			return fmt.Errorf("label key %q: prefix %q must be a lowercase DNS subdomain of at most 253 characters", key, prefix)
		case reservedLabelPrefix(prefix):
			return fmt.Errorf("label key %q: prefix %q is reserved (kubernetes.io, k8s.io, helm.sh, strimzi.io, kates.io; only pod-security.kubernetes.io is allowed)", key, prefix)
		}
	}
	if len(name) > 63 || !labelNamePattern.MatchString(name) {
		return fmt.Errorf("label key %q: name %q must be 1-63 characters of letters, digits, '-', '_' or '.', starting and ending with a letter or digit", key, name)
	}
	if len(value) > 63 || value != "" && !labelNamePattern.MatchString(value) {
		return fmt.Errorf("label %q: value %q must be empty or 1-63 characters of letters, digits, '-', '_' or '.', starting and ending with a letter or digit", key, value)
	}
	return nil
}

func reservedLabelPrefix(prefix string) bool {
	if prefix == "pod-security.kubernetes.io" {
		return false
	}
	for _, domain := range reservedLabelDomains {
		if prefix == domain || strings.HasSuffix(prefix, "."+domain) {
			return true
		}
	}
	return false
}
