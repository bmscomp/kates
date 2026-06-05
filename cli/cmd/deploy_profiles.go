package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type DeployProfile struct {
	Name            string
	Topology        string
	HA              bool
	WithStrimzi     bool
	WithChaos       bool
	WithMonitoring  bool
	WithCertManager bool
	WithKyverno     bool
	WithConnect     bool
	SchemaRegistry  string // "none", "apicurio"
}

var deployProfiles = map[string]DeployProfile{
	"dev": {
		Name:            "dev",
		Topology:        "single",
		HA:              false,
		WithStrimzi:     true,
		WithChaos:       false,
		WithMonitoring:  false,
		WithCertManager: false,
		WithKyverno:     false,
		WithConnect:     false,
		SchemaRegistry:  "none",
	},
	"production": {
		Name:            "production",
		Topology:        "isolated",
		HA:              true,
		WithStrimzi:     true,
		WithChaos:       true,
		WithMonitoring:  true,
		WithCertManager: true,
		WithKyverno:     true,
		WithConnect:     true,
		SchemaRegistry:  "apicurio",
	},
	"ci": {
		Name:            "ci",
		Topology:        "single",
		HA:              false,
		WithStrimzi:     true,
		WithChaos:       false,
		WithMonitoring:  false,
		WithCertManager: false,
		WithKyverno:     false,
		WithConnect:     false,
		SchemaRegistry:  "none",
	},
	"minimal": {
		Name:            "minimal",
		Topology:        "single",
		HA:              false,
		WithStrimzi:     true,
		WithChaos:       false,
		WithMonitoring:  false,
		WithCertManager: false,
		WithKyverno:     false,
		WithConnect:     false,
		SchemaRegistry:  "none",
	},
	"full": {
		Name:            "full",
		Topology:        "isolated",
		HA:              false,
		WithStrimzi:     true,
		WithChaos:       true,
		WithMonitoring:  true,
		WithCertManager: true,
		WithKyverno:     true,
		WithConnect:     true,
		SchemaRegistry:  "apicurio",
	},
}

func applyProfile(profileName string, cmd *cobra.Command) error {
	p, ok := deployProfiles[profileName]
	if !ok {
		valid := []string{}
		for k := range deployProfiles {
			valid = append(valid, k)
		}
		return fmt.Errorf("unknown profile: %s. Valid profiles are: %s", profileName, strings.Join(valid, ", "))
	}

	if !cmd.Flags().Changed("topology") {
		deployTopology = p.Topology
	}
	if !cmd.Flags().Changed("ha") {
		deployHA = p.HA
	}
	if !cmd.Flags().Changed("with-strimzi") {
		deployWithStrimzi = p.WithStrimzi
	}
	if !cmd.Flags().Changed("with-chaos") {
		deployWithChaos = p.WithChaos
	}
	if !cmd.Flags().Changed("with-monitoring") {
		deployWithMonitoring = p.WithMonitoring
	}
	if !cmd.Flags().Changed("with-cert-manager") {
		deployWithCertManager = p.WithCertManager
	}
	if !cmd.Flags().Changed("with-kyverno") {
		deployWithKyverno = p.WithKyverno
	}
	if !cmd.Flags().Changed("with-kafka-connect") {
		deployWithKafkaConnect = p.WithConnect
	}
	if !cmd.Flags().Changed("with-schema-registry") {
		deployWithSchemaRegistry = p.SchemaRegistry
	}

	return nil
}
