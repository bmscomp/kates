package cmd

import (
	"context"
	"errors"
	"net/http"

	"github.com/bmscomp/kates/cli/client"
	"golang.org/x/sync/errgroup"
)

// The summary section of security_evidence (mcp_tools_security.go): every
// check's headline, read in parallel. One check failing does not cost the
// others: each headline says whether it is available.

type mcpSecuritySummary struct {
	Posture    mcpPostureHeadline    `json:"posture"`
	Pentest    mcpPentestHeadline    `json:"pentest"`
	Compliance mcpComplianceHeadline `json:"compliance"`
	TLS        mcpChecksHeadline     `json:"tls"`
	Certs      mcpCertsHeadline      `json:"certs"`
	CVE        mcpCVEHeadline        `json:"cve"`
	Drift      mcpDriftHeadline      `json:"drift"`
	ConfigDiff mcpConfigDiffHeadline `json:"configDiff"`
}

type mcpSecHeadline struct {
	Available bool         `json:"available" jsonschema:"false when this check could not run; its other fields then say nothing"`
	ErrorCode mcpErrorCode `json:"errorCode,omitempty"`
	Reason    string       `json:"reason,omitempty" jsonschema:"why not, in this server's words"`
	Error     mcpUntrusted `json:"error,omitempty" jsonschema:"why not, in the backend's words"`
}

type mcpSecCheckRef struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Severity string `json:"severity,omitempty"`
}

type mcpPostureHeadline struct {
	mcpSecHeadline
	Grade    string           `json:"grade,omitempty"`
	Total    int              `json:"total"`
	Passed   int              `json:"passed"`
	Warnings int              `json:"warnings"`
	Failures int              `json:"failures"`
	Problems []mcpSecCheckRef `json:"problems" jsonschema:"checks that are not PASS, FAIL first; section posture has their detail"`
}

type mcpPentestHeadline struct {
	mcpSecHeadline
	Total      int      `json:"total"`
	Protected  int      `json:"protected"`
	Vulnerable int      `json:"vulnerable"`
	Findings   []string `json:"findings" jsonschema:"ids of the VULNERABLE checks"`
}

type mcpComplianceHeadline struct {
	mcpSecHeadline
	Frameworks []mcpComplianceFramework `json:"frameworks"`
}

type mcpChecksHeadline struct {
	mcpSecHeadline
	Total    int              `json:"total"`
	Problems []mcpSecCheckRef `json:"problems" jsonschema:"checks that are not PASS, FAIL first"`
}

type mcpCertsHeadline struct {
	mcpSecHeadline
	BrokersInspected []int            `json:"brokersInspected" jsonschema:"ids of the brokers whose configuration was read, the first broker only"`
	TotalBrokers     int              `json:"totalBrokers"`
	Problems         []mcpSecCheckRef `json:"problems" jsonschema:"checks that are not PASS, FAIL first"`
}

type mcpCVEHeadline struct {
	mcpSecHeadline
	KafkaVersion string   `json:"kafkaVersion,omitempty" jsonschema:"the version the backend compared its list with; unknown when it had none, and the check is then not available"`
	Grade        string   `json:"grade,omitempty"`
	Total        int      `json:"total"`
	Vulnerable   int      `json:"vulnerable"`
	Patched      int      `json:"patched"`
	Findings     []string `json:"findings" jsonschema:"ids of the VULNERABLE entries"`
}

type mcpDriftHeadline struct {
	mcpSecHeadline
	HasBaseline             bool     `json:"hasBaseline" jsonschema:"false when no baseline is saved: there is nothing to compare with, the counts are absent, and this server cannot save one"`
	BaselineTimestamp       string   `json:"baselineTimestamp,omitempty"`
	BaselineGrade           string   `json:"baselineGrade,omitempty"`
	CurrentGrade            string   `json:"currentGrade,omitempty"`
	Improved                *int     `json:"improved,omitempty" jsonschema:"checks better than in the baseline, notInBaseline included"`
	Degraded                *int     `json:"degraded,omitempty"`
	Unchanged               *int     `json:"unchanged,omitempty"`
	NotInBaseline           *int     `json:"notInBaseline,omitempty" jsonschema:"checks the baseline lacks, as when it was saved before the audit gained them: the backend counts each IMPROVED whatever its status, FAIL included"`
	NotInBaselineNotPassing []string `json:"notInBaselineNotPassing,omitempty" jsonschema:"names of the checks the baseline lacks that do not pass now"`
	DegradedChecks          []string `json:"degradedChecks" jsonschema:"names of the checks that got worse since the baseline"`
}

