package client

import (
	"context"
	"net/url"
)

// Typed views of the security endpoints that kates mcp reads. The Security*
// methods in client.go return map[string]interface{}, which leaves a tool
// nothing to promise in its output schema; these decode the same responses
// into structs whose fields are the keys SecurityService and
// SecurityPentestService put in their maps
// (kates/src/main/java/com/bmscomp/kates/service/).
//
// Every one of these services catches its own exceptions and answers 200 with
// a body that carries only "error" (the audit adds grade "F"), so a caller
// must look at Error before reading anything else.

// SecurityCheckResult is one check as SecurityService.check builds it: the
// audit, TLS and certificate reports all use this shape.
type SecurityCheckResult struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Status   string `json:"status"` // PASS, WARN or FAIL
	Detail   string `json:"detail"`
	Severity string `json:"severity"`
	// Compliance is the CIS Kafka Benchmark id the check names.
	Compliance string `json:"compliance"`
	Fix        string `json:"fix"`
}

// SecurityAuditSummary counts the audit's checks by status.
type SecurityAuditSummary struct {
	Total    int `json:"total"`
	Passed   int `json:"passed"`
	Warnings int `json:"warnings"`
	Failures int `json:"failures"`
}

// SecurityAuditReport is GET /api/security/audit (SecurityService.securityAudit).
type SecurityAuditReport struct {
	Checks    []SecurityCheckResult `json:"checks"`
	Summary   *SecurityAuditSummary `json:"summary"`
	Grade     string                `json:"grade"`
	Timestamp string                `json:"timestamp"`
	Error     string                `json:"error"`
}

// SecurityAuditReport runs the security audit. Every call appends a snapshot
// to the backend's in-memory grade history.
func (c *Client) SecurityAuditReport(ctx context.Context) (*SecurityAuditReport, error) {
	return get[*SecurityAuditReport](c, ctx, "/api/security/audit")
}

// SecurityPentestResult is one of the pentest's checks.
type SecurityPentestResult struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Result   string `json:"result"` // VULNERABLE or PROTECTED
	Detail   string `json:"detail"`
	Severity string `json:"severity"`
}

// SecurityPentestSummary counts the pentest's results.
type SecurityPentestSummary struct {
	Total      int `json:"total"`
	Protected  int `json:"protected"`
	Vulnerable int `json:"vulnerable"`
}

// SecurityPentestReport is GET /api/security/pentest
// (SecurityPentestService.pentest).
type SecurityPentestReport struct {
	Tests     []SecurityPentestResult `json:"tests"`
	Summary   *SecurityPentestSummary `json:"summary"`
	Timestamp string                  `json:"timestamp"`
	Error     string                  `json:"error"`
}

// SecurityPentestReport runs every pentest check ("all"); the backend runs
// the same set for an empty name.
func (c *Client) SecurityPentestReport(ctx context.Context) (*SecurityPentestReport, error) {
	return get[*SecurityPentestReport](c, ctx, withQuery("/api/security/pentest", url.Values{"test": {"all"}}))
}

