package strimzi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

const (
	// RepoName is the name the kates-owned Helm repository config gives the
	// Strimzi chart repository.
	RepoName = "strimzi"
	// RepoURL is the Strimzi chart repository index.
	RepoURL = "https://strimzi.io/charts/"
	// OCIChart is the OCI reference of the operator chart, the registry the
	// pin is pulled from.
	OCIChart = "oci://quay.io/strimzi-helm/strimzi-kafka-operator"
	// CatalogueTTL is how long a cached catalogue is served without a
	// refresh.
	CatalogueTTL = 24 * time.Hour

	catalogueFile = "catalogue.json"
	repoConfig    = "repositories.yaml"
	repoCache     = "repo-cache"
)

// CatalogueEntry is one published operator chart version.
type CatalogueEntry struct {
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Description string `json:"description,omitempty"`
}

// Catalogue is the list of published operator chart versions, newest first.
type Catalogue struct {
	FetchedAt time.Time        `json:"fetchedAt"`
	Entries   []CatalogueEntry `json:"entries"`
}

// Newest returns the newest published version; ok is false when the
// catalogue is empty.
func (c *Catalogue) Newest() (CatalogueEntry, bool) {
	if c == nil || len(c.Entries) == 0 {
		return CatalogueEntry{}, false
	}
	return c.Entries[0], true
}

// Has reports whether version is a published chart version.
func (c *Catalogue) Has(version string) bool {
	if c == nil {
		return false
	}
	return slices.ContainsFunc(c.Entries, func(e CatalogueEntry) bool { return e.Version == version })
}

// FetchCatalogue returns the published operator chart versions. A cache at
// <cacheDir>/catalogue.json younger than CatalogueTTL is used unless refresh
// is set; otherwise the Strimzi repository is added to a kates-owned Helm
// repository config under cacheDir (the user's own helm repo list is never
// touched), searched, and the result cached.
func FetchCatalogue(ctx context.Context, r Runner, cacheDir string, refresh bool) (*Catalogue, error) {
	cachePath := filepath.Join(cacheDir, catalogueFile)
	if !refresh {
		if c, ok := readCatalogueCache(cachePath); ok {
			return c, nil
		}
	}
	repoArgs := []string{
		"--repository-config", filepath.Join(cacheDir, repoConfig),
		"--repository-cache", filepath.Join(cacheDir, repoCache),
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	addArgs := append([]string{"repo", "add", RepoName, RepoURL}, repoArgs...)
	if _, err := r.Run(ctx, "helm", append(addArgs, "--force-update")...); err != nil {
		return nil, fmt.Errorf("add %s chart repository: %w", RepoName, err)
	}
	searchArgs := append([]string{"search", "repo", RepoName + "/" + ChartName, "--versions", "-o", "json"}, repoArgs...)
	out, err := r.Run(ctx, "helm", searchArgs...)
	if err != nil {
		return nil, fmt.Errorf("search %s chart repository: %w", RepoName, err)
	}
	entries, err := parseSearchOutput(out)
	if err != nil {
		return nil, err
	}
	c := &Catalogue{FetchedAt: time.Now().UTC(), Entries: entries}
	if err := writeCatalogueCache(cachePath, c); err != nil {
		return nil, err
	}
	return c, nil
}

// parseSearchOutput reads "helm search repo … -o json", keeps the operator
// chart's own entries and orders them newest first.
func parseSearchOutput(out string) ([]CatalogueEntry, error) {
	var rows []struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		AppVersion  string `json:"app_version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rows); err != nil {
		return nil, fmt.Errorf("parse helm search output: %w", err)
	}
	want := RepoName + "/" + ChartName
	var entries []CatalogueEntry
	for _, row := range rows {
		if row.Name != want {
			continue
		}
		entries = append(entries, CatalogueEntry{Version: row.Version, AppVersion: row.AppVersion, Description: row.Description})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no %s versions in helm search output", want)
	}
	sortNewestFirst(entries)
	return entries, nil
}

// sortNewestFirst orders entries by semantic version, newest first; versions
// that do not parse as x.y.z sort after those that do, by string.
func sortNewestFirst(entries []CatalogueEntry) {
	slices.SortStableFunc(entries, func(a, b CatalogueEntry) int {
		av, aErr := kafkaversion.Parse(a.Version)
		bv, bErr := kafkaversion.Parse(b.Version)
		switch {
		case aErr == nil && bErr == nil:
			return kafkaversion.Compare(bv, av)
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			return strings.Compare(b.Version, a.Version)
		}
	})
}

func readCatalogueCache(path string) (*Catalogue, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c Catalogue
	if err := json.Unmarshal(data, &c); err != nil || len(c.Entries) == 0 {
		return nil, false
	}
	if c.FetchedAt.IsZero() || time.Since(c.FetchedAt) >= CatalogueTTL {
		return nil, false
	}
	return &c, true
}

func writeCatalogueCache(path string, c *Catalogue) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode catalogue: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write catalogue cache: %w", err)
	}
	return nil
}

// ChartFileName is the tarball name helm pull gives the operator chart at a
// version.
func ChartFileName(version string) string {
	return ChartName + "-" + version + ".tgz"
}

// PullChart returns the path of the operator chart tarball for version under
// cacheDir, pulling it from the OCI registry when it is not there yet.
func PullChart(ctx context.Context, r Runner, cacheDir, version string) (string, error) {
	if err := checkVersionSegment(version); err != nil {
		return "", err
	}
	dest := filepath.Join(cacheDir, ChartFileName(version))
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	if _, err := r.Run(ctx, "helm", "pull", OCIChart, "--version", version, "--destination", cacheDir); err != nil {
		return "", fmt.Errorf("pull %s %s: %w", ChartName, version, err)
	}
	if _, err := os.Stat(dest); err != nil {
		return "", fmt.Errorf("helm pull did not produce %s: %w", dest, err)
	}
	return dest, nil
}

// checkVersionSegment rejects a version that could not be a file or
// directory name of its own.
func checkVersionSegment(version string) error {
	if version == "" || version == "." || version == ".." || strings.ContainsAny(version, `/\`) {
		return fmt.Errorf("invalid Strimzi version %q", version)
	}
	return nil
}

// DefaultCacheDir is where pulled charts, generated wrapper charts and the
// catalogue live: $XDG_CACHE_HOME/kates/strimzi, or the user cache
// directory's kates/strimzi.
func DefaultCacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", fmt.Errorf("locate user cache directory: %w", err)
		}
	}
	if base == "" {
		return "", errors.New("no user cache directory")
	}
	return filepath.Join(base, "kates", "strimzi"), nil
}
