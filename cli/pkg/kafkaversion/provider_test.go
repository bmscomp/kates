package kafkaversion

import (
	"errors"
	"strings"
	"testing"
)

func TestLegacyModeFor(t *testing.T) {
	tests := map[string]LegacyMode{
		"2.1.0": LegacyZooKeeper,
		"2.8.2": LegacyZooKeeper,
		"3.2.3": LegacyZooKeeper,
		"3.3.0": LegacyKRaftBuilt,
		"3.5.2": LegacyKRaftBuilt,
		"3.6.2": LegacyKRaftBuilt,
		"3.7.0": LegacyKRaftOfficial,
		"3.9.1": LegacyKRaftOfficial,
		"4.0.0": LegacyKRaftOfficial,
		"4.1.2": LegacyKRaftOfficial,
	}
	for v, want := range tests {
		if got := LegacyModeFor(MustParse(v)); got != want {
			t.Errorf("LegacyModeFor(%s) = %q, want %q", v, got, want)
		}
	}
}

func TestLegacyImageFor(t *testing.T) {
	tests := []struct {
		v, registry, want string
	}{
		{"2.8.2", "", "ghcr.io/bmscomp/kates-legacy-kafka:2.8.2"},
		{"3.6.2", "", "ghcr.io/bmscomp/kates-legacy-kafka:3.6.2"},
		{"3.5.2", "registry.local:5000/kates", "registry.local:5000/kates/kates-legacy-kafka:3.5.2"},
		{"3.7.0", "", "apache/kafka:3.7.0"},
		{"3.9.1", "registry.local:5000/kates", "apache/kafka:3.9.1"},
		{"4.1.2", "", "apache/kafka:4.1.2"},
	}
	for _, tt := range tests {
		if got := LegacyImageFor(MustParse(tt.v), tt.registry); got != tt.want {
			t.Errorf("LegacyImageFor(%s, %q) = %q, want %q", tt.v, tt.registry, got, tt.want)
		}
	}
}

func TestProviderFor(t *testing.T) {
	window := NewWindow(MustParse("4.2.0"), MustParse("4.2.1"), MustParse("4.3.0"))
	tests := []struct {
		v        string
		provider Provider
		mode     LegacyMode
		wantErr  bool
	}{
		{v: "2.0.0", wantErr: true},
		{v: "1.1.1", wantErr: true},
		{v: "0.10.2", wantErr: true},
		{v: "2.1.0", provider: ProviderLegacy, mode: LegacyZooKeeper},
		{v: "2.8.2", provider: ProviderLegacy, mode: LegacyZooKeeper},
		{v: "3.4.1", provider: ProviderLegacy, mode: LegacyKRaftBuilt},
		{v: "3.9.1", provider: ProviderLegacy, mode: LegacyKRaftOfficial},
		{v: "4.1.2", provider: ProviderLegacy, mode: LegacyKRaftOfficial},
		{v: "4.2.0", provider: ProviderStrimzi},
		{v: "4.2.1", provider: ProviderStrimzi},
		{v: "4.3.0", provider: ProviderStrimzi},
		{v: "4.4.0", provider: ProviderLegacy, mode: LegacyKRaftOfficial},
	}
	for _, tt := range tests {
		t.Run(tt.v, func(t *testing.T) {
			p, m, err := ProviderFor(MustParse(tt.v), window)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ProviderFor = %q/%q, want error", p, m)
				}
				if !errors.Is(err, ErrBelowMirrorFloor) {
					t.Errorf("error %v does not wrap ErrBelowMirrorFloor", err)
				}
				for _, word := range []string{"KIP-896", "2.1.0", tt.v} {
					if !strings.Contains(err.Error(), word) {
						t.Errorf("error %q does not name %s", err, word)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ProviderFor: %v", err)
			}
			if p != tt.provider || m != tt.mode {
				t.Fatalf("ProviderFor = %q/%q, want %q/%q", p, m, tt.provider, tt.mode)
			}
		})
	}

	t.Run("empty window is all legacy", func(t *testing.T) {
		p, m, err := ProviderFor(MustParse("4.3.0"), nil)
		if err != nil || p != ProviderLegacy || m != LegacyKRaftOfficial {
			t.Fatalf("ProviderFor = %q/%q/%v", p, m, err)
		}
	})
}