type mcpConfigDiffHeadline struct {
	mcpSecHeadline
	BrokerCount    int      `json:"brokerCount"`
	KeysChecked    int      `json:"keysChecked"`
	MismatchCount  int      `json:"mismatchCount"`
	MismatchedKeys []string `json:"mismatchedKeys"`
}

// mcpSecurityReads holds the eight reads of a summary.
type mcpSecurityReads struct {
	audit         *client.SecurityAuditReport
	auditErr      error
	pentest       *client.SecurityPentestReport
	pentestErr    error
	compliance    *client.SecurityComplianceReport
	complianceErr error
	tls           *client.SecurityTLSReport
	tlsErr        error
	certs         *client.SecurityCertsReport
	certsErr      error
	cve           *client.SecurityCVEReport
	cveErr        error
	drift         *client.SecurityDriftReport
	driftErr      error
	configDiff    *client.SecurityConfigDiffReport
	configDiffErr error
}

func mcpReadAllSecurity(ctx context.Context, call *mcpCall) (*mcpSecurityReads, error) {
	r := &mcpSecurityReads{}
	c := call.Client()
	var g errgroup.Group
	// Each read records its own error, so one failing does not cancel the
	// others; only a panic ends the group.
	call.Go(&g, func() error { r.audit, r.auditErr = c.SecurityAuditReport(ctx); return nil })
	call.Go(&g, func() error { r.pentest, r.pentestErr = c.SecurityPentestReport(ctx); return nil })
	call.Go(&g, func() error { r.compliance, r.complianceErr = c.SecurityComplianceReport(ctx); return nil })
	call.Go(&g, func() error { r.tls, r.tlsErr = c.SecurityTLSReport(ctx); return nil })
	call.Go(&g, func() error { r.certs, r.certsErr = c.SecurityCertsReport(ctx); return nil })
	call.Go(&g, func() error { r.cve, r.cveErr = c.SecurityCVEReport(ctx); return nil })
	call.Go(&g, func() error { r.drift, r.driftErr = c.SecurityDriftReport(ctx); return nil })
	call.Go(&g, func() error { r.configDiff, r.configDiffErr = c.SecurityConfigDiffReport(ctx); return nil })
	return r, g.Wait()
}

func mcpSecuritySummaryOf(ctx context.Context, call *mcpCall, out *mcpSecurityEvidenceOut) error {
	r, err := mcpReadAllSecurity(ctx, call)
	if err != nil {
		return err
	}
	if err := r.sharedError(call); err != nil {
		return err
	}
	call.Caveat(
		mcpCaveatSecurityConfigReadSilent, mcpCaveatSecurityBrokerWideKeys, mcpCaveatPentestConfigOnly,
		mcpCaveatSecurityComplianceByCategory, mcpCaveatSecurityTLSConfigOnly, mcpCaveatCVEFixedList,
		mcpCaveatSecurityDriftNewChecks,
	)
	for _, ran := range []bool{mcpSecMayHaveRun(r.auditErr), mcpSecMayHaveRun(r.complianceErr), mcpDriftRanAudit(r.drift, r.driftErr)} {
		if ran {
			mcpAuditRan(call, out, 1)
		}
	}
	s := &mcpSecuritySummary{
		Posture:    mcpPostureHeadlineOf(call, r.audit, r.auditErr),
		Pentest:    mcpPentestHeadlineOf(call, r.pentest, r.pentestErr),
		Compliance: mcpComplianceHeadlineOf(call, r.compliance, r.complianceErr),
		TLS:        mcpTLSHeadlineOf(call, r.tls, r.tlsErr),
		Certs:      mcpCertsHeadlineOf(call, r.certs, r.certsErr),
		CVE:        mcpCVEHeadlineOf(call, r.cve, r.cveErr),
		Drift:      mcpDriftHeadlineOf(call, r.drift, r.driftErr),
		ConfigDiff: mcpConfigDiffHeadlineOf(call, r.configDiff, r.configDiffErr),
	}
	out.Summary = s
	return nil
}

