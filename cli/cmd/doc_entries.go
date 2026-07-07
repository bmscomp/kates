package cmd

import (
	"embed"
	"log"

	"gopkg.in/yaml.v3"
)

//go:embed data/doc_entries.yaml
var docEntriesYAML []byte

// Ensure embed is used (the directive above references the embed package).
var _ embed.FS

type yamlDocEntry struct {
	Name        string        `yaml:"name"`
	Category    string        `yaml:"category"`
	Synopsis    string        `yaml:"synopsis"`
	Short       string        `yaml:"short"`
	Description string        `yaml:"description"`
	Examples    []string      `yaml:"examples"`
	SeeAlso     []string      `yaml:"see_also"`
	Flags       []yamlDocFlag `yaml:"flags"`
}

type yamlDocFlag struct {
	Name    string `yaml:"name"`
	Short   string `yaml:"short"`
	Type    string `yaml:"type"`
	Default string `yaml:"default"`
	Desc    string `yaml:"desc"`
}

var docEntries []DocEntry

func init() {
	var raw []yamlDocEntry
	if err := yaml.Unmarshal(docEntriesYAML, &raw); err != nil {
		log.Fatalf("failed to parse embedded doc_entries.yaml: %v", err)
	}
	docEntries = make([]DocEntry, len(raw))
	for i, r := range raw {
		flags := make([]DocFlag, len(r.Flags))
		for j, f := range r.Flags {
			flags[j] = DocFlag{Name: f.Name, Short: f.Short, Type: f.Type, Default: f.Default, Desc: f.Desc}
		}
		docEntries[i] = DocEntry{
			Name:        r.Name,
			Category:    r.Category,
			Synopsis:    r.Synopsis,
			Short:       r.Short,
			Description: r.Description,
			Examples:    r.Examples,
			SeeAlso:     r.SeeAlso,
			Flags:       flags,
		}
	}
}
