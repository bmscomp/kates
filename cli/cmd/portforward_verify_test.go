package cmd

import (
	"net"
	"testing"
)

// A forward whose local port answers is active, and the Kates API's is the
// one the "ports" context will point at; a port already in use fails the
// run, and an API port in use is reported as such.
func TestVerifyPortForwards(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	t.Run("active API forward", func(t *testing.T) {
		v := verifyPortForwards([]portForwardSpec{{Label: katesAPILabel, Namespace: "kates", Local: port}}, nil)
		if !v.allOk || !v.apiActive || v.apiNS != "kates" || v.apiLocalPort != port || v.apiPortTaken != 0 {
			t.Fatalf("got %+v, want the API forward active on %d", v, port)
		}
	})

	t.Run("API port in use", func(t *testing.T) {
		v := verifyPortForwards([]portForwardSpec{
			{Label: "Kafka UI", Namespace: "kafka", Local: port},
			{Label: katesAPILabel, Namespace: "kates", Local: 8080},
		}, map[int]bool{8080: true})
		if v.allOk || v.apiActive || v.apiPortTaken != 8080 {
			t.Fatalf("got %+v, want a failed run with the API port 8080 taken", v)
		}
	})
}
