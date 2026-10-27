package cmd

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"github.com/charmbracelet/huh"
)

func catalogueOf(versions ...string) []strimzi.CatalogueEntry {
	out := make([]strimzi.CatalogueEntry, 0, len(versions))
	for _, v := range versions {
		out = append(out, strimzi.CatalogueEntry{Version: v})
	}
	return out
}

// The catalogue carries every chart Strimzi ever published. The picker offers
// the newest operatorPickerLimit of them and nothing older, so the select stays
// readable however long the catalogue grows.
func TestRecentCatalogueEntriesStopsAtTheLimit(t *testing.T) {
	var long []string
	for minor := 30; minor >= 0; minor-- {
		long = append(long, fmt.Sprintf("1.%d.0", minor))
	}
	got := recentCatalogueEntries(catalogueOf(long...), map[string]bool{}, operatorPickerLimit)
	if len(got) != operatorPickerLimit {
		t.Fatalf("offered %d releases, want %d", len(got), operatorPickerLimit)
	}
	if got[0].Version != "1.30.0" {
		t.Errorf("first offer is %s, want the newest (1.30.0)", got[0].Version)
	}
	if got[operatorPickerLimit-1].Version != "1.21.0" {
		t.Errorf("last offer is %s, want 1.21.0 — the list must be the newest %d, contiguous",
			got[operatorPickerLimit-1].Version, operatorPickerLimit)
	}
}

// The pin is already offered as the first option, so it must not appear twice.
// Skipping it must not cost a slot either: the list is still the limit long.
func TestRecentCatalogueEntriesSkipsThePinWithoutLosingASlot(t *testing.T) {
	entries := catalogueOf("1.9.0", "1.8.0", "1.7.0", "1.6.0")
	got := recentCatalogueEntries(entries, map[string]bool{"1.8.0": true}, 3)
	if len(got) != 3 {
		t.Fatalf("offered %d releases, want 3", len(got))
	}
	for _, e := range got {
		if e.Version == "1.8.0" {
			t.Fatalf("the pinned version was offered a second time")
		}
	}
	if got[2].Version != "1.6.0" {
		t.Errorf("skipping the pin lost a slot: last offer is %s, want 1.6.0", got[2].Version)
	}
}

// A catalogue entry whose version does not parse (a release candidate, a
// mis-tagged chart) would produce an option that only fails later, in
// resolution, after the user has already chosen it.
func TestRecentCatalogueEntriesDropsUnparseableVersions(t *testing.T) {
	got := recentCatalogueEntries(catalogueOf("not-a-version", "1.9.0"), map[string]bool{}, 5)
	if len(got) != 1 || got[0].Version != "1.9.0" {
		t.Fatalf("got %+v, want only 1.9.0", got)
	}
}

// A catalogue shorter than the limit is offered whole rather than padded.
func TestRecentCatalogueEntriesShortCatalogue(t *testing.T) {
	got := recentCatalogueEntries(catalogueOf("1.2.0", "1.1.0"), map[string]bool{}, operatorPickerLimit)
	if len(got) != 2 {
		t.Fatalf("offered %d releases, want both", len(got))
	}
}

// optionLabels flattens the picker's options to "value=label" for comparing.
func optionLabels(opts []huh.Option[string]) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Value+"="+o.Key)
	}
	return out
}

// With no operator installed, the pin leads, then the catalogue newest first,
// each marked against the pin and the v1 storage line.
func TestOperatorPickerOptionsWithoutInstalledOperator(t *testing.T) {
	opts, desc := operatorPickerOptions("1.0.1", nil, catalogueOf("1.1.0", "1.0.1", "0.49.0"))
	want := []string{
		"1.0.1=1.0.1  (pinned — tested with this release)",
		"1.1.0=1.1.0  (newer than pinned — untested)",
		"0.49.0=0.49.0  (stores v1beta2)",
	}
	if got := optionLabels(opts); !slices.Equal(got, want) {
		t.Fatalf("options:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(desc, "Installed:") {
		t.Fatalf("description names an installed operator when there is none: %q", desc)
	}
}

// An installed operator newer than the pin leads, the pin and anything older
// than the installed one are left out (Strimzi cannot downgrade), and a newer
// release is offered as an upgrade from it.
func TestOperatorPickerOptionsInstalledNewerThanThePin(t *testing.T) {
	installed := &strimzi.Operator{Namespace: "strimzi", Version: "1.1.0", Scope: strimzi.ScopeCluster}
	opts, desc := operatorPickerOptions("1.0.1", installed, catalogueOf("1.2.0", "1.1.0", "1.0.1"))
	want := []string{
		"1.1.0=1.1.0  (installed — keep)",
		"1.2.0=1.2.0  (newer than pinned — untested)  (upgrade from 1.1.0)",
	}
	if got := optionLabels(opts); !slices.Equal(got, want) {
		t.Fatalf("options:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(desc, "Installed: Strimzi 1.1.0 in namespace strimzi, cluster-wide") {
		t.Fatalf("description does not name the installed operator: %q", desc)
	}
}

// The installed operator at the pin is offered once, marked as both.
func TestOperatorPickerOptionsInstalledAtThePin(t *testing.T) {
	installed := &strimzi.Operator{Namespace: "strimzi", Version: "1.0.1", Scope: strimzi.ScopeCluster}
	opts, _ := operatorPickerOptions("1.0.1", installed, nil)
	want := []string{"1.0.1=1.0.1  (installed, pinned — keep)"}
	if got := optionLabels(opts); !slices.Equal(got, want) {
		t.Fatalf("options:\n got %q\nwant %q", got, want)
	}
}