// SecurityComplianceControl is one audit check as a framework maps it.
type SecurityComplianceControl struct {
	Check     string `json:"check"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Fix       string `json:"fix"`
	ControlID string `json:"controlId"`
}

// SecurityComplianceFramework is one framework's share of the audit's checks.
type SecurityComplianceFramework struct {
	Controls []SecurityComplianceControl `json:"controls"`
	Total    int                         `json:"total"`
	Passed   int                         `json:"passed"`
	// Compliance is the share of controls that pass, as "83%", or "N/A"
	// when the framework maps no check.
	Compliance string `json:"compliance"`
}

// SecurityComplianceReport is GET /api/security/compliance
// (SecurityService.securityCompliance). The frameworks are top-level keys
// named as the backend names them.
type SecurityComplianceReport struct {
	CIS       SecurityComplianceFramework `json:"CIS Kafka Benchmark"`
	SOC2      SecurityComplianceFramework `json:"SOC2 Type II"`
	PCI       SecurityComplianceFramework `json:"PCI-DSS v4.0"`
	Grade     string                      `json:"grade"`
	Timestamp string                      `json:"timestamp"`
	Error     string                      `json:"error"`
}

// SecurityComplianceReport maps the audit's checks to frameworks. The backend
// runs a full audit for it, which appends to its grade history.
func (c *Client) SecurityComplianceReport(ctx context.Context) (*SecurityComplianceReport, error) {
	return get[*SecurityComplianceReport](c, ctx, "/api/security/compliance")
}

// SecurityTLSReport is GET /api/security/tls (SecurityService.tlsInspect).
type SecurityTLSReport struct {
	Checks    []SecurityCheckResult `json:"checks"`
	Timestamp string                `json:"timestamp"`
	Error     string                `json:"error"`
}

// SecurityTLSReport inspects the TLS settings of the first broker listed.
func (c *Client) SecurityTLSReport(ctx context.Context) (*SecurityTLSReport, error) {
	return get[*SecurityTLSReport](c, ctx, "/api/security/tls")
}

// SecurityBrokerCerts is the certificate configuration of one broker.
type SecurityBrokerCerts struct {
	Broker                 int                   `json:"broker"`
	KeystoreConfigured     bool                  `json:"keystoreConfigured"`
	TruststoreConfigured   bool                  `json:"truststoreConfigured"`
	SSLProtocol            string                `json:"sslProtocol"`
	ClientAuth             string                `json:"clientAuth"`
	EndpointIdentification string                `json:"endpointIdentification"`
	CipherSuites           string                `json:"cipherSuites"`
	EnabledProtocols       string                `json:"enabledProtocols"`
	Checks                 []SecurityCheckResult `json:"checks"`
}

// SecurityCertsReport is GET /api/security/certs
// (SecurityService.certificateCheck). Certificates lists the brokers
// inspected, which is the first broker only; TotalBrokers counts them all.
type SecurityCertsReport struct {
	Certificates []SecurityBrokerCerts `json:"certificates"`
	TotalBrokers int                   `json:"totalBrokers"`
	Timestamp    string                `json:"timestamp"`
	Error        string                `json:"error"`
}

// SecurityCertsReport reads the certificate configuration.
func (c *Client) SecurityCertsReport(ctx context.Context) (*SecurityCertsReport, error) {
	return get[*SecurityCertsReport](c, ctx, "/api/security/certs")
}

// SecurityCVE is one entry of the backend's fixed CVE list.
type SecurityCVE struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Severity     string `json:"severity"`
	AffectedFrom string `json:"affectedFrom"`
	AffectedUpTo string `json:"affectedUpTo"`
	Description  string `json:"description"`
	Status       string `json:"status"` // VULNERABLE or PATCHED
}

// SecurityCVESummary counts the CVE list by status.
type SecurityCVESummary struct {
	Total      int `json:"total"`
	Vulnerable int `json:"vulnerable"`
	Patched    int `json:"patched"`
}

// SecurityCVEReport is GET /api/security/cve (SecurityService.cveCheck).
type SecurityCVEReport struct {
	KafkaVersion    string              `json:"kafkaVersion"`
	Vulnerabilities []SecurityCVE       `json:"vulnerabilities"`
	Patched         []SecurityCVE       `json:"patched"`
	Summary         *SecurityCVESummary `json:"summary"`
	Grade           string              `json:"grade"` // PASS or FAIL
	Timestamp       string              `json:"timestamp"`
	Error           string              `json:"error"`
}

// SecurityCVEReport compares the cluster with the backend's CVE list.
func (c *Client) SecurityCVEReport(ctx context.Context) (*SecurityCVEReport, error) {
	return get[*SecurityCVEReport](c, ctx, "/api/security/cve")
}

// SecurityDriftEntry is one audit check compared with the saved baseline.
type SecurityDriftEntry struct {
	Check    string `json:"check"`
	Baseline string `json:"baseline"` // UNKNOWN when the baseline lacks the check
	Current  string `json:"current"`
	Change   string `json:"change"` // IMPROVED, DEGRADED or UNCHANGED
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

// SecurityDriftSummary counts the drift entries by change.
type SecurityDriftSummary struct {
	Improved  int `json:"improved"`
	Degraded  int `json:"degraded"`
	Unchanged int `json:"unchanged"`
	Total     int `json:"total"`
}

// SecurityDriftReport is GET /api/security/drift (SecurityService.securityDrift).
// Without a saved baseline it carries only HasBaseline false and Error.
type SecurityDriftReport struct {
	HasBaseline       bool                  `json:"hasBaseline"`
	BaselineTimestamp string                `json:"baselineTimestamp"`
	BaselineGrade     string                `json:"baselineGrade"`
	CurrentGrade      string                `json:"currentGrade"`
	Drifts            []SecurityDriftEntry  `json:"drifts"`
	Summary           *SecurityDriftSummary `json:"summary"`
	Timestamp         string                `json:"timestamp"`
	Error             string                `json:"error"`
}

// SecurityDriftReport compares a fresh audit with the saved baseline. When a
// baseline is saved the backend runs a full audit for it, which appends to
// its grade history.
func (c *Client) SecurityDriftReport(ctx context.Context) (*SecurityDriftReport, error) {
	return get[*SecurityDriftReport](c, ctx, "/api/security/drift")
}

// SecurityConfigKey is one security-relevant broker setting across brokers.
type SecurityConfigKey struct {
	Key string `json:"key"`
	// Values maps each broker id, as a string, to its value, or "(not set)".
	Values     map[string]string `json:"values"`
	Consistent bool              `json:"consistent"`
	// Value is the common value, present when Consistent.
	Value string `json:"value"`
}

// SecurityConfigDiffReport is GET /api/security/config-diff
// (SecurityService.configConsistency).
type SecurityConfigDiffReport struct {
	BrokerCount   int                 `json:"brokerCount"`
	KeysChecked   int                 `json:"keysChecked"`
	Mismatches    []SecurityConfigKey `json:"mismatches"`
	Consistent    []SecurityConfigKey `json:"consistent"`
	MismatchCount int                 `json:"mismatchCount"`
	Grade         string              `json:"grade"` // PASS or WARN
	Timestamp     string              `json:"timestamp"`
	Error         string              `json:"error"`
}

// SecurityConfigDiffReport compares security settings across every broker.
func (c *Client) SecurityConfigDiffReport(ctx context.Context) (*SecurityConfigDiffReport, error) {
	return get[*SecurityConfigDiffReport](c, ctx, "/api/security/config-diff")
}