// sharedError returns the error every read failed with, when all eight failed
// with the same one: the API or its access control refused them all, and
// eight headlines saying so would hide it.
func (r *mcpSecurityReads) sharedError(call *mcpCall) error {
	errs := []error{r.auditErr, r.pentestErr, r.complianceErr, r.tlsErr, r.certsErr, r.cveErr, r.driftErr, r.configDiffErr}
	var code mcpErrorCode
	for i, err := range errs {
		if err == nil {
			return nil
		}
		c := call.deps.classify(err).Code
		if i > 0 && c != code {
			return nil
		}
		code = c
	}
	return errs[0]
}

// mcpSecMayHaveRun reports whether a read that ran an audit may have reached
// it: it answered, or failed after it was sent. A 4xx answer comes from the
// API's filters or routing before the security service runs, so no audit ran.
func mcpSecMayHaveRun(err error) bool {
	var he *client.HTTPError
	if errors.As(err, &he) {
		return he.StatusCode >= http.StatusInternalServerError
	}
	return true
}

// mcpSecUnavailable is the headline of a check that could not run: err is the
// request's error, or nil when the backend answered with backendError (or
// with nothing usable) in place of a result.
func mcpSecUnavailable(call *mcpCall, what string, err error, backendError string) mcpSecHeadline {
	if err != nil {
		te := call.deps.classify(err)
		call.deps.logger.Warn("security_evidence: "+what+" unavailable", "error", err)
		return mcpSecHeadline{ErrorCode: te.Code, Reason: te.Message, Error: call.FenceN(te.Detail, 200)}
	}
	return mcpSecHeadline{
		ErrorCode: mcpErrBackend,
		Reason:    "The Kates API could not run the " + what + " and answered with an error in place of a result.",
		Error:     call.FenceN(backendError, 200),
	}
}

func mcpSecCheckRefs(call *mcpCall, checks []client.SecurityCheckResult) []mcpSecCheckRef {
	refs := make([]mcpSecCheckRef, 0, len(checks))
	for _, c := range checks {
		if c.Status == "PASS" {
			continue
		}
		refs = append(refs, mcpSecCheckRef{Name: mcpSecLine(c.Name), Status: mcpSanitizeLine(c.Status, 16), Severity: mcpSanitizeLine(c.Severity, 16)})
	}
	mcpSecWorstFirst(refs, func(r mcpSecCheckRef) string { return r.Status })
	return mcpCap(call, refs, mcpSecHeadlineItems)
}

func mcpPostureHeadlineOf(call *mcpCall, r *client.SecurityAuditReport, err error) mcpPostureHeadline {
	if err != nil || r == nil || r.Error != "" || len(r.Checks) == 0 {
		return mcpPostureHeadline{mcpSecHeadline: mcpSecUnavailable(call, "security audit", err, mcpAuditError(r)), Problems: []mcpSecCheckRef{}}
	}
	h := mcpPostureHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, Grade: mcpSanitizeLine(r.Grade, 4), Problems: mcpSecCheckRefs(call, r.Checks)}
	if s := r.Summary; s != nil {
		h.Total, h.Passed, h.Warnings, h.Failures = s.Total, s.Passed, s.Warnings, s.Failures
	}
	return h
}

