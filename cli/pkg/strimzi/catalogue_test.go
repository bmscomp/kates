package strimzi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchJSON is "helm search repo strimzi/strimzi-kafka-operator --versions
// -o json" as helm prints it, with a foreign row that must be filtered and
// the order scrambled.
const searchJSON = `[
{"name":"strimzi/strimzi-kafka-operator","version":"1.0.1","app_version":"1.0.1","description":"Strimzi: Apache Kafka running on Kubernetes"},
{"name":"strimzi/strimzi-drain-cleaner","version":"1.4.0","app_version":"1.4.0","description":"Strimzi Drain Cleaner"},
{"name":"strimzi/strimzi-kafka-operator","version":"1.1.0","app_version":"1.1.0","description":"Strimzi: Apache Kafka running on Kubernetes"},
{"name":"strimzi/strimzi-kafka-operator","version":"0.51.0","app_version":"0.51.0","description":"Strimzi: Apache Kafka running on Kubernetes"},
{"name":"strimzi/strimzi-kafka-operator","version":"1.0.0","app_version":"1.0.0","description":"Strimzi: Apache Kafka running on Kubernetes"},
{"name":"strimzi/strimzi-kafka-operator","version":"0.48.0","app_version":"0.48.0","description":"Strimzi: Apache Kafka running on Kubernetes"}
]`

func catalogueCommands(cacheDir string) (add, search string) {
	repoArgs := " --repository-config " + filepath.Join(cacheDir, "repositories.yaml") + " --repository-cache " + filepath.Join(cacheDir, "repo-cache")
	add = "helm repo add strimzi https://strimzi.io/charts/" + repoArgs + " --force-update"
	search = "helm search repo strimzi/strimzi-kafka-operator --versions -o json" + repoArgs
	return add, search
}

func TestFetchCatalogue(t *testing.T) {
	ctx := context.Background()
	wantVersions := []string{"1.1.0", "1.0.1", "1.0.0", "0.51.0", "0.48.0"}
	check := func(t *testing.T, c *Catalogue) {
		t.Helper()
		if len(c.Entries) != len(wantVersions) {
			t.Fatalf("entries = %+v", c.Entries)
		}
		for i, v := range wantVersions {
			if c.Entries[i].Version != v || c.Entries[i].AppVersion != v {
				t.Errorf("entry %d = %+v, want %s", i, c.Entries[i], v)
			}
		}
		if newest, ok := c.Newest(); !ok || newest.Version != "1.1.0" {
			t.Errorf("Newest = %+v, %v", newest, ok)
		}
		if !c.Has("1.0.1") || c.Has("1.4.0") || c.Has("") {
			t.Error("Has is wrong")
		}
	}

	t.Run("fetches and caches", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "kates", "strimzi")
		add, search := catalogueCommands(cacheDir)
		r := newFakeRunner(t)
		r.outputs[add] = `"strimzi" has been added to your repositories`
		r.outputs[search] = searchJSON
		c, err := FetchCatalogue(ctx, r, cacheDir, false)
		if err != nil {
			t.Fatalf("FetchCatalogue: %v", err)
		}
		r.assertCalls(add, search)
		check(t, c)
		if time.Since(c.FetchedAt) > time.Minute {
			t.Errorf("FetchedAt = %v", c.FetchedAt)
		}
		data, err := os.ReadFile(filepath.Join(cacheDir, "catalogue.json"))
		if err != nil {
			t.Fatalf("cache file: %v", err)
		}
		var cached Catalogue
		if err := json.Unmarshal(data, &cached); err != nil {
			t.Fatalf("cache file: %v", err)
		}
		check(t, &cached)

		// a second call within the TTL runs nothing
		r2 := newFakeRunner(t)
		c2, err := FetchCatalogue(ctx, r2, cacheDir, false)
		if err != nil {
			t.Fatalf("FetchCatalogue (cached): %v", err)
		}
		r2.assertCalls()
		check(t, c2)

		// refresh runs the commands again
		r3 := newFakeRunner(t)
		r3.outputs[add] = ""
		r3.outputs[search] = searchJSON
		if _, err := FetchCatalogue(ctx, r3, cacheDir, true); err != nil {
			t.Fatalf("FetchCatalogue (refresh): %v", err)
		}
		r3.assertCalls(add, search)
	})

	t.Run("stale cache is refetched", func(t *testing.T) {
		cacheDir := t.TempDir()
		stale := Catalogue{FetchedAt: time.Now().Add(-25 * time.Hour), Entries: []CatalogueEntry{{Version: "0.1.0"}}}
		data, _ := json.Marshal(stale)
		writeFile(t, filepath.Join(cacheDir, "catalogue.json"), string(data))
		add, search := catalogueCommands(cacheDir)
		r := newFakeRunner(t)
		r.outputs[add] = ""
		r.outputs[search] = searchJSON
		c, err := FetchCatalogue(ctx, r, cacheDir, false)
		if err != nil {
			t.Fatalf("FetchCatalogue: %v", err)
		}
		r.assertCalls(add, search)
		check(t, c)
	})

	t.Run("corrupt cache is refetched", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeFile(t, filepath.Join(cacheDir, "catalogue.json"), "{not json")
		add, search := catalogueCommands(cacheDir)
		r := newFakeRunner(t)
		r.outputs[add] = ""
		r.outputs[search] = searchJSON
		if _, err := FetchCatalogue(ctx, r, cacheDir, false); err != nil {
			t.Fatalf("FetchCatalogue: %v", err)
		}
		r.assertCalls(add, search)
	})

	t.Run("helm failures propagate", func(t *testing.T) {
		cacheDir := t.TempDir()
		add, search := catalogueCommands(cacheDir)
		r := newFakeRunner(t)
		r.errors[add] = errors.New("helm repo add: exit status 1: dial tcp: no route to host")
		_, err := FetchCatalogue(ctx, r, cacheDir, false)
		if err == nil || !strings.Contains(err.Error(), "no route to host") {
			t.Fatalf("err = %v", err)
		}
		r = newFakeRunner(t)
		r.outputs[add] = ""
		r.errors[search] = errors.New("boom")
		if _, err := FetchCatalogue(ctx, r, cacheDir, false); err == nil {
			t.Fatal("search failure: want error")
		}
		r = newFakeRunner(t)
		r.outputs[add] = ""
		r.outputs[search] = `[]`
		if _, err := FetchCatalogue(ctx, r, cacheDir, false); err == nil {
			t.Fatal("empty search: want error")
		}
		r = newFakeRunner(t)
		r.outputs[add] = ""
		r.outputs[search] = `not json`
		if _, err := FetchCatalogue(ctx, r, cacheDir, false); err == nil {
			t.Fatal("bad json: want error")
		}
		if _, err := os.Stat(filepath.Join(cacheDir, "catalogue.json")); err == nil {
			t.Error("a failed fetch must not write the cache")
		}
	})
}

