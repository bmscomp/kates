package cmd

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestChartSignerIdentityIsThePublishWorkflow(t *testing.T) {
	re := regexp.MustCompile(chartSignerIdentity)
	for san, want := range map[string]bool{
		"https://github.com/bmscomp/kates/.github/workflows/publish-charts.yml@refs/tags/v1.29.3": true,
		"https://github.com/bmscomp/kates/.github/workflows/publish-charts.yml@refs/heads/main":   true,
		"https://github.com/bmscomp/kates/.github/workflows/publish-charts.yml@refs/heads/feat/x": false,
		"https://github.com/bmscomp/kates/.github/workflows/release.yml@refs/tags/v1.29.3":        false,
		"https://github.com/evil/kates/.github/workflows/publish-charts.yml@refs/tags/v1.29.3":    false,
	} {
		if re.MatchString(san) != want {
			t.Errorf("%s: matched %v, want %v", san, !want, want)
		}
	}
}

func TestPublishedChartsAreSignatureChecked(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	digest := fakeDigest("kates", "0.14.1")
	want := "cosign verify ghcr.io/bmscomp/charts/kates@" + digest +
		" --certificate-identity-regexp " + chartSignerIdentity +
		" --certificate-oidc-issuer https://token.actions.githubusercontent.com"
	got := callsOf(*calls, "cosign")
	if len(got) != 2 || strings.Join(got[0], " ") != want {
		t.Fatalf("cosign calls %v, want [%s] and the provenance check", got, want)
	}
	// The provenance: a SLSA attestation from the same signer, about this digest.
	prov := strings.Join(got[1], " ")
	for _, part := range []string{"cosign verify-blob-attestation --bundle ", "--certificate-identity-regexp " + chartSignerIdentity, "--type slsaprovenance1", "--digest " + strings.TrimPrefix(digest, "sha256:") + " --digestAlg sha256"} {
		if !strings.Contains(prov, part) {
			t.Errorf("provenance check %q lacks %q", prov, part)
		}
	}
	if d, provenance := readMarker(chartRootPath("charts/kates")); d != digest || !provenance {
		t.Fatalf("verified marker records %q, provenance %v; want %s and provenance", d, provenance, digest)
	}
}

func TestABadSignatureStopsTheRunEvenWithACache(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	served := runExecCombinedFn
	other := fakeDigest("kates", "tampered")
	runExecCombinedFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "cosign" {
			return []byte("Error: no matching signatures"), errors.New("exit status 1")
		}
		out, err := served(ctx, name, args...)
		return []byte(strings.ReplaceAll(string(out), fakeDigest("kates", "0.14.1"), other)), err
	}
	err := resolveChartRoot(context.Background(), "", false, "kates")
	var sigErr *chartSignatureError
	if !errors.As(err, &sigErr) || !strings.Contains(err.Error(), "no matching signatures") {
		t.Fatalf("a chart that fails its signature must stop the run, got %v", err)
	}
	if d, _ := readMarker(chartRootPath("charts/kates")); d != fakeDigest("kates", "0.14.1") {
		t.Fatalf("the refused chart replaced the verified one: marker records %q", d)
	}
}

func TestChartsWithoutCosignGoInUncheckedWithOneWarning(t *testing.T) {
	withChartBuild(t, "kates=0.14.1,kafka-ui=0.3.2")
	calls := fakeHelmPull(t)
	cosignPathFn = func() (string, error) { return "", errors.New("not found") }
	stderr := output.ResetForTesting()
	if err := resolveChartRoot(context.Background(), "", false, "kafka-ui", "kates"); err != nil {
		t.Fatalf("without cosign the charts still go in: %v", err)
	}
	if n := strings.Count(stderr.String(), "not signature-checked"); n != 1 || !strings.Contains(stderr.String(), "kafka-ui, kates") {
		t.Fatalf("want one warning naming both charts, got %q", stderr.String())
	}
	if len(callsOf(*calls, "cosign")) != 0 || chartVerified(chartRootPath("charts/kates")) {
		t.Fatal("an unchecked chart is marked verified")
	}

	t.Setenv("KATES_REQUIRE_SIGNED_CHARTS", "1")
	if err := resolveChartRoot(context.Background(), "", true, "kates"); err == nil || !strings.Contains(err.Error(), "KATES_REQUIRE_SIGNED_CHARTS=1 refuses") {
		t.Fatalf("an unchecked cached chart must be refused under KATES_REQUIRE_SIGNED_CHARTS=1: %v", err)
	}
}

func TestAVerifiedDigestNeedsNoCosignToStayVerified(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	calls := fakeHelmPull(t)
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	cosignPathFn = func() (string, error) { return "", errors.New("not found") }
	stderr := output.ResetForTesting()
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "not signature-checked") || !chartVerified(chartRootPath("charts/kates")) {
		t.Fatalf("the same digest as the verified one is still verified; got %q", stderr.String())
	}
	if n := len(callsOf(*calls, "cosign verify ghcr.io")); n != 1 {
		t.Fatalf("the signature was checked %d times, want once", n)
	}
}

func TestAPullWithoutADigestIsRefused(t *testing.T) {
	withChartBuild(t, "kates=0.14.1")
	fakeHelmPull(t)
	served := runExecCombinedFn
	runExecCombinedFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		_, err := served(ctx, name, args...)
		return []byte("Pulled: somewhere\n"), err
	}
	if err := resolveChartRoot(context.Background(), "", false, "kates"); err == nil || !strings.Contains(err.Error(), "no digest") {
		t.Fatalf("a pull whose digest is unknown cannot be checked: %v", err)
	}
}