func mcpPentestHeadlineOf(call *mcpCall, r *client.SecurityPentestReport, err error) mcpPentestHeadline {
	if err != nil || r == nil || r.Error != "" {
		return mcpPentestHeadline{mcpSecHeadline: mcpSecUnavailable(call, "pentest", err, mcpSecErrorOf(r, func(r *client.SecurityPentestReport) string { return r.Error })), Findings: []string{}}
	}
	h := mcpPentestHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, Findings: []string{}}
	if s := r.Summary; s != nil {
		h.Total, h.Protected, h.Vulnerable = s.Total, s.Protected, s.Vulnerable
	}
	for _, t := range r.Tests {
		if t.Result == "VULNERABLE" {
			h.Findings = append(h.Findings, mcpSanitizeLine(t.ID, 64))
		}
	}
	h.Findings = mcpCap(call, h.Findings, mcpSecHeadlineItems)
	return h
}

func mcpComplianceHeadlineOf(call *mcpCall, r *client.SecurityComplianceReport, err error) mcpComplianceHeadline {
	if err != nil || r == nil || r.Error != "" {
		return mcpComplianceHeadline{
			mcpSecHeadline: mcpSecUnavailable(call, "compliance mapping", err, mcpSecErrorOf(r, func(r *client.SecurityComplianceReport) string { return r.Error })),
			Frameworks:     []mcpComplianceFramework{},
		}
	}
	frameworks, _, anyChecks := mcpComplianceFrameworksOf(r)
	if !anyChecks {
		return mcpComplianceHeadline{
			mcpSecHeadline: mcpSecHeadline{ErrorCode: mcpErrBackend, Reason: "The audit the compliance mapping is built from produced no checks."},
			Frameworks:     []mcpComplianceFramework{},
		}
	}
	return mcpComplianceHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, Frameworks: frameworks}
}

func mcpTLSHeadlineOf(call *mcpCall, r *client.SecurityTLSReport, err error) mcpChecksHeadline {
	if err != nil || r == nil || r.Error != "" || len(r.Checks) == 0 {
		return mcpChecksHeadline{mcpSecHeadline: mcpSecUnavailable(call, "TLS inspection", err, mcpSecErrorOf(r, func(r *client.SecurityTLSReport) string { return r.Error })), Problems: []mcpSecCheckRef{}}
	}
	return mcpChecksHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, Total: len(r.Checks), Problems: mcpSecCheckRefs(call, r.Checks)}
}

func mcpCertsHeadlineOf(call *mcpCall, r *client.SecurityCertsReport, err error) mcpCertsHeadline {
	if err != nil || r == nil || r.Error != "" || len(r.Certificates) == 0 {
		return mcpCertsHeadline{
			mcpSecHeadline:   mcpSecUnavailable(call, "certificate check", err, mcpSecErrorOf(r, func(r *client.SecurityCertsReport) string { return r.Error })),
			BrokersInspected: []int{},
			Problems:         []mcpSecCheckRef{},
		}
	}
	h := mcpCertsHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, TotalBrokers: r.TotalBrokers, BrokersInspected: []int{}}
	var checks []client.SecurityCheckResult
	for _, b := range r.Certificates {
		h.BrokersInspected = append(h.BrokersInspected, b.Broker)
		checks = append(checks, b.Checks...)
	}
	h.BrokersInspected = mcpCap(call, h.BrokersInspected, mcpSecCertBrokers)
	h.Problems = mcpSecCheckRefs(call, checks)
	return h
}

func mcpCVEHeadlineOf(call *mcpCall, r *client.SecurityCVEReport, err error) mcpCVEHeadline {
	if err != nil || r == nil || r.Error != "" {
		return mcpCVEHeadline{mcpSecHeadline: mcpSecUnavailable(call, "CVE check", err, mcpSecErrorOf(r, func(r *client.SecurityCVEReport) string { return r.Error })), Findings: []string{}}
	}
	h := mcpCVEHeadline{
		mcpSecHeadline: mcpSecHeadline{Available: true},
		KafkaVersion:   mcpSanitizeLine(r.KafkaVersion, 32),
		Grade:          mcpSanitizeLine(r.Grade, 8),
		Findings:       []string{},
	}
	if s := r.Summary; s != nil {
		h.Total, h.Vulnerable, h.Patched = s.Total, s.Vulnerable, s.Patched
	}
	if !mcpCVEVersionKnown(r.KafkaVersion) {
		// The backend's PASS and PATCHED rest on a comparison that never ran.
		return mcpCVEHeadline{
			mcpSecHeadline: mcpSecHeadline{Reason: "The backend had no Kafka version to compare its list of CVEs with, " +
				"so it compared none; it reports them PATCHED and grade PASS all the same. Section cve lists them as NOT_COMPARED."},
			KafkaVersion: h.KafkaVersion,
			Total:        h.Total,
			Findings:     []string{},
		}
	}
	for _, c := range r.Vulnerabilities {
		h.Findings = append(h.Findings, mcpSanitizeLine(c.ID, 32))
	}
	h.Findings = mcpCap(call, h.Findings, mcpSecHeadlineItems)
	return h
}

