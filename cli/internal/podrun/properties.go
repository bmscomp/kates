package podrun

import (
	"strings"
)

// DefaultPropertiesPath is where a client pod keeps its client.properties by
// convention (LOG_DIR is /tmp too; both are writable for any uid).
const DefaultPropertiesPath = "/tmp/client.properties"

// The credential contract.
//
// A password reaches a client pod in exactly one of two ways, and the command
// line is never one of them:
//
//  1. Pod.SecretEnv mounts it as an environment variable through
//     valueFrom.secretKeyRef, so the pod spec names a Secret and nothing else.
//  2. The CLI reads it with `kubectl get secret … -o jsonpath` into a Go
//     string it never prints, renders a properties file with one of the
//     builders below, and places it with WriteFile — which pipes the content
//     through `tee` on stdin.
//
// Either way `kubectl get pod -o yaml`, the API audit log and the CLI's own
// output show at most a Secret name. Callers must keep the string out of
// their logs and error messages; nothing in this package includes it in one.

// SASL is a SASL credential for a properties file.
type SASL struct {
	// Mechanism is PLAIN, SCRAM-SHA-256 or SCRAM-SHA-512 (case-insensitive;
	// the chart's authentication.type spellings are accepted).
	Mechanism string
	Username  string
	Password  string
}

// PlaintextProperties returns a client.properties for an unauthenticated
// PLAINTEXT listener.
func PlaintextProperties() string {
	return "security.protocol=PLAINTEXT\n"
}

// SASLProperties returns a client.properties for SASL over a plaintext
// listener: security.protocol, sasl.mechanism and the sasl.jaas.config line.
// PLAIN uses PlainLoginModule; every other mechanism (the SCRAM family) uses
// ScramLoginModule, exactly as the chart's test helper decides it.
//
// The username and password are escaped for the two parsers that read the
// line in turn, see JaasQuote.
func SASLProperties(mechanism, username, password string) string {
	var b strings.Builder
	b.WriteString("security.protocol=SASL_PLAINTEXT\n")
	writeSASL(&b, SASL{Mechanism: mechanism, Username: username, Password: password})
	return b.String()
}

// TLSProperties returns a client.properties for a TLS listener whose
// certificate is signed by the CA at caPath (a PEM file inside the pod, such
// as a mounted <cluster>-cluster-ca-cert). With sasl nil the protocol is SSL;
// with a credential it is SASL_SSL and the SASL lines are appended. PEM
// truststores need a Kafka 2.7+ client (KIP-651), which every image this
// package targets on the TLS path is.
func TLSProperties(caPath string, sasl *SASL) string {
	var b strings.Builder
	if sasl == nil {
		b.WriteString("security.protocol=SSL\n")
	} else {
		b.WriteString("security.protocol=SASL_SSL\n")
	}
	b.WriteString("ssl.truststore.type=PEM\n")
	b.WriteString("ssl.truststore.location=" + caPath + "\n")
	if sasl != nil {
		writeSASL(&b, *sasl)
	}
	return b.String()
}

func writeSASL(b *strings.Builder, s SASL) {
	mech := strings.ToUpper(strings.TrimSpace(s.Mechanism))
	module := "org.apache.kafka.common.security.scram.ScramLoginModule"
	if mech == "PLAIN" {
		module = "org.apache.kafka.common.security.plain.PlainLoginModule"
	}
	b.WriteString("sasl.mechanism=" + mech + "\n")
	b.WriteString("sasl.jaas.config=" + module + ` required username="` + JaasQuote(s.Username) +
		`" password="` + JaasQuote(s.Password) + `";` + "\n")
}

// JaasQuote escapes s for use inside a double-quoted option of a
// sasl.jaas.config line that lives in a Java properties file.
//
// Two parsers read that line, one after the other, and each strips one level
// of backslashes:
//
//   - java.util.Properties.load, which every Kafka tool uses for
//     --command-config / --consumer.config / --producer.config, turns `\\`
//     into `\` and drops the backslash of any other escape (`\"` becomes
//     `"`);
//   - the JAAS parser (a StreamTokenizer over the loaded value) then reads the
//     quoted string, where `\"` is a quote and `\\` a backslash.
//
// So a literal `"` must be written `\\"` and a literal `\` must be written
// `\\\\`. The shell scripts this package replaces — and the chart's own
// test helpers — escape only once (`\"`, `\\`), which the properties loader
// undoes before the JAAS parser ever sees it: a password containing either
// character fails authentication on that path. Strimzi-generated passwords
// are alphanumeric, which is why nobody noticed.
func JaasQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\\\`)
		case '"':
			b.WriteString(`\\"`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
