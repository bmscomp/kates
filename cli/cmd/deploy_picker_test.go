package cmd

import (
	"fmt"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
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