func mcpDriftHeadlineOf(call *mcpCall, r *client.SecurityDriftReport, err error) mcpDriftHeadline {
	if err != nil || r == nil {
		return mcpDriftHeadline{mcpSecHeadline: mcpSecUnavailable(call, "drift check", err, ""), DegradedChecks: []string{}}
	}
	if !r.HasBaseline {
		// No counts: zeros would read as a comparison that found no change.
		return mcpDriftHeadline{mcpSecHeadline: mcpSecHeadline{Available: true}, DegradedChecks: []string{}}
	}
	if r.Error != "" {
		return mcpDriftHeadline{mcpSecHeadline: mcpSecUnavailable(call, "drift check", nil, r.Error), DegradedChecks: []string{}}
	}
	if len(r.Drifts) == 0 {
		return mcpDriftHeadline{
			mcpSecHeadline: mcpSecHeadline{ErrorCode: mcpErrBackend, Reason: "The audit the drift check compares with the baseline produced no checks."},
			HasBaseline:    true,
			DegradedChecks: []string{},
		}
	}
	h := mcpDriftHeadline{
		mcpSecHeadline:    mcpSecHeadline{Available: true},
		HasBaseline:       true,
		BaselineTimestamp: mcpSecLine(r.BaselineTimestamp),
		BaselineGrade:     mcpSanitizeLine(r.BaselineGrade, 4),
		CurrentGrade:      mcpSanitizeLine(r.CurrentGrade, 4),
		DegradedChecks:    []string{},
	}
	if s := r.Summary; s != nil {
		h.Improved, h.Degraded, h.Unchanged = &s.Improved, &s.Degraded, &s.Unchanged
	}
	notInBaseline := 0
	var notPassing []string
	for _, d := range r.Drifts {
		if d.Change == "DEGRADED" {
			h.DegradedChecks = append(h.DegradedChecks, mcpSecLine(d.Check))
		}
		if d.Baseline == "UNKNOWN" {
			notInBaseline++
			if d.Current != "PASS" {
				notPassing = append(notPassing, mcpSecLine(d.Check))
			}
		}
	}
	h.NotInBaseline = &notInBaseline
	if len(notPassing) > 0 {
		h.NotInBaselineNotPassing = mcpCap(call, notPassing, mcpSecHeadlineItems)
	}
	h.DegradedChecks = mcpCap(call, h.DegradedChecks, mcpSecHeadlineItems)
	return h
}

func mcpConfigDiffHeadlineOf(call *mcpCall, r *client.SecurityConfigDiffReport, err error) mcpConfigDiffHeadline {
	if err != nil || r == nil || r.Error != "" {
		return mcpConfigDiffHeadline{
			mcpSecHeadline: mcpSecUnavailable(call, "config consistency check", err, mcpSecErrorOf(r, func(r *client.SecurityConfigDiffReport) string { return r.Error })),
			MismatchedKeys: []string{},
		}
	}
	h := mcpConfigDiffHeadline{
		mcpSecHeadline: mcpSecHeadline{Available: true},
		BrokerCount:    r.BrokerCount,
		KeysChecked:    r.KeysChecked,
		MismatchCount:  r.MismatchCount,
		MismatchedKeys: []string{},
	}
	for _, k := range r.Mismatches {
		h.MismatchedKeys = append(h.MismatchedKeys, mcpSecLine(k.Key))
	}
	h.MismatchedKeys = mcpCap(call, h.MismatchedKeys, mcpSecHeadlineItems)
	return h
}
