package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Every published chart is checked against its cosign signature before deploy
// uses it: signed keylessly by publish-charts.yml in this repository, on main
// or a release tag, which is what the README's verify command checks. A chart
// unchanged since an earlier release keeps that release's signature, so the
// identity names the workflow rather than this build's tag.
const (
	chartSignerIdentity = `^https://github\.com/bmscomp/kates/\.github/workflows/publish-charts\.yml@refs/(heads/main|tags/v.+)$`
	chartSignerIssuer   = "https://token.actions.githubusercontent.com"
)

// cosignPathFn finds cosign; tests replace it.
var cosignPathFn = func() (string, error) { return exec.LookPath("cosign") }

// errNoCosign is a chart that could not be checked: cosign is not installed.
var errNoCosign = errors.New("cosign is not installed")

// chartSignatureError is a chart whose signature did not check out. Deploy
// stops on it rather than falling back to a cached copy: a version is never
// pushed twice, so a pull that verifies differently from before is not a
// network fault.
type chartSignatureError struct {
	image string
	err   error
}

func (e *chartSignatureError) Error() string {
	return fmt.Sprintf("the signature of %s does not check out, so the chart is not used: %v", e.image, e.err)
}

// helmDigest is the "Digest: sha256:…" line `helm pull` prints.
var helmDigest = regexp.MustCompile(`(?m)^Digest:\s*(sha256:[0-9a-f]{64})\s*$`)

// requireSignedCharts makes a chart nobody could check an error, not a warning.
func requireSignedCharts() bool {
	return os.Getenv("KATES_REQUIRE_SIGNED_CHARTS") == "1"
}

// verifiedMarker records the digest the chart in dest was verified at. It sits
// beside dest, so Helm never loads it as a chart file.
func verifiedMarker(dest string) string {
	return filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".verified")
}

// readMarker is what dest's verified marker records: the digest whose
// signature checked out, and whether its build provenance did too.
func readMarker(dest string) (digest string, provenance bool) {
	data, err := os.ReadFile(verifiedMarker(dest))
	if err != nil {
		return "", false
	}
	lines := strings.Fields(string(data))
	if len(lines) == 0 || !helmDigest.MatchString("Digest: "+lines[0]) {
		return "", false
	}
	return lines[0], slices.Contains(lines[1:], "provenance")
}

// markerContent is the marker of a chart verified at digest.
func markerContent(digest string, provenance bool) []byte {
	if provenance {
		return []byte(digest + "\nprovenance\n")
	}
	return []byte(digest + "\n")
}

// chartVerified reports whether the chart in dest was verified as it is.
func chartVerified(dest string) bool {
	digest, _ := readMarker(dest)
	return digest != ""
}

// verifyChart checks ref@digest against the publish workflow's signature and
// its build provenance, and says whether the provenance was checked. A
// digest dest was already verified at is not checked again: it names the
// same bytes. Only an unchecked provenance is tried again.
func verifyChart(ctx context.Context, ref, digest, dest string) (provenance bool, err error) {
	known, knownProvenance := readMarker(dest)
	if known == digest && knownProvenance {
		return true, nil
	}
	cosign, err := cosignPathFn()
	if err != nil {
		if known == digest {
			return false, nil
		}
		return false, errNoCosign
	}
	if known != digest {
		if err := verifySignature(ctx, cosign, ref, digest); err != nil {
			return false, err
		}
	}
	return verifyProvenance(ctx, cosign, digest)
}

// verifySignature is cosign verify of ref@digest.
func verifySignature(ctx context.Context, cosign, ref, digest string) error {
	image := strings.TrimPrefix(ref, "oci://") + "@" + digest
	vctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"verify", image,
		"--certificate-identity-regexp", chartSignerIdentity,
		"--certificate-oidc-issuer", chartSignerIssuer}
	if chartRegistryPlainHTTP() {
		args = append(args, "--allow-http-registry")
	}
	out, err := runExecCombinedFn(vctx, cosign, args...)
	if err != nil {
		return &chartSignatureError{image: image, err: fmt.Errorf("%w: %s", err, lastLine(string(out)))}
	}
	return nil
}

// attestationsURL lists the build provenance attestations GitHub holds for a
// digest; publish-charts.yml records one per chart it pushes.
var attestationsURL = "https://api.github.com/repos/bmscomp/kates/attestations/"