func TestSortNewestFirst(t *testing.T) {
	entries := []CatalogueEntry{{Version: "1.0.0"}, {Version: "weird"}, {Version: "1.1.0"}, {Version: "0.9.0"}, {Version: "alpha"}}
	sortNewestFirst(entries)
	var got []string
	for _, e := range entries {
		got = append(got, e.Version)
	}
	if want := "1.1.0 1.0.0 0.9.0 weird alpha"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestCatalogueNilSafety(t *testing.T) {
	var c *Catalogue
	if _, ok := c.Newest(); ok {
		t.Error("nil catalogue has a newest")
	}
	if c.Has("1.1.0") {
		t.Error("nil catalogue has a version")
	}
}

func TestPullChart(t *testing.T) {
	ctx := context.Background()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	pull := "helm pull " + OCIChart + " --version 1.0.1 --destination " + cacheDir
	want := filepath.Join(cacheDir, "strimzi-kafka-operator-1.0.1.tgz")

	t.Run("pulls when absent", func(t *testing.T) {
		r := newFakeRunner(t)
		r.hooks[pull] = func() { writeFile(t, want, "tgz") }
		r.outputs[pull] = "Pulled: quay.io/strimzi-helm/strimzi-kafka-operator:1.0.1"
		got, err := PullChart(ctx, r, cacheDir, "1.0.1")
		if err != nil {
			t.Fatalf("PullChart: %v", err)
		}
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		r.assertCalls(pull)
	})

	t.Run("cached chart is not pulled again", func(t *testing.T) {
		r := newFakeRunner(t)
		got, err := PullChart(ctx, r, cacheDir, "1.0.1")
		if err != nil || got != want {
			t.Fatalf("PullChart = %q, %v", got, err)
		}
		r.assertCalls()
	})

	t.Run("pull that produces no file", func(t *testing.T) {
		r := newFakeRunner(t)
		key := "helm pull " + OCIChart + " --version 1.0.0 --destination " + cacheDir
		r.outputs[key] = ""
		_, err := PullChart(ctx, r, cacheDir, "1.0.0")
		if err == nil || !strings.Contains(err.Error(), "did not produce") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("pull failure", func(t *testing.T) {
		r := newFakeRunner(t)
		key := "helm pull " + OCIChart + " --version 9.9.9 --destination " + cacheDir
		r.errors[key] = errors.New("Error: failed to fetch")
		_, err := PullChart(ctx, r, cacheDir, "9.9.9")
		if err == nil || !strings.Contains(err.Error(), "failed to fetch") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unsafe version", func(t *testing.T) {
		for _, v := range []string{"", "..", "../x", "a/b", `a\b`} {
			if _, err := PullChart(ctx, newFakeRunner(t), cacheDir, v); err == nil {
				t.Errorf("PullChart(%q): want error", v)
			}
		}
	})
}

func TestDefaultCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	got, err := DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/xdg-cache", "kates", "strimzi") {
		t.Errorf("DefaultCacheDir = %q", got)
	}

	t.Setenv("XDG_CACHE_HOME", "")
	got, err = DefaultCacheDir()
	if err != nil {
		t.Skipf("no user cache dir here: %v", err)
	}
	base, _ := os.UserCacheDir()
	if got != filepath.Join(base, "kates", "strimzi") {
		t.Errorf("DefaultCacheDir = %q, want under %q", got, base)
	}
}
