package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// chartVerified reports whether the chart in dest was verified as it is.
func chartVerified(dest string) bool {
	data, err := os.ReadFile(verifiedMarker(dest))
	return err == nil && helmDigest.MatchString("Digest: "+strings.TrimSpace(string(data)))
}

// verifyChart checks ref@digest against the publish workflow's signature. A
// digest dest was already verified at is not checked again: it names the
// same bytes.
func verifyChart(ctx context.Context, ref, digest, dest string) error {
	if data, err := os.ReadFile(verifiedMarker(dest)); err == nil && strings.TrimSpace(string(data)) == digest {
		return nil
	}
	cosign, err := cosignPathFn()
	if err != nil {
		return errNoCosign
	}
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

// checkChartSignatures says which of dirs under root hold a chart nobody
// verified: pulled without cosign, or cached that way. It is a warning, or an
// error under KATES_REQUIRE_SIGNED_CHARTS=1.
func checkChartSignatures(root string, dirs []string) error {
	var unchecked []string
	for _, dir := range dirs {
		if !chartVerified(filepath.Join(root, "charts", dir)) {
			unchecked = append(unchecked, deployChartNames[dir])
		}
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