// errProvenanceUnavailable is a provenance nobody could check: GitHub's
// attestation API did not answer, or refused the request (an anonymous
// caller gets 60 an hour; GITHUB_TOKEN raises that).
var errProvenanceUnavailable = errors.New("GitHub's attestation API could not be read")

// attestationsFn reads the Sigstore bundles of digest's attestations; tests
// replace it.
var attestationsFn = fetchAttestations

func fetchAttestations(ctx context.Context, digest string) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attestationsURL+digest, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errProvenanceUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: %s", errProvenanceUnavailable, resp.Status)
	}
	var body struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: %v", errProvenanceUnavailable, err)
	}
	bundles := make([][]byte, 0, len(body.Attestations))
	for _, a := range body.Attestations {
		bundles = append(bundles, a.Bundle)
	}
	return bundles, nil
}

// chartProvenanceError is a chart whose build provenance did not check out.
// Like a bad signature, it stops the run.
type chartProvenanceError struct {
	digest string
	err    error
}

func (e *chartProvenanceError) Error() string {
	return fmt.Sprintf("the build provenance of %s does not check out, so the chart is not used: %v", e.digest, e.err)
}

// verifyProvenance checks that one of GitHub's attestations for digest is a
// SLSA provenance signed by publish-charts.yml whose subject is digest. It
// returns false, with no error, when the attestations could not be read.
func verifyProvenance(ctx context.Context, cosign, digest string) (bool, error) {
	bundles, err := attestationsFn(ctx, digest)
	if errors.Is(err, errProvenanceUnavailable) {
		provenanceUnchecked = err
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(bundles) == 0 {
		return false, &chartProvenanceError{digest: digest, err: errors.New("GitHub holds no attestation for it")}
	}
	dir, err := os.MkdirTemp("", "kates-provenance-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	var last error
	for i, b := range bundles {
		path := filepath.Join(dir, fmt.Sprintf("bundle-%d.json", i))
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return false, err
		}
		vctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		out, err := runExecCombinedFn(vctx, cosign, "verify-blob-attestation", "--bundle", path,
			"--certificate-identity-regexp", chartSignerIdentity,
			"--certificate-oidc-issuer", chartSignerIssuer,
			"--type", "slsaprovenance1",
			"--digest", strings.TrimPrefix(digest, "sha256:"), "--digestAlg", "sha256")
		cancel()
		if err == nil {
			return true, nil
		}
		last = fmt.Errorf("%w: %s", err, lastLine(string(out)))
	}
	return false, &chartProvenanceError{digest: digest, err: last}
}

// provenanceUnchecked is why this run checked no provenance, for the
// warning checkChartSignatures gives.
var provenanceUnchecked error

// checkChartSignatures says which of dirs under root hold a chart nobody
// verified: pulled without cosign, or cached that way. It is a warning, or an
// error under KATES_REQUIRE_SIGNED_CHARTS=1.
func checkChartSignatures(root string, dirs []string) error {
	var unchecked, unattested []string
	for _, dir := range dirs {
		switch digest, provenance := readMarker(filepath.Join(root, "charts", dir)); {
		case digest == "":
			unchecked = append(unchecked, deployChartNames[dir])
		case !provenance:
			unattested = append(unattested, deployChartNames[dir])
		}
	}
	if len(unattested) > 0 && len(unchecked) == 0 {
		why := "GitHub's attestation API was not read"
		if provenanceUnchecked != nil {
			why = provenanceUnchecked.Error()
		}
		msg := fmt.Sprintf("the build provenance of %d chart(s) is not checked yet (%s): %s. Their signatures are; a later run tries again, and GITHUB_TOKEN lifts the API's anonymous limit", len(unattested), why, strings.Join(unattested, ", "))
		if requireSignedCharts() {
			return errors.New(msg + "; KATES_REQUIRE_SIGNED_CHARTS=1 refuses them")
		}
		warnStderr(msg)
		return nil
	}
	if len(unchecked) == 0 {
		return nil
	}
	msg := fmt.Sprintf("%d chart(s) not signature-checked, because cosign is not installed or was not when they were pulled: %s. Install it (brew install cosign) and run again", len(unchecked), strings.Join(unchecked, ", "))
	if requireSignedCharts() {
		return errors.New(msg + "; KATES_REQUIRE_SIGNED_CHARTS=1 refuses unchecked charts")
	}
	warnStderr(msg + "; KATES_REQUIRE_SIGNED_CHARTS=1 makes this an error")
	return nil
}

// lastLine is the last non-empty line of a command's output: cosign ends with
// the error that matters.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
