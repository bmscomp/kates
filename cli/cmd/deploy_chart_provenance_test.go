package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestABadProvenanceStopsTheRunEvenWithACache(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	ChartVersions = "kates=0.14.2" // a new digest, so both checks run
	served := runExecCombinedFn
	runExecCombinedFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "cosign" && args[0] == "verify-blob-attestation" {
			return []byte("Error: provided artifact digest does not match any digest in statement"), errors.New("exit status 1")
		}
		return served(ctx, name, args...)
	}
	err := resolveChartRoot(context.Background(), "", false, "kates")
	var provErr *chartProvenanceError
	if !errors.As(err, &provErr) || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a provenance that does not check out must stop the run: %v", err)
	}

	attestationsFn = func(context.Context, string) ([][]byte, error) { return nil, nil }
	if err := resolveChartRoot(context.Background(), "", false, "kates"); !errors.As(err, &provErr) || !strings.Contains(err.Error(), "no attestation") {
		t.Fatalf("a chart GitHub holds no attestation for: %v", err)
	}
}

func TestAnUnreadableAttestationAPIIsAWarningAndTriedAgain(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	attestationsFn = func(context.Context, string) ([][]byte, error) {
		return nil, errors.Join(errProvenanceUnavailable, errors.New("403 rate limit exceeded"))
	}
	stderr := output.ResetForTesting()
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatalf("an unreadable attestation API must not stop the run: %v", err)
	}
	if !strings.Contains(stderr.String(), "build provenance of 1 chart(s) is not checked yet") || !strings.Contains(stderr.String(), "rate limit") {
		t.Fatalf("warning %q", stderr.String())
	}
	if d, provenance := readMarker(chartRootPath("charts/kates")); d == "" || provenance {
		t.Fatalf("marker %q provenance %v: the signature is recorded, the provenance is not", d, provenance)
	}

	t.Setenv("KATES_REQUIRE_SIGNED_CHARTS", "1")
	if err := resolveChartRoot(context.Background(), "", true, "kates"); err == nil || !strings.Contains(err.Error(), "refuses them") {
		t.Fatalf("KATES_REQUIRE_SIGNED_CHARTS=1 refuses an unchecked provenance: %v", err)
	}
	t.Setenv("KATES_REQUIRE_SIGNED_CHARTS", "")

	// Next run, the API answers: only the provenance is checked.
	attestationsFn = func(context.Context, string) ([][]byte, error) { return [][]byte{[]byte(`{}`)}, nil }
	provenanceUnchecked = nil
	n := len(callsOf(*calls, "cosign verify ghcr.io"))
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if len(callsOf(*calls, "cosign verify ghcr.io")) != n {
		t.Error("the signature, already verified, was checked again")
	}
	if _, provenance := readMarker(chartRootPath("charts/kates")); !provenance {
		t.Error("the provenance was not recorded once checked")
	}
}

func TestFetchAttestations(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch {
		case strings.HasSuffix(r.URL.Path, "/sha256:ok"):
			_, _ = w.Write([]byte(`{"attestations":[{"bundle":{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/sha256:none"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	orig := attestationsURL
	attestationsURL = srv.URL + "/repos/bmscomp/kates/attestations/"
	t.Cleanup(func() { attestationsURL = orig })

	t.Setenv("GITHUB_TOKEN", "")
	bundles, err := fetchAttestations(context.Background(), "sha256:ok")
	if err != nil || len(bundles) != 1 || !strings.Contains(string(bundles[0]), "sigstore.bundle") || auth != "" {
		t.Fatalf("200: %q %v, auth %q", bundles, err, auth)
	}
	if bundles, err := fetchAttestations(context.Background(), "sha256:none"); err != nil || len(bundles) != 0 {
		t.Fatalf("404 means none: %v %v", bundles, err)
	}
	t.Setenv("GITHUB_TOKEN", "ghs_test")
	if _, err := fetchAttestations(context.Background(), "sha256:limited"); !errors.Is(err, errProvenanceUnavailable) || auth != "Bearer ghs_test" {
		t.Fatalf("403: %v, auth %q", err, auth)
	}
}
