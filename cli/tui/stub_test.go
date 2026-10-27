package tui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// kafkaStubRoutes are the Kafka API answers the dashboard tabs read.
var kafkaStubRoutes = map[string]string{
	"/api/kafka/topics": `[
		{"name":"orders","partitions":3,"replicationFactor":3,"underReplicated":0},
		{"name":"payments","partitions":6,"replicationFactor":3,"underReplicated":1},
		{"name":"__consumer_offsets","internal":true,"partitions":50,"replicationFactor":3,"underReplicated":0}]`,
	"/api/kafka/topics/orders": `{"name":"orders","partitions":3,"replicationFactor":3,
		"configs":{"retention.ms":"3600000"},
		"partitionInfo":[{"partition":0,"leader":1,"replicas":[1,2,3],"isr":[1,2,3],"underReplicated":false},
		                 {"partition":1,"leader":2,"replicas":[1,2,3],"isr":[2,3],"underReplicated":true}]}`,
	"/api/kafka/consume/orders": `[{"offset":7,"partition":0,"key":"k1","value":"v1"},{"offset":8,"partition":1,"key":null,"value":"v2"}]`,
	"/api/kafka/groups": `[{"groupId":"billing","state":"Stable","members":2},
		{"groupId":"audit","state":"Empty","members":0},
		{"groupId":"legacy","state":"Dead","members":0},
		{"groupId":"rebalancing","state":"PreparingRebalance","members":1}]`,
	"/api/kafka/groups/billing": `{"groupId":"billing","state":"Stable","members":2,"totalLag":5,
		"offsets":[{"topic":"orders","partition":0,"currentOffset":10,"endOffset":15,"lag":5},
		           {"topic":"orders","partition":1,"currentOffset":3,"endOffset":3,"lag":0}]}`,
	"/api/kafka/produce/orders": `{"topic":"orders","partition":2,"offset":42}`,
	"/api/kafka/brokers":        `{"clusterId":"cluster-0123456789abcdef","brokerCount":2,"brokers":[{"id":0,"host":"kafka-0","port":9092},{"id":1,"host":"kafka-1","port":9092,"rack":"az-b"}]}`,
	"/api/cluster/check":        `{"status":"HEALTHY","brokers":2,"controllerId":0}`,
}

// newKafkaStub is a client of a stub API that answers kafkaStubRoutes and
// 404s anything else.
func newKafkaStub(t *testing.T) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := kafkaStubRoutes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := client.New(srv.URL)
	c.MaxRetries = 1 // one attempt: a 404 fails at once
	return c
}

// runes is a key press of typed text.
func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// plain is s without escape sequences.
func plain(s string) string {
	return ansiSeq.ReplaceAllString(s, "")
}

// nonASCII returns the first rune of s outside printable ASCII and
// whitespace, or "" when there is none.
func nonASCII(s string) string {
	for _, r := range s {
		if r > '~' || (r < ' ' && r != '\n' && r != '\t' && r != '\r') {
			return string(r)
		}
	}
	return ""
}

// asciiMode switches the CLI to --plain for the rest of the test.
func asciiMode(t *testing.T) {
	t.Helper()
	profile := lipgloss.ColorProfile()
	output.SetPlain(true)
	t.Cleanup(func() {
		output.SetPlain(false)
		lipgloss.SetColorProfile(profile)
	})
}

// mustContain fails t for each of want missing from s.
func mustContain(t *testing.T, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("want %q in:\n%s", w, s)
		}
	}
}
