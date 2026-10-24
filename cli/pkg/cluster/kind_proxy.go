package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// KindProxy is the proxy the kind nodes pull images through, and the CA the
// proxy signs TLS with, as scripts/start-cluster.sh gives them to `make
// cluster`. Only a new cluster gets them: there is nothing to clear.
type KindProxy struct {
	HTTP, HTTPS, NoProxy string
	// CAFile is a PEM certificate the nodes trust, for a proxy that intercepts TLS.
	CAFile string
	// Set is true when the environment named any proxy variable, so kind runs
	// with exactly the values above rather than whatever it would inherit.
	Set bool
}

// Configures reports whether the nodes need the proxy or the CA set up.
func (p KindProxy) Configures() bool {
	return p.HTTP != "" || p.HTTPS != "" || p.CAFile != ""
}

// ProxyFromEnv reads the proxy the way start-cluster.sh does: HTTP_PROXY,
// HTTPS_PROXY and NO_PROXY, the upper-case name first, and PROXY_CA_FILE. A
// loopback proxy is dropped with a warning, because the nodes are containers
// and their localhost is not the host's.
func ProxyFromEnv(getenv func(string) string) (KindProxy, []string, error) {
	first := func(names ...string) (string, bool) {
		for _, n := range names {
			if v := getenv(n); v != "" {
				return v, true
			}
		}
		return "", false
	}
	var p KindProxy
	var warnings []string
	var set bool
	for _, v := range []struct {
		upper, lower string
		dst          *string
	}{
		{"HTTP_PROXY", "http_proxy", &p.HTTP},
		{"HTTPS_PROXY", "https_proxy", &p.HTTPS},
		{"NO_PROXY", "no_proxy", &p.NoProxy},
	} {
		value, ok := first(v.upper, v.lower)
		set = set || ok
		if v.upper != "NO_PROXY" && isLoopbackProxy(value) {
			warnings = append(warnings, fmt.Sprintf("%s=%s is not used for the kind nodes: their localhost is not this machine's. Use an address the nodes can reach, such as host.docker.internal", v.upper, value))
			value = ""
		}
		*v.dst = value
	}

	if ca := getenv("PROXY_CA_FILE"); ca != "" {
		abs, err := filepath.Abs(ca)
		if err != nil {
			return KindProxy{}, nil, fmt.Errorf("PROXY_CA_FILE %s: %w", ca, err)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return KindProxy{}, nil, fmt.Errorf("PROXY_CA_FILE: %w", err)
		}
		if !strings.Contains(string(data), "-----BEGIN CERTIFICATE-----") {
			return KindProxy{}, nil, fmt.Errorf("PROXY_CA_FILE %s holds no PEM certificate", abs)
		}
		p.CAFile, set = abs, true
	}
	p.Set = set
	return p, warnings, nil
}

// isLoopbackProxy is start-cluster.sh's is_loopback_proxy_url.
func isLoopbackProxy(url string) bool {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if strings.Contains(url, "://"+host) || strings.Contains(url, "@"+host) {
			return true
		}
	}
	return false
}

// kindCreateCommand is `kind create cluster`, run through env when the
// environment named a proxy, so kind hands the nodes the resolved values in
// both spellings and never a dropped loopback one.
func kindCreateCommand(p KindProxy, args []string) (string, []string) {
	if !p.Set {
		return "kind", args
	}
	env := []string{}
	for _, v := range []struct{ upper, lower, value string }{
		{"HTTP_PROXY", "http_proxy", p.HTTP},
		{"HTTPS_PROXY", "https_proxy", p.HTTPS},
		{"NO_PROXY", "no_proxy", p.NoProxy},
	} {
		env = append(env, v.upper+"="+v.value, v.lower+"="+v.value)
	}
	return "env", append(append(env, "kind"), args...)
}

// containerdProxyScript is start-cluster.sh's reconcile for one node, for a
// new cluster: the containerd proxy drop-in, the staged CA trusted, containerd
// restarted.
const containerdProxyScript = `set -e
dropin_dir="/etc/systemd/system/containerd.service.d"
dropin_file="${dropin_dir}/10-kates-proxy.conf"
tmp_file="${dropin_file}.tmp"
managed_ca="/usr/local/share/ca-certificates/kates-proxy-ca.crt"
staged_ca="/root/kates-proxy-ca.crt"
mkdir -p "${dropin_dir}"
{
  echo "[Service]"
  printf "Environment=\"HTTP_PROXY=%s\"\n" "${HTTP_PROXY:-}"
  printf "Environment=\"http_proxy=%s\"\n" "${HTTP_PROXY:-}"
  printf "Environment=\"HTTPS_PROXY=%s\"\n" "${HTTPS_PROXY:-}"
  printf "Environment=\"https_proxy=%s\"\n" "${HTTPS_PROXY:-}"
  printf "Environment=\"NO_PROXY=%s\"\n" "${NO_PROXY:-}"
  printf "Environment=\"no_proxy=%s\"\n" "${NO_PROXY:-}"
} > "${tmp_file}"
mv "${tmp_file}" "${dropin_file}"
if [ -f "${staged_ca}" ]; then
  mv "${staged_ca}" "${managed_ca}"
  chmod 0644 "${managed_ca}"
  update-ca-certificates
fi
systemctl daemon-reload
systemctl restart containerd
`

// configureNodeProxy gives every node of cluster name the proxy and the CA.
func configureNodeProxy(exec detect.CommandExecutor, name string, p KindProxy) error {
	out, err := exec.Exec("kind", "get", "nodes", "--name", name)
	if err != nil {
		return fmt.Errorf("listing the nodes of %s: %w", name, err)
	}
	nodes := strings.Fields(out)
	if len(nodes) == 0 {
		return fmt.Errorf("kind lists no nodes for %s", name)
	}
	for _, node := range nodes {
		if p.CAFile != "" {
			if out, err := exec.Exec("docker", "cp", p.CAFile, node+":/root/kates-proxy-ca.crt"); err != nil {
				return fmt.Errorf("copying the proxy CA into %s: %w: %s", node, err, strings.TrimSpace(out))
			}
		}
		if out, err := exec.Exec("docker", "exec",
			"-e", "HTTP_PROXY="+p.HTTP, "-e", "HTTPS_PROXY="+p.HTTPS, "-e", "NO_PROXY="+p.NoProxy,
			node, "/bin/bash", "-lc", containerdProxyScript); err != nil {
			return fmt.Errorf("configuring the proxy on %s: %w: %s", node, err, strings.TrimSpace(out))
		}
	}
	return nil
}
