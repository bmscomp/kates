package client

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// Bodies shaped as the backend builds them, key for key, from
// SecurityService and SecurityPentestService (the maps are LinkedHashMaps,
// serialised by Jackson). They pin the DTOs in mcp_security.go to the keys
// the Java code writes.
const (
	auditBody = `{"checks":[{"name":"SASL Authentication","category":"auth","status":"FAIL",` +
		`"detail":"No SASL mechanisms configured — any client can connect","severity":"HIGH","compliance":"CIS-4.1",` +
		`"fix":"Set listener.security.protocol.map and sasl.enabled.mechanisms in broker config"}],` +
		`"summary":{"total":1,"passed":0,"warnings":0,"failures":1},"grade":"D","timestamp":"2026-09-25T12:00:00Z"}`
	auditFailedBody = `{"error":"Security audit failed: Timed out waiting for a node assignment","grade":"F"}`
	pentestBody     = `{"tests":[{"name":"Topic Auto-Creation","id":"auto-create","result":"VULNERABLE",` +
		`"detail":"auto.create.topics.enable=true — any producer can create topics","severity":"HIGH"}],` +
		`"summary":{"total":1,"protected":0,"vulnerable":1},"timestamp":"2026-09-25T12:00:00Z"}`
	complianceBody = `{"CIS Kafka Benchmark":{"controls":[{"check":"SASL Authentication","status":"FAIL","detail":"d",` +
		`"fix":"f","controlId":"CIS-4.1"}],"total":1,"passed":0,"compliance":"0%"},` +
		`"SOC2 Type II":{"controls":[{"check":"SASL Authentication","status":"FAIL","detail":"d","fix":"f","controlId":"CC6.1"}],` +
		`"total":1,"passed":0,"compliance":"0%"},` +
		`"PCI-DSS v4.0":{"controls":[],"total":0,"passed":0,"compliance":"N/A"},"grade":"D","timestamp":"2026-09-25T12:00:00Z"}`
	tlsBody = `{"checks":[{"name":"TLS Protocol","category":"tls","status":"PASS","detail":"ssl.protocol = TLSv1.3",` +
		`"severity":"HIGH","compliance":"CIS-4.4","fix":"Set ssl.protocol: TLSv1.3 in kafka.config"}],"timestamp":"t"}`
	certsBody = `{"certificates":[{"broker":0,"keystoreConfigured":false,"truststoreConfigured":true,` +
		`"sslProtocol":"TLSv1.3","clientAuth":"none","endpointIdentification":"https","cipherSuites":"JVM defaults",` +
		`"enabledProtocols":"TLSv1.2,TLSv1.3","checks":[{"name":"Keystore Configured","category":"transport",` +
		`"status":"FAIL","detail":"No SSL keystore — TLS unavailable","severity":"CRITICAL","compliance":"CIS-4.11",` +
		`"fix":"Configure ssl.keystore.location with broker certificate"}]}],"totalBrokers":3,"timestamp":"t"}`
	cveBody = `{"kafkaVersion":"unknown","vulnerabilities":[],"patched":[{"id":"CVE-2024-31141",` +
		`"title":"Apache Kafka Client JNDI Injection","severity":"CRITICAL","affectedFrom":"0.0.0","affectedUpTo":"3.7.0",` +
		`"description":"Clients can be tricked into JNDI lookups via SASL/OAUTHBEARER","status":"PATCHED"}],` +
		`"summary":{"total":1,"vulnerable":0,"patched":1},"grade":"PASS","timestamp":"t"}`
	driftBody = `{"hasBaseline":true,"baselineTimestamp":"2026-09-01T00:00:00Z","baselineGrade":"B","currentGrade":"D",` +
		`"drifts":[{"check":"SASL Authentication","baseline":"PASS","current":"FAIL","change":"DEGRADED","detail":"d","fix":"f"},` +
		`{"check":"ACL Rules Defined","baseline":"PASS","current":"PASS","change":"UNCHANGED"}],` +
		`"summary":{"improved":0,"degraded":1,"unchanged":1,"total":2},"timestamp":"t"}`
	noBaselineBody = `{"error":"No baseline saved. Run 'kates security baseline --save' first.","hasBaseline":false}`
	configDiffBody = `{"brokerCount":2,"keysChecked":2,` +
		`"mismatches":[{"key":"ssl.client.auth","values":{"0":"none","1":"required"},"consistent":false}],` +
		`"consistent":[{"key":"ssl.protocol","values":{"0":"TLSv1.3","1":"TLSv1.3"},"consistent":true,"value":"TLSv1.3"}],` +
		`"mismatchCount":1,"grade":"WARN","timestamp":"t"}`
)

