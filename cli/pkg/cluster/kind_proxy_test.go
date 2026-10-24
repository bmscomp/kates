package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestProxyFromEnv(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notPEM := filepath.Join(dir, "ca.txt")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, warns, err := ProxyFromEnv(envOf(nil))
	if err != nil || p.Set || p.Configures() || len(warns) != 0 {
		t.Fatalf("no proxy variables: %+v %v %v", p, warns, err)
	}

	p, _, err = ProxyFromEnv(envOf(map[string]string{
		"HTTPS_PROXY": "http://proxy.corp:3128", "https_proxy": "http://other:1",
		"http_proxy": "http://proxy.corp:3128", "no_proxy": ".corp,10.0.0.0/8", "PROXY_CA_FILE": ca,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := KindProxy{HTTP: "http://proxy.corp:3128", HTTPS: "http://proxy.corp:3128", NoProxy: ".corp,10.0.0.0/8", CAFile: ca, Set: true}
	if p != want {
		t.Fatalf("got %+v, want %+v: upper case first, lower case otherwise", p, want)
	}

	for _, loop := range []string{"http://localhost:3128", "http://127.0.0.1:8080", "http://[::1]:3128", "http://user:pw@localhost:3128"} {
		p, warns, err := ProxyFromEnv(envOf(map[string]string{"HTTPS_PROXY": loop}))
		if err != nil || p.HTTPS != "" || !p.Set || len(warns) != 1 || !strings.Contains(warns[0], "host.docker.internal") {
			t.Errorf("%s: %+v %v %v; a loopback proxy is dropped, said, and still set empty for kind", loop, p, warns, err)
		}
	}

	for _, bad := range []string{filepath.Join(dir, "missing.pem"), notPEM} {
		if _, _, err := ProxyFromEnv(envOf(map[string]string{"PROXY_CA_FILE": bad})); err == nil || !strings.Contains(err.Error(), "PROXY_CA_FILE") {
			t.Errorf("PROXY_CA_FILE=%s: want an error naming it, got %v", bad, err)
		}
	}
}

func TestCreateKind_WithAProxyConfiguresEveryNode(t *testing.T) {
	f := newFake().
		on("env", "", nil).
		on("kind get nodes --name panda", "panda-control-plane\npanda-worker\n", nil).
		on("kubectl get nodes", "alpha", nil)
	proxy := KindProxy{HTTPS: "http://proxy.corp:3128", NoProxy: ".corp", CAFile: "/etc/corp-ca.pem", Set: true}

	if err := CreateKind(f, KindOptions{ConfigPath: "c.yaml", Proxy: proxy}); err != nil {
		t.Fatalf("CreateKind: %v", err)
	}
	create := "env HTTP_PROXY= http_proxy= HTTPS_PROXY=http://proxy.corp:3128 https_proxy=http://proxy.corp:3128 NO_PROXY=.corp no_proxy=.corp kind create cluster --config c.yaml --name panda"
	if f.calls[0] != create {
		t.Fatalf("first call %q, want %q", f.calls[0], create)
	}
	for _, node := range []string{"panda-control-plane", "panda-worker"} {
		if !f.called("docker cp /etc/corp-ca.pem " + node + ":/root/kates-proxy-ca.crt") {
			t.Errorf("the CA was not copied into %s: %v", node, f.calls)
		}
		if !f.called("docker exec -e HTTP_PROXY= -e HTTPS_PROXY=http://proxy.corp:3128 -e NO_PROXY=.corp " + node + " /bin/bash -lc") {
			t.Errorf("containerd on %s was not configured: %v", node, f.calls)
		}
	}
	// The script restarts containerd, so readiness is waited for after it.
	last := strings.Join(f.calls, "\n")
	if strings.Index(last, "docker exec") > strings.Index(last, "kubectl wait") {
		t.Errorf("nodes must be configured before the readiness wait: %v", f.calls)
	}
}

func TestCreateKind_WithoutAProxyTouchesNoNode(t *testing.T) {
	f := newFake().on("kubectl get nodes", "alpha", nil)
	if err := CreateKind(f, KindOptions{ConfigPath: "c.yaml"}); err != nil {
		t.Fatal(err)
	}
	if f.called("env ") || f.called("docker ") || !strings.HasPrefix(f.calls[0], "kind create cluster") {
		t.Errorf("no proxy, no proxy setup: %v", f.calls)
	}
}

func TestCreateKind_ProxySetupFailureIsReported(t *testing.T) {
	f := newFake().
		on("kind get nodes", "panda-control-plane", nil).
		on("docker exec", "update-ca-certificates: command failed", errors.New("exit 1"))
	err := CreateKind(f, KindOptions{ConfigPath: "c.yaml", Proxy: KindProxy{HTTPS: "http://p:3128", Set: true}})
	if err == nil || !strings.Contains(err.Error(), "panda-control-plane") || !strings.Contains(err.Error(), "update-ca-certificates") {
		t.Fatalf("want the node and its output in the error, got %v", err)
	}
}
