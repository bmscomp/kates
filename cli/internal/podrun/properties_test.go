package podrun

import (
	"strings"
	"testing"
)

func TestPlaintextProperties(t *testing.T) {
	if got := PlaintextProperties(); got != "security.protocol=PLAINTEXT\n" {
		t.Errorf("PlaintextProperties = %q", got)
	}
}

func TestSASLProperties(t *testing.T) {
	tests := []struct {
		name               string
		mechanism, user    string
		password           string
		wantMech, wantJaas string
	}{
		{
			name: "scram", mechanism: "SCRAM-SHA-512", user: "kates-mm2", password: "p4ss",
			wantMech: "sasl.mechanism=SCRAM-SHA-512",
			wantJaas: `sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-mm2" password="p4ss";`,
		},
		{
			name: "chart spelling of scram-sha-256", mechanism: "scram-sha-256", user: "u", password: "p",
			wantMech: "sasl.mechanism=SCRAM-SHA-256",
			wantJaas: `sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="u" password="p";`,
		},
		{
			name: "plain", mechanism: "plain", user: "mm2reader", password: "mm2reader-secret",
			wantMech: "sasl.mechanism=PLAIN",
			wantJaas: `sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="mm2reader" password="mm2reader-secret";`,
		},
		{
			name: "quote and backslash are escaped for both parsers", mechanism: "PLAIN", user: `us"er`, password: `pa\ss"word`,
			wantMech: "sasl.mechanism=PLAIN",
			wantJaas: `sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="us\\"er" password="pa\\\\ss\\"word";`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SASLProperties(tt.mechanism, tt.user, tt.password)
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("want 3 lines, got %q", got)
			}
			if lines[0] != "security.protocol=SASL_PLAINTEXT" {
				t.Errorf("line 1 = %q", lines[0])
			}
			if lines[1] != tt.wantMech {
				t.Errorf("line 2 = %q, want %q", lines[1], tt.wantMech)
			}
			if lines[2] != tt.wantJaas {
				t.Errorf("line 3 = %q, want %q", lines[2], tt.wantJaas)
			}
		})
	}
}

// TestJaasQuoteRoundTrip runs the two parsers the properties file meets —
// java.util.Properties.load, then the JAAS quoted-string tokenizer — over
// the escaped form and checks the original comes back.
func TestJaasQuoteRoundTrip(t *testing.T) {
	for _, in := range []string{"plain", `a"b`, `a\b`, `\`, `"`, `\"`, `a\\b"c`, "unicode-é-ok"} {
		escaped := JaasQuote(in)
		got, ok := jaasUnquote(propertiesUnescape(escaped))
		if !ok || got != in {
			t.Errorf("JaasQuote(%q) = %q, round-trips to %q (ok=%v)", in, escaped, got, ok)
		}
	}
	// The scripts' single escaping does NOT survive the properties loader:
	// the quote comes out bare and ends the JAAS string early.
	single := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(`a"b`)
	if _, ok := jaasUnquote(propertiesUnescape(single)); ok {
		t.Errorf("single escaping %q unexpectedly survives both parsers; the double escaping would be redundant", single)
	}
}

// propertiesUnescape models java.util.Properties.load on a value: a backslash
// escapes the next character; an unknown escape drops the backslash.
func propertiesUnescape(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
			switch rs[i] {
			case 't':
				b.WriteRune('\t')
			case 'n':
				b.WriteRune('\n')
			case 'r':
				b.WriteRune('\r')
			case 'f':
				b.WriteRune('\f')
			default:
				b.WriteRune(rs[i])
			}
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

// jaasUnquote models StreamTokenizer's quoted-string escapes as Kafka's
// JaasConfig sees an option value between double quotes. A bare quote ends
// the string, so its presence means the value was not usable (ok=false).
func jaasUnquote(s string) (string, bool) {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
			b.WriteRune(rs[i])
			continue
		}
		if rs[i] == '"' {
			return b.String(), false
		}
		b.WriteRune(rs[i])
	}
	return b.String(), true
}

func TestTLSProperties(t *testing.T) {
	got := TLSProperties("/etc/kates/ca.crt", nil)
	want := "security.protocol=SSL\nssl.truststore.type=PEM\nssl.truststore.location=/etc/kates/ca.crt\n"
	if got != want {
		t.Errorf("TLSProperties(nil) = %q, want %q", got, want)
	}

	got = TLSProperties("/etc/kates/ca.crt", &SASL{Mechanism: "scram-sha-512", Username: "kates-mm2", Password: "pw"})
	want = "security.protocol=SASL_SSL\nssl.truststore.type=PEM\nssl.truststore.location=/etc/kates/ca.crt\n" +
		"sasl.mechanism=SCRAM-SHA-512\n" +
		`sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-mm2" password="pw";` + "\n"
	if got != want {
		t.Errorf("TLSProperties(sasl) = %q, want %q", got, want)
	}
}