func TestSecurityReportsDecode(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		path      string
		wantQuery string
		body      string
		call      func(c *Client) (any, error)
		want      any
	}{
		{"audit", "/api/security/audit", "", auditBody,
			func(c *Client) (any, error) { return c.SecurityAuditReport(ctx) },
			&SecurityAuditReport{
				Checks: []SecurityCheckResult{{
					Name: "SASL Authentication", Category: "auth", Status: "FAIL",
					Detail: "No SASL mechanisms configured — any client can connect", Severity: "HIGH", Compliance: "CIS-4.1",
					Fix: "Set listener.security.protocol.map and sasl.enabled.mechanisms in broker config",
				}},
				Summary: &SecurityAuditSummary{Total: 1, Failures: 1},
				Grade:   "D", Timestamp: "2026-09-25T12:00:00Z",
			}},
		{"audit that failed", "/api/security/audit", "", auditFailedBody,
			func(c *Client) (any, error) { return c.SecurityAuditReport(ctx) },
			&SecurityAuditReport{Grade: "F", Error: "Security audit failed: Timed out waiting for a node assignment"}},
		{"pentest", "/api/security/pentest", "test=all", pentestBody,
			func(c *Client) (any, error) { return c.SecurityPentestReport(ctx) },
			&SecurityPentestReport{
				Tests: []SecurityPentestResult{{
					Name: "Topic Auto-Creation", ID: "auto-create", Result: "VULNERABLE",
					Detail: "auto.create.topics.enable=true — any producer can create topics", Severity: "HIGH",
				}},
				Summary:   &SecurityPentestSummary{Total: 1, Vulnerable: 1},
				Timestamp: "2026-09-25T12:00:00Z",
			}},
		{"compliance", "/api/security/compliance", "", complianceBody,
			func(c *Client) (any, error) { return c.SecurityComplianceReport(ctx) },
			&SecurityComplianceReport{
				CIS: SecurityComplianceFramework{
					Controls: []SecurityComplianceControl{{Check: "SASL Authentication", Status: "FAIL", Detail: "d", Fix: "f", ControlID: "CIS-4.1"}},
					Total:    1, Compliance: "0%",
				},
				SOC2: SecurityComplianceFramework{
					Controls: []SecurityComplianceControl{{Check: "SASL Authentication", Status: "FAIL", Detail: "d", Fix: "f", ControlID: "CC6.1"}},
					Total:    1, Compliance: "0%",
				},
				PCI:   SecurityComplianceFramework{Controls: []SecurityComplianceControl{}, Compliance: "N/A"},
				Grade: "D", Timestamp: "2026-09-25T12:00:00Z",
			}},
		{"tls", "/api/security/tls", "", tlsBody,
			func(c *Client) (any, error) { return c.SecurityTLSReport(ctx) },
			&SecurityTLSReport{
				Checks: []SecurityCheckResult{{
					Name: "TLS Protocol", Category: "tls", Status: "PASS", Detail: "ssl.protocol = TLSv1.3",
					Severity: "HIGH", Compliance: "CIS-4.4", Fix: "Set ssl.protocol: TLSv1.3 in kafka.config",
				}},
				Timestamp: "t",
			}},
		{"certs", "/api/security/certs", "", certsBody,
			func(c *Client) (any, error) { return c.SecurityCertsReport(ctx) },
			&SecurityCertsReport{
				Certificates: []SecurityBrokerCerts{{
					Broker: 0, TruststoreConfigured: true, SSLProtocol: "TLSv1.3", ClientAuth: "none",
					EndpointIdentification: "https", CipherSuites: "JVM defaults", EnabledProtocols: "TLSv1.2,TLSv1.3",
					Checks: []SecurityCheckResult{{
						Name: "Keystore Configured", Category: "transport", Status: "FAIL",
						Detail: "No SSL keystore — TLS unavailable", Severity: "CRITICAL", Compliance: "CIS-4.11",
						Fix: "Configure ssl.keystore.location with broker certificate",
					}},
				}},
				TotalBrokers: 3, Timestamp: "t",
			}},
		{"cve", "/api/security/cve", "", cveBody,
			func(c *Client) (any, error) { return c.SecurityCVEReport(ctx) },
			&SecurityCVEReport{
				KafkaVersion:    "unknown",
				Vulnerabilities: []SecurityCVE{},
				Patched: []SecurityCVE{{
					ID: "CVE-2024-31141", Title: "Apache Kafka Client JNDI Injection", Severity: "CRITICAL",
					AffectedFrom: "0.0.0", AffectedUpTo: "3.7.0",
					Description: "Clients can be tricked into JNDI lookups via SASL/OAUTHBEARER", Status: "PATCHED",
				}},
				Summary: &SecurityCVESummary{Total: 1, Patched: 1},
				Grade:   "PASS", Timestamp: "t",
			}},
		{"drift", "/api/security/drift", "", driftBody,
			func(c *Client) (any, error) { return c.SecurityDriftReport(ctx) },
			&SecurityDriftReport{
				HasBaseline: true, BaselineTimestamp: "2026-09-01T00:00:00Z", BaselineGrade: "B", CurrentGrade: "D",
				Drifts: []SecurityDriftEntry{
					{Check: "SASL Authentication", Baseline: "PASS", Current: "FAIL", Change: "DEGRADED", Detail: "d", Fix: "f"},
					{Check: "ACL Rules Defined", Baseline: "PASS", Current: "PASS", Change: "UNCHANGED"},
				},
				Summary:   &SecurityDriftSummary{Degraded: 1, Unchanged: 1, Total: 2},
				Timestamp: "t",
			}},
		{"drift without a baseline", "/api/security/drift", "", noBaselineBody,
			func(c *Client) (any, error) { return c.SecurityDriftReport(ctx) },
			&SecurityDriftReport{Error: "No baseline saved. Run 'kates security baseline --save' first."}},
		{"config diff", "/api/security/config-diff", "", configDiffBody,
			func(c *Client) (any, error) { return c.SecurityConfigDiffReport(ctx) },
			&SecurityConfigDiffReport{
				BrokerCount: 2, KeysChecked: 2,
				Mismatches: []SecurityConfigKey{{Key: "ssl.client.auth", Values: map[string]string{"0": "none", "1": "required"}}},
				Consistent: []SecurityConfigKey{{
					Key: "ssl.protocol", Values: map[string]string{"0": "TLSv1.3", "1": "TLSv1.3"}, Consistent: true, Value: "TLSv1.3",
				}},
				MismatchCount: 1, Grade: "WARN", Timestamp: "t",
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotQuery string
			c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			})
			got, err := tt.call(c)
			if err != nil {
				t.Fatal(err)
			}
			if gotMethod != http.MethodGet || gotPath != tt.path || gotQuery != tt.wantQuery {
				t.Errorf("sent %s %s?%s, want GET %s?%s", gotMethod, gotPath, gotQuery, tt.path, tt.wantQuery)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decoded\n %+v\nwant\n %+v", got, tt.want)
			}
		})
	}
}

// TestSecurityReportsKeepHTTPErrors: a failed request is an *HTTPError, which
// kates mcp maps to its fixed codes, not an empty report.
func TestSecurityReportsKeepHTTPErrors(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":403,"error":"Forbidden","message":"Invalid or missing API key"}`))
	})
	_, err := c.SecurityCVEReport(context.Background())
	he, ok := err.(*HTTPError)
	if !ok || he.StatusCode != http.StatusForbidden {
		t.Fatalf("err = %v (%T), want an *HTTPError 403", err, err)
	}
}
