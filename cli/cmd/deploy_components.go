package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

// kyvernoChartVersion pins the Kyverno chart: the last stable patch of the
// 3.6 minor series. Chart versions: https://kyverno.github.io/kyverno/
const kyvernoChartVersion = "3.6.4"

type deployProxySettings struct {
	http    string
	https   string
	noProxy string
}

var readStrimziCRDBundleFn = strimzi.CRDBundle

var prometheusOperatorCRDNames = []string{
	"alertmanagerconfigs.monitoring.coreos.com",
	"alertmanagers.monitoring.coreos.com",
	"podmonitors.monitoring.coreos.com",
	"probes.monitoring.coreos.com",
	"prometheusagents.monitoring.coreos.com",
	"prometheuses.monitoring.coreos.com",
	"prometheusrules.monitoring.coreos.com",
	"scrapeconfigs.monitoring.coreos.com",
	"servicemonitors.monitoring.coreos.com",
	"thanosrulers.monitoring.coreos.com",
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func loadProxyConf(path string) map[string]string {
	values := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return values
	}

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		values[key] = value
	}
	return values
}

func rewriteLoopbackProxyURLForKind(proxyURL string) string {
	if proxyURL == "" {
		return proxyURL
	}
	rewritten := proxyURL
	rewritten = strings.ReplaceAll(rewritten, "://localhost", "://host.docker.internal")
	rewritten = strings.ReplaceAll(rewritten, "://127.0.0.1", "://host.docker.internal")
	rewritten = strings.ReplaceAll(rewritten, "://[::1]", "://host.docker.internal")
	rewritten = strings.ReplaceAll(rewritten, "@localhost", "@host.docker.internal")
	rewritten = strings.ReplaceAll(rewritten, "@127.0.0.1", "@host.docker.internal")
	rewritten = strings.ReplaceAll(rewritten, "@[::1]", "@host.docker.internal")
	return rewritten
}

func detectDeployProxySettings(isKind bool) deployProxySettings {
	settings := deployProxySettings{
		http:    firstNonEmpty(os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy")),
		https:   firstNonEmpty(os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy")),
		noProxy: firstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy")),
	}

	if settings.http == "" || settings.https == "" || settings.noProxy == "" {
		conf := loadProxyConf("proxy/proxy.conf")
		if settings.http == "" {
			settings.http = firstNonEmpty(conf["HTTP_PROXY"], conf["http_proxy"])
		}
		if settings.https == "" {
			settings.https = firstNonEmpty(conf["HTTPS_PROXY"], conf["https_proxy"])
		}
		if settings.noProxy == "" {
			settings.noProxy = firstNonEmpty(conf["NO_PROXY"], conf["no_proxy"])
		}
	}

	if isKind {
		settings.http = rewriteLoopbackProxyURLForKind(settings.http)
		settings.https = rewriteLoopbackProxyURLForKind(settings.https)
	} else {
		settings.http = dropLoopbackProxyURL(settings.http)
		settings.https = dropLoopbackProxyURL(settings.https)
	}

	return settings
}

func dropLoopbackProxyURL(proxyURL string) string {
	if isLoopbackProxyURL(proxyURL) {
		return ""
	}
	return proxyURL
}

func isLoopbackProxyURL(proxyURL string) bool {
	if strings.TrimSpace(proxyURL) == "" {
		return false
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func appendUniqueCommaSeparated(existing string, values ...string) string {
	result := []string{}
	seen := map[string]struct{}{}
	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}

	for _, item := range strings.Split(existing, ",") {
		add(item)
	}
	for _, item := range values {
		add(item)
	}

	return strings.Join(result, ",")
}

func ensureKubernetesNoProxy(noProxy, clusterDomain string) string {
	domain := strings.TrimSpace(clusterDomain)
	if domain == "" {
		domain = "cluster.local"
	}

	return appendUniqueCommaSeparated(
		noProxy,
		"localhost",
		"127.0.0.1",
		"::1",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc."+domain,
	)
}

// deployNamespaceLabelMap is the user's namespace labels plus the built-in
// managed-by label, which ensureDeployNamespace sets only on a namespace it
// creates.
func deployNamespaceLabelMap() (map[string]string, error) {
	labels, _, err := namespaceUserLabels()
	if err != nil {
		return nil, err
	}
	labels[managedByLabel] = "kates"
	return labels, nil
}

func podSecurityRejectsHostLevelPods(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "baseline", "restricted":
		return true
	default:
		return false
	}
}

func monitoringNamespaceRejectsNodeExporter(ctx context.Context, namespace string) (bool, error) {
	labels, err := deployNamespaceLabelMap()
	if err != nil {
		return false, err
	}
	if podSecurityRejectsHostLevelPods(labels["pod-security.kubernetes.io/enforce"]) {
		return true, nil
	}

	out, err := runExecOutputFn(ctx, "kubectl", "get", "namespace", namespace, "-o", `jsonpath={.metadata.labels.pod-security\.kubernetes\.io/enforce}`)
	if err != nil {
		return false, nil
	}
	return podSecurityRejectsHostLevelPods(string(out)), nil
}

func monitoringNodeExporterDaemonSetScheduled(ctx context.Context, namespace string) bool {
	out, err := runExecOutputFn(ctx, "kubectl", "get", "daemonset", "monitoring-prometheus-node-exporter",
		"-n", namespace, "-o", "jsonpath={.status.desiredNumberScheduled}")
	if err != nil {
		return false
	}
	desired, err := strconv.Atoi(strings.TrimSpace(string(out)))
	return err == nil && desired > 0
}

// deployNamespaceManifest is a Namespace carrying labels, and the annotation
// that records which of them are the user's (every key but managed-by).
func deployNamespaceManifest(name string, labels map[string]string) string {
	var annotations map[string]string
	if user := userNamespaceLabels(labels); len(user) > 0 {
		annotations = map[string]string{namespaceLabelsAnnotation: strings.Join(slices.Sorted(maps.Keys(user)), ",")}
	}
	return detect.NamespaceManifest(name, labels, annotations)
}

func userNamespaceLabels(labels map[string]string) map[string]string {
	user := maps.Clone(labels)
	delete(user, managedByLabel)
	return user
}

// ensureDeployNamespace creates a missing namespace with labels, or brings an
// existing one's user labels in line with them. On an existing namespace it
// never touches managed-by (the namespace is not Kates'), and removes only
// keys its annotation says Kates set.
func ensureDeployNamespace(ctx context.Context, namespace string, labels map[string]string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil
	}
	out, err := runExecOutputFn(ctx, "kubectl", "get", "namespace", namespace, "--ignore-not-found", "-o", "json")
	if err != nil {
		return fmt.Errorf("check namespace %s: %w", namespace, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		manifest := deployNamespaceManifest(namespace, labels)
		if err := runExecStdinFn(ctx, "kubectl", []string{"create", "-f", "-"}, manifest); err != nil {
			if rbacRefused(err, "create") {
				return fmt.Errorf("create namespace %s: %w\n\nThis account may not create namespaces. Ask a cluster admin to apply this manifest (kubectl create -f <file>), then run again:\n\n%s", namespace, err, manifest)
			}
			return fmt.Errorf("create namespace %s: %w", namespace, err)
		}
		return nil
	}

	var current struct {
		Metadata struct{ Labels, Annotations map[string]string }
	}
	if err := json.Unmarshal(out, &current); err != nil {
		return fmt.Errorf("check namespace %s: %w", namespace, err)
	}
	user, have := userNamespaceLabels(labels), current.Metadata.Labels
	set := map[string]string{}
	for key, value := range user {
		if old, ok := have[key]; !ok || old != value {
			set[key] = value
		}
	}
	var remove, tracked []string
	for _, key := range strings.Split(current.Metadata.Annotations[namespaceLabelsAnnotation], ",") {
		key = strings.TrimSpace(key)
		if _, wanted := user[key]; wanted {
			tracked = append(tracked, key)
		} else if _, present := have[key]; key != "" && present {
			remove = append(remove, key)
		}
	}
	// No call when nothing changes, so an account that may not patch
	// namespaces still passes once the labels are right.
	if len(set) == 0 && len(remove) == 0 {
		return nil
	}

	labelPatch := map[string]any{}
	for key, value := range set {
		labelPatch[key] = value
		if !slices.Contains(tracked, key) {
			tracked = append(tracked, key)
		}
	}
	for _, key := range remove {
		labelPatch[key] = nil
	}
	var annotation any // null removes it
	if len(tracked) > 0 {
		slices.Sort(tracked)
		annotation = strings.Join(tracked, ",")
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"labels":      labelPatch,
		"annotations": map[string]any{namespaceLabelsAnnotation: annotation},
	}})
	if err != nil {
		return err
	}
	if out, err := runExecCombinedFn(ctx, "kubectl", "patch", "namespace", namespace, "--type", "merge", "-p", string(patch)); err != nil {
		err = commandErrorWithOutput("patch namespace "+namespace, err, out)
		if !rbacRefused(err, "patch") {
			return err
		}
		args := []string{"kubectl", "label", "namespace", namespace}
		for _, key := range slices.Sorted(maps.Keys(set)) {
			args = append(args, key+"="+set[key])
		}
		slices.Sort(remove)
		for _, key := range remove {
			args = append(args, key+"-")
		}
		return fmt.Errorf("%w\n\nThis account may not change the labels of namespace %s. Ask a cluster admin to run this, then run again:\n\n  %s", err, namespace, strings.Join(append(args, "--overwrite"), " "))
	}
	return nil
}

// rbacRefused says whether RBAC refused verb on a resource, as the API
// server words it ("User ... cannot create resource ..."). An admission
// webhook or policy denial also says Forbidden, but an admin running the same
// request is refused the same way, so it gets no admin instruction.
func rbacRefused(err error, verb string) bool {
	return strings.Contains(err.Error(), "cannot "+verb+" resource")
}

func ensureConfiguredDeployNamespace(ctx context.Context, namespace string) error {
	labels, err := deployNamespaceLabelMap()
	if err != nil {
		return err
	}
	return ensureDeployNamespace(ctx, namespace, labels)
}

func ensureDeployNamespaces(ctx context.Context, dc *deployContext) error {
	labels, err := deployNamespaceLabelMap()
	if err != nil {
		return err
	}
	names := selectedDeployNamespaces(dc)
	if len(names) == 0 {
		return nil
	}
	dl.Printf("    - Ensuring deploy namespaces exist (%s)...\n", strings.Join(names, ", "))
	for _, name := range names {
		if err := ensureDeployNamespace(ctx, name, labels); err != nil {
			return err
		}
	}
	return nil
}

func selectedDeployNamespaces(dc *deployContext) []string {
	seen := map[string]bool{}
	var names []string
	add := func(namespace string) {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" || seen[namespace] {
			return
		}
		seen[namespace] = true
		names = append(names, namespace)
	}

	add(dc.ns.kafka)
	add(dc.ns.app)
	if deployWithMonitoring {
		add(dc.ns.jaeger)
	}
	if deployWithKafkaConnect {
		add(dc.ns.connect)
		add(deployDbNS)
	}
	if deployWithKafkaUI {
		add(dc.ns.kafkaUI)
	}
	if deployWithMirrorMaker2 {
		add(dc.ns.mm2)
	}
	if deployWithChaos {
		add(dc.ns.chaos)
	}
	return names
}

func isKatesPostgresImmutableStatefulSetError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, `StatefulSet.apps "kates-postgresql" is invalid`) &&
		strings.Contains(msg, "Forbidden: updates to statefulset spec") &&
		strings.Contains(msg, "are forbidden")
}

func runKatesHelmUpgrade(ctx context.Context, appNS string, args ...string) error {
	err := runHelmFn(ctx, args...)
	if err == nil {
		return nil
	}
	if !isKatesPostgresImmutableStatefulSetError(err) {
		return err
	}

	dl.Println("    - Recreating immutable kates-postgresql StatefulSet object, preserving pod and PVC...")
	if _, deleteErr := runExecCombinedFn(ctx, "kubectl", "delete", "statefulset", "kates-postgresql",
		"-n", appNS, "--cascade=orphan", "--ignore-not-found"); deleteErr != nil {
		return fmt.Errorf("kates helm upgrade hit immutable PostgreSQL StatefulSet and recovery failed: %v (original: %w)", deleteErr, err)
	}

	dl.Println("    - Retrying Kates backend Helm upgrade...")
	if retryErr := runHelmFn(ctx, args...); retryErr != nil {
		return fmt.Errorf("kates helm upgrade retry after recreating PostgreSQL StatefulSet failed: %w", retryErr)
	}
	return nil
}

func prepareEmptyPreV1StrimziReplacement(ctx context.Context, vp *versionPlan) error {
	if vp == nil || !vp.ReplaceEmptyPreV1CRDs {
		return nil
	}
	if vp.primaryOp != nil {
		release := vp.primaryOp.HelmRelease
		if release == "" {
			return fmt.Errorf("cannot replace empty pre-v1 Strimzi CRDs automatically: the running operator %s/%s is not owned by a discoverable Helm release",
				vp.primaryOp.Namespace, vp.primaryOp.Name)
		}

		dl.Printf("    - Empty pre-v1 Strimzi install detected; uninstalling Helm release %s/%s before CRD replacement...\n", vp.primaryOp.Namespace, release)
		if out, err := runExecCombinedFn(ctx, "helm", "uninstall", release, "-n", vp.primaryOp.Namespace, "--wait", "--timeout", "5m"); err != nil {
			return commandErrorWithOutput(fmt.Sprintf("uninstall old Strimzi operator release %s/%s", vp.primaryOp.Namespace, release), err, out)
		}
	}

	if len(vp.ReplaceCRDs) == 0 {
		return nil
	}
	dl.Printf("    - Labelling %d empty pre-v1 Strimzi CRDs for deletion...\n", len(vp.ReplaceCRDs))
	labelArgs := append([]string{"label", "crd"}, vp.ReplaceCRDs...)
	labelArgs = append(labelArgs, "allowDeletion=true", "--overwrite")
	if out, err := runExecCombinedFn(ctx, "kubectl", labelArgs...); err != nil {
		return commandErrorWithOutput("label empty pre-v1 Strimzi CRDs for deletion", err, out)
	}
	dl.Printf("    - Deleting %d empty pre-v1 Strimzi CRDs so v1 CRDs can be installed cleanly...\n", len(vp.ReplaceCRDs))
	args := append([]string{"delete", "crd"}, vp.ReplaceCRDs...)
	args = append(args, "--ignore-not-found", "--timeout=120s")
	if out, err := runExecCombinedFn(ctx, "kubectl", args...); err != nil {
		return commandErrorWithOutput("delete empty pre-v1 Strimzi CRDs", err, out)
	}
	return nil
}

func applyLocalStrimziCRDs(ctx context.Context, vp *versionPlan) error {
	if vp == nil || strings.TrimSpace(vp.ChartPath) == "" {
		return fmt.Errorf("cannot apply Strimzi CRDs locally: no operator chart tarball was resolved")
	}
	bundle, names, err := readStrimziCRDBundleFn(vp.ChartPath)
	if err != nil {
		return fmt.Errorf("read Strimzi CRDs from local chart %s: %w", vp.ChartPath, err)
	}
	if missing := missingRequiredStrimziCRDs(names); len(missing) > 0 {
		return fmt.Errorf("operator chart %s does not contain required Strimzi CRDs: %s", vp.ChartPath, strings.Join(missing, ", "))
	}

	dl.Printf("    - Applying Strimzi CRDs from local chart %s...\n", filepath.Base(vp.ChartPath))
	if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "--server-side", "--force-conflicts", "-f", "-"}, bundle); err != nil {
		return fmt.Errorf("apply Strimzi CRDs from local chart %s: %w", vp.ChartPath, err)
	}

	args := []string{"wait", "--for=condition=Established"}
	for _, crd := range strimzi.RequiredCRDNames() {
		args = append(args, "crd/"+crd)
	}
	args = append(args, "--timeout=180s")
	if out, err := runExecCombinedFn(ctx, "kubectl", args...); err != nil {
		return commandErrorWithOutput("wait for Strimzi CRDs to be Established", err, out)
	}
	return nil
}

func missingRequiredStrimziCRDs(names []string) []string {
	present := map[string]bool{}
	for _, name := range names {
		present[name] = true
	}
	var missing []string
	for _, required := range strimzi.RequiredCRDNames() {
		if !present[required] {
			missing = append(missing, required)
		}
	}
	return missing
}

func commandErrorWithOutput(action string, err error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, msg)
}

func isKyvernoAvailable(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := runExecOutputFn(checkCtx, "kubectl", "-n", "kyverno", "get", "deploy", "-l", "app.kubernetes.io/part-of=kyverno", "-o", "json")
	if err != nil {
		return false
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int32 `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int32 `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return false
	}

	required := map[string]bool{
		"kyverno-admission-controller":  false,
		"kyverno-background-controller": false,
		"kyverno-cleanup-controller":    false,
		"kyverno-reports-controller":    false,
	}
	for _, item := range list.Items {
		if _, ok := required[item.Metadata.Name]; !ok {
			continue
		}
		desired := int32(1)
		if item.Spec.Replicas != nil {
			desired = *item.Spec.Replicas
		}
		if desired > 0 && item.Status.ReadyReplicas >= desired {
			required[item.Metadata.Name] = true
		}
	}
	for _, ready := range required {
		if !ready {
			return false
		}
	}
	return true
}

func prometheusOperatorCRDsAvailable(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	args := append([]string{"get", "crd"}, prometheusOperatorCRDNames...)
	if _, err := runExecOutputFn(checkCtx, "kubectl", args...); err != nil {
		return false
	}
	return true
}

func isKafkaClusterReady(ctx context.Context, namespace, name string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := runExecOutputFn(checkCtx,
		"kubectl", "get", "kafka", name, "-n", namespace,
		"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`,
	)
	return err == nil && strings.Contains(string(out), "True")
}

func kafkaConnectNeedsReconcile(ctx context.Context, namespace, name, expectedBootstrap, expectedKafkaSearch string) (bool, string) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := runExecOutputFn(checkCtx, "kubectl", "get", "kafkaconnect", name, "-n", namespace, "-o", "json")
	if err != nil {
		return true, "KafkaConnect CR is missing or unreadable"
	}

	var kc struct {
		Spec struct {
			BootstrapServers string         `json:"bootstrapServers"`
			Config           map[string]any `json:"config"`
			Template         struct {
				Pod struct {
					DNSConfig struct {
						Searches []string `json:"searches"`
					} `json:"dnsConfig"`
				} `json:"pod"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			Conditions []statusCondition `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &kc); err != nil {
		return true, "KafkaConnect CR cannot be parsed"
	}

	if expectedBootstrap != "" && strings.TrimSpace(kc.Spec.BootstrapServers) != expectedBootstrap {
		return true, fmt.Sprintf("bootstrap is %q, expected %q", strings.TrimSpace(kc.Spec.BootstrapServers), expectedBootstrap)
	}
	if expectedKafkaSearch != "" && !stringSliceContains(kc.Spec.Template.Pod.DNSConfig.Searches, expectedKafkaSearch) {
		return true, fmt.Sprintf("DNS search list misses %q", expectedKafkaSearch)
	}
	if v, ok := kc.Spec.Config["schema"]; ok {
		if _, nested := v.(map[string]any); nested {
			return true, "dotted Connect config key schema.registry.url was stored as a nested map"
		}
	}
	if isReadyConditionTrue(kc.Status.Conditions) != "True" {
		return true, "KafkaConnect CR is not Ready"
	}
	return false, ""
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cleanupStaleKatesChartDependencies(chartDir string) error {
	data, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", filepath.Join(chartDir, "Chart.yaml"), err)
	}

	var chart struct {
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return fmt.Errorf("parsing %s: %w", filepath.Join(chartDir, "Chart.yaml"), err)
	}
	if len(chart.Dependencies) > 0 {
		return nil
	}

	removed := false
	for _, path := range []string{filepath.Join(chartDir, "Chart.lock"), filepath.Join(chartDir, "charts")} {
		if _, err := os.Stat(path); err == nil {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("removing stale Helm dependency artifact %s: %w", path, err)
			}
			removed = true
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking stale Helm dependency artifact %s: %w", path, err)
		}
	}
	if removed {
		dl.Println("    - Removed stale Kates chart dependency artifacts...")
	}
	return nil
}

// deployGroupA deploys Group A components (Operators & CRDs) in parallel:
// Strimzi, Cert-Manager, and Kyverno.
func deployGroupA(dc *deployContext) error {
	ctx := dc.ctx

	// ---------------------------------------------------------
	// GROUP A: Operators & CRDs (Parallel)
	// ---------------------------------------------------------
	// Strimzi is reconciled unconditionally. `helm upgrade --install` converges,
	// and charts/strimzi-operator's pre-upgrade hook is what keeps the CRDs
	// current — skipping when the release already exists means the CRDs can
	// silently freeze at whatever version first installed them.
	deployStrimzi := deployWithStrimzi
	deployCertMgr := false
	if deployWithCertManager {
		deployCertMgr = !isHelmReleaseDeployedFn(ctx, "cert-manager", "cert-manager")
	}
	deployKyvernoFlag := false
	if deployWithKyverno {
		deployKyvernoFlag = !isHelmReleaseDeployedFn(ctx, "kyverno", "kyverno")
		if deployKyvernoFlag && isKyvernoAvailable(ctx) {
			deployKyvernoFlag = false
		}
	}
	dc.kyvernoInstalled = deployKyvernoFlag

	g, gCtx := errgroup.WithContext(ctx)

	// Deploy Strimzi Operator
	if deployWithStrimzi {
		g.Go(func() error {
			if !deployStrimzi {
				dl.Println(output.Glyphs().Skip + "  Strimzi Operator already deployed. Skipping.")
				dl.FinishComponent("strimzi", true)
				dc.advanceStep()
				return nil
			}
			dl.Println("\n📦 Deploying Strimzi Operator (Namespace: strimzi-operator)...")
			if err := prepareEmptyPreV1StrimziReplacement(gCtx, dc.versions); err != nil {
				return err
			}
			// Create namespace properly
			if err := ensureConfiguredDeployNamespace(gCtx, "strimzi-operator"); err != nil {
				return err
			}
			clusterDomain := dc.resolveClusterDomain()
			// The wrapper chart does not render until its subchart is fetched.
			// `build` (not `update`) resolves from Chart.lock, so the pinned
			// Strimzi version cannot drift. A non-pinned version installs
			// from the generated wrapper under the cache, whose charts/
			// already holds the pulled tarball (deploy_versions.go).
			chartDir := chartRootPath(operatorChartDir)
			var versionArgs []string
			if dc.versions != nil {
				if !dc.versions.Pinned {
					chartDir = dc.versions.ChartDir
				}
				versionArgs = dc.versions.operatorHelmArgs()
			}
			if dc.versions == nil || dc.versions.Pinned {
				if err := helmDependency(gCtx, "build", chartRootPath(operatorChartDir)); err != nil {
					return err
				}
			}
			if err := applyLocalStrimziCRDs(gCtx, dc.versions); err != nil {
				return err
			}
			// Everything this call site used to --set is now pinned in the
			// chart's values.yaml; only the cluster domain is environment-
			// specific. The old leaderElection.enabled=false is gone on
			// purpose: `enabled` was a typo for the upstream key `enable`, so
			// Helm silently ignored it and leader election has always been on.
			// Dropping it preserves that behavior; turning it off is a separate
			// decision, not a side effect of this refactor.
			//
			// --reset-values: pre-chart releases stored flat upstream keys that
			// now live under the subchart key, and the schema rejects them.
			//
			// --skip-crds: applyLocalStrimziCRDs already applied them as the
			// "kubectl" field manager; Helm 4 re-applying crds/ on a fresh
			// install conflicts with it over .spec.versions.
			opArgs := []string{"upgrade", "--install", "strimzi-operator", chartDir, "-n", "strimzi-operator",
				"--reset-values", "--skip-crds",
				"-f", dc.chartOverlay(chartRootPath(operatorChartDir)),
				"--set", "strimzi-kafka-operator.kubernetesServiceDnsDomain=" + clusterDomain,
				"--set", "crdUpgrade.enabled=false"}
			opArgs = append(opArgs, versionArgs...)
			opArgs = append(opArgs, "--timeout", "10m")
			if err := runHelmFn(gCtx, opArgs...); err != nil {
				return err
			}
			return nil
		})
	}

	// Deploy Cert-Manager
	if deployWithCertManager {
		g.Go(func() error {
			if !deployCertMgr {
				dl.Println(output.Glyphs().Skip + "  Cert-Manager already deployed. Skipping.")
				dl.FinishComponent("cert-manager", true)
				dc.advanceStep()
				return nil
			}
			dl.Printf("\n📦 Deploying Cert-Manager (Namespace: %s)...\n", "cert-manager")
			runHelmFn(gCtx, "repo", "add", "jetstack", "https://charts.jetstack.io")
			runHelmFn(gCtx, "repo", "update", "jetstack")
			// Created here with the namespace labels; --create-namespace
			// would make it bare.
			if err := ensureConfiguredDeployNamespace(gCtx, "cert-manager"); err != nil {
				return err
			}
			// global.clusterDomain ensures cert-manager generates webhook TLS certificates
			// and service references using the actual cluster DNS domain (not always cluster.local).
			// report.Network.ClusterDomain is detected from the live cluster by kates detect.
			clusterDomain := dc.resolveClusterDomain()
			err := runHelmFn(gCtx, "upgrade", "--install", "cert-manager", "jetstack/cert-manager",
				"--version", "v1.13.3",
				"-n", "cert-manager", "--create-namespace",
				"--set", "installCRDs=true",
				"--set", "startupapicheck.enabled=false",
				"--set", "global.clusterDomain="+clusterDomain,
				"--timeout", "10m")
			if err != nil {
				return err
			}

			dl.Println("    - Waiting for Cert-Manager CRDs to be established...")
			if err := runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
				"crd", "clusterissuers.cert-manager.io", "--timeout=180s"); err != nil {
				return err
			}

			// On re-deploys the cainjector may hold a stale CA from the previous
			// installation. Restart it to force fresh CA bundle generation and
			// re-injection into the MutatingWebhookConfiguration.
			dl.Println("    - Restarting Cert-Manager CA injector for fresh CA bundle...")
			runExecFn(gCtx, "kubectl", "rollout", "restart",
				"deployment/cert-manager-cainjector", "-n", "cert-manager")
			runExecFn(gCtx, "kubectl", "rollout", "status",
				"deployment/cert-manager-cainjector", "-n", "cert-manager", "--timeout=90s")

			// The webhook pod must be serving before the API server can call it.
			// The Group A readiness wait for cert-manager only runs after this
			// goroutine returns, so without this gate the ClusterIssuer apply
			// below races the webhook Deployment and dies with
			// "Internal error occurred: failed calling webhook".
			dl.Println("    - Waiting for Cert-Manager webhook rollout...")
			runExecFn(gCtx, "kubectl", "rollout", "status",
				"deployment/cert-manager-webhook", "-n", "cert-manager", "--timeout=180s")

			// The definitive readiness signal: poll until caBundle is non-empty —
			// on BOTH webhook configurations. A ClusterIssuer CREATE goes through
			// the validating webhook as well, and cainjector injects the two
			// configs independently, so a ready mutating config says nothing
			// about the validating one; checking only it leaves a window where
			// the apply still fails with x509: certificate signed by unknown
			// authority (surfaced by the API server as "Internal error occurred").
			dl.Println("    - Polling for Cert-Manager CA bundle injection...")
			const caTimeout = 180 * time.Second
			const caPoll = 3 * time.Second
			caDeadline := time.Now().Add(caTimeout)
			certMgrWebhookConfigs := []string{
				"mutatingwebhookconfiguration",
				"validatingwebhookconfiguration",
			}
			for {
				injected := 0
				for _, kind := range certMgrWebhookConfigs {
					out, err := runExecOutputFn(gCtx,
						"kubectl", "get", kind, "cert-manager-webhook",
						"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
					)
					if err == nil && len(strings.TrimSpace(string(out))) > 0 {
						injected++
					}
				}
				if injected == len(certMgrWebhookConfigs) {
					dl.Println("    - CA bundle injected. Applying ClusterIssuer...")
					break
				}
				if time.Now().After(caDeadline) {
					dl.Println("    ⚠ CA bundle injection timed out — proceeding with webhook bypass fallback")
					break
				}
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case <-time.After(caPoll):
				}
			}

			clusterIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}`

			// Try applying normally first. Early attempts can land in the short
			// window where the CA bundle is injected but the API server's webhook
			// client hasn't seen ready endpoints yet — give it a real window
			// before reaching for the failurePolicy bypass.
			var lastErr error
			const applyAttempts = 5
			for attempt := 1; attempt <= applyAttempts; attempt++ {
				if lastErr = runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer); lastErr == nil {
					return nil
				}
				if attempt < applyAttempts {
					select {
					case <-gCtx.Done():
						return gCtx.Err()
					case <-time.After(5 * time.Second):
					}
				}
			}

			// Hard fallback: temporarily set failurePolicy: Ignore so the API server
			// allows the resource creation call through even if webhook TLS is still
			// not verifiable. BOTH configs must be bypassed — the validating webhook
			// blocks the CREATE exactly like the mutating one, so patching only the
			// mutating config leaves the failure in place.
			dl.Println("    ⚠ Webhook TLS not verifiable — temporarily setting failurePolicy: Ignore")
			defer restoreCertMgrWebhookPolicy(gCtx, certMgrWebhookConfigs)
			for _, kind := range certMgrWebhookConfigs {
				runExecFn(gCtx, "kubectl", "patch",
					kind, "cert-manager-webhook",
					"--type=json", "-p",
					`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]`)
			}
			time.Sleep(2 * time.Second) // let the API server pick up the patch

			applyErr := runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer)
			if applyErr != nil {
				return fmt.Errorf("failed to apply cert-manager ClusterIssuer: %w", applyErr)
			}
			dl.Println("    - ClusterIssuer created. Restoring the webhook policy.")
			return nil
		})
	}

	// Deploy Kyverno
	if deployWithKyverno {
		g.Go(func() error {
			if !deployKyvernoFlag {
				dl.Println(output.Glyphs().Skip + "  Kyverno already deployed. Skipping.")
				dl.FinishComponent("kyverno", true)
				dc.advanceStep()
				return nil
			}
			dl.Println("\n📦 Deploying Kyverno (Namespace: kyverno)...")
			runHelmFn(gCtx, "repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
			runHelmFn(gCtx, "repo", "update", "kyverno")
			if err := ensureConfiguredDeployNamespace(gCtx, "kyverno"); err != nil {
				return err
			}

			// Kyverno v3.x splits into 4 controllers; replicaCount=1 is a v2.x flag.
			// global.clusterDomain ensures Kyverno webhook certificates use the
			// correct cluster DNS domain — same value detected for all other components.
			kyvernoDomain := dc.resolveClusterDomain()
			err := runHelmFn(gCtx, "upgrade", "--install", "kyverno", "kyverno/kyverno",
				"--version", kyvernoChartVersion,
				"-n", "kyverno", "--create-namespace",
				"--set", "admissionController.replicas=1",
				"--set", "backgroundController.replicas=1",
				"--set", "cleanupController.replicas=1",
				"--set", "reportsController.replicas=1",
				"--set", "global.clusterDomain="+kyvernoDomain,
				"--timeout", "5m")
			if err != nil {
				return err
			}

			// Wait for Kyverno CRDs before anything downstream tries to use them.
			// `kubectl wait --for=condition=Established` can fail early on some
			// clusters with transient "no .status.conditions" races right after CRD
			// creation. Poll explicitly and tolerate empty status until established.
			dl.Println("    - Waiting for Kyverno CRDs to be established...")
			kyvernoCRDs := []string{
				"clusterpolicies.kyverno.io",
				"policies.kyverno.io",
			}
			deadline := time.Now().Add(180 * time.Second)
			for _, crd := range kyvernoCRDs {
				for {
					out, checkErr := runExecOutputFn(gCtx,
						"kubectl", "get", "crd", crd,
						"-o", "jsonpath={.status.conditions[?(@.type==\"Established\")].status}",
					)
					if checkErr == nil && strings.Contains(string(out), "True") {
						break
					}
					if time.Now().After(deadline) {
						if checkErr != nil {
							return fmt.Errorf("kyverno CRD %s did not become Established: %w", crd, checkErr)
						}
						return fmt.Errorf("kyverno CRD %s did not become Established before timeout", crd)
					}
					select {
					case <-gCtx.Done():
						return gCtx.Err()
					case <-time.After(2 * time.Second):
					}
				}
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed during Group A (Operators) deployments: %w", err)
	}

	// ── Sequential Readiness Waits for Group A ──
	if deployWithStrimzi {
		if !deployStrimzi {
			// Already skipped above, but ensure progress isn't blocked
		} else {
			if err := dc.deployComponent("strimzi", "strimzi-operator", "name=strimzi-cluster-operator", 5*time.Minute, "Strimzi operator readiness failed"); err != nil {
				return err
			}
		}
	}
	if deployWithCertManager {
		if !deployCertMgr {
			// already handled
		} else {
			if err := dc.deployComponent("cert-manager", "cert-manager", "app.kubernetes.io/instance=cert-manager", 10*time.Minute, "Cert-Manager readiness failed"); err != nil {
				return err
			}
		}
	}
	if deployWithKyverno {
		if !deployKyvernoFlag {
			// already handled
		} else {
			if err := dc.deployComponent("kyverno", "kyverno", "app.kubernetes.io/instance=kyverno", 5*time.Minute, "Kyverno readiness failed"); err != nil {
				return err
			}
		}
	}

	// Bust Kubernetes Discovery Cache so Helm knows about the newly created CRDs.
	// Not ~/.cache/helm: on Linux that is Helm's repository cache, and the
	// kyverno/kyverno upgrade that wires Kyverno into Prometheus reads it.
	dl.Println("    - Refreshing API server schema cache...")
	if home, err := os.UserHomeDir(); err == nil {
		os.RemoveAll(fmt.Sprintf("%s/.kube/cache/discovery", home))
	}

	return nil
}

// restoreCertMgrWebhookPolicy puts failurePolicy: Fail back on the cert-manager
// webhook configs, on a context ctx's cancellation does not reach. When a
// sibling branch of the deploy group failed, ctx was already done, the restore
// patch never ran, and the webhook stayed fail-open for good: later deploys
// skip an installed cert-manager, and cert-manager does not put the policy back.
func restoreCertMgrWebhookPolicy(ctx context.Context, configs []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, kind := range configs {
		if err := runExecFn(ctx, "kubectl", "patch", kind, "cert-manager-webhook", "--type=json", "-p",
			`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]`); err != nil {
			dl.Printf("    ⚠ Could not restore failurePolicy: Fail on %s cert-manager-webhook: %v\n", kind, err)
		}
	}
}

// deployGroupB deploys Group B components (Core Infrastructure) in parallel:
// Kafka, Monitoring, PostgreSQL, followed by sequential readiness waits,
// entity operator wait, Kafka Connect, and CDC connectors.
func deployGroupB(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	connectNS := dc.ns.connect
	jaegerNS := dc.ns.jaeger

	// ---------------------------------------------------------
	// GROUP B: Core Infrastructure (Parallel)
	// ---------------------------------------------------------

	deployKafka := !isHelmReleaseDeployedFn(ctx, dc.primary.Name, kafkaNS)
	if !deployKafka && !isKafkaClusterReady(ctx, kafkaNS, dc.primary.Name) {
		dl.Println("    - Kafka Helm release exists but Kafka CR is not Ready; reconciling release...")
		deployKafka = true
	}
	deployMon := false
	monitoringRejectsNodeExporter := false
	if deployWithMonitoring {
		var err error
		monitoringRejectsNodeExporter, err = monitoringNamespaceRejectsNodeExporter(ctx, jaegerNS)
		if err != nil {
			return err
		}
		deployMon = !releaseKept(ctx, "monitoring", jaegerNS)
		if !deployMon && monitoringRejectsNodeExporter && monitoringNodeExporterDaemonSetScheduled(ctx, jaegerNS) {
			dl.Println("    - Monitoring Helm release exists but node-exporter cannot run under namespace PodSecurity; reconciling release...")
			deployMon = true
		}
	}
	deployPG := false
	if deployWithKafkaConnect {
		deployPG = !isHelmReleaseDeployedFn(ctx, "postgresql", deployDbNS)
	}

	if deployKafka {
		dl.Printf("\n📦 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)
	} else {
		dl.Println(output.Glyphs().Skip + "  Kafka Cluster already deployed. Skipping.")
		dl.FinishComponent("kafka", true)
		dc.advanceStep()
		dl.FinishComponent("kafka-users", true)
		dc.advanceStep()
	}

	if deployWithMonitoring {
		if deployMon {
			dl.Printf("\n📦 Deploying Monitoring (Prometheus + Grafana) (Namespace: %s)...\n", jaegerNS)
		} else {
			dl.Println(output.Glyphs().Skip + "  Monitoring stack already deployed. Skipping.")
			dl.FinishComponent("monitoring", true)
			dc.advanceStep()
		}
	}

	if deployWithKafkaConnect {
		if deployPG {
			dl.Printf("\n📦 Deploying PostgreSQL CDC Database (Namespace: %s)...\n", deployDbNS)
		} else {
			dl.Println(output.Glyphs().Skip + "  PostgreSQL already deployed. Skipping.")
			dl.FinishComponent("postgres", true)
			dc.advanceStep()
		}
	}

	// Monitoring first, on its own. kube-prometheus-stack carries the
	// monitoring.coreos.com CRDs, and every chart below renders its
	// PodMonitor and PrometheusRule only if that API exists when Helm
	// renders it — a release is never revisited when a CRD turns up later.
	// Installed in parallel with Kafka, the CRDs landed seconds after Kafka
	// had rendered without them (see deploy_monitoring.go). Without --wait
	// this costs the time the chart takes to apply, not the minutes its pods
	// take to start; the readiness wait further down is unchanged.
	//
	// chaosAlerts is the monitoring chart's own set of rules: the
	// kafka:chaos:* recording series the chaos board is drawn from.
	if deployWithMonitoring && deployMon {
		// Update chart dependencies (kube-prometheus-stack subchart).
		_ = helmDependency(ctx, "update", chartRootPath("charts/monitoring"))

		monitoringArgs := []string{"upgrade", "--install", "monitoring",
			chartRootPath("charts/monitoring"),
			"-n", jaegerNS, "--create-namespace",
			"-f", dc.chartOverlay(chartRootPath("charts/monitoring")),
			"--set", "kube-prometheus-stack.global.clusterDomain=" + dc.report.Network.ClusterDomain,
			"--set", fmt.Sprintf("chaosAlerts.enabled=%t", deployWithChaos),
			"--timeout", "10m"}
		if monitoringRejectsNodeExporter {
			dl.Println("    - Monitoring namespace enforces baseline/restricted PodSecurity; disabling node-exporter...")
			monitoringArgs = append(monitoringArgs,
				"--set", "kube-prometheus-stack.nodeExporter.enabled=false",
				"--set", "kube-prometheus-stack.prometheus-node-exporter.enabled=false")
		}
		if prometheusOperatorCRDsAvailable(ctx) {
			dl.Println("    - Reusing existing Prometheus Operator CRDs...")
			monitoringArgs = append(monitoringArgs, "--set", "kube-prometheus-stack.crds.enabled=false")
		}
		if err := runHelmFn(ctx, monitoringArgs...); err != nil {
			return fmt.Errorf("failed during Group B (Core Infra) deployments: %w", err)
		}
	}

	g2, g2Ctx := errgroup.WithContext(ctx)

	if deployWithKafkaConnect && deployPG {
		g2.Go(func() error {
			dbNS := deployDbNS

			if err := ensureConfiguredDeployNamespace(g2Ctx, dbNS); err != nil {
				return err
			}

			runHelmFn(g2Ctx, "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami")
			runHelmFn(g2Ctx, "repo", "update", "bitnami")

			if err := runHelmFn(g2Ctx, "upgrade", "--install", "postgresql", "bitnami/postgresql",
				"-n", dbNS, "--create-namespace",
				"--set", "auth.postgresPassword=postgres",
				"--set", "auth.username=debezium",
				"--set", "auth.password=debezium",
				"--set", "auth.database=orders",
				"--set", "primary.extendedConfiguration=wal_level = logical\nmax_wal_senders = 10\nmax_replication_slots = 10",
				"--timeout", "5m"); err != nil {
				return err
			}

			return nil
		})
	}

	// Deploy Kafka
	if deployKafka {
		g2.Go(func() error {

			_ = helmDependency(g2Ctx, "update", chartRootPath(kafkaChartDir))
			// No --wait: Helm only needs to submit the manifests; the Strimzi
			// operator drives reconciliation asynchronously. waitKafkaReady()
			// below is the real readiness gate.
			clusterDomain := dc.resolveClusterDomain()
			kafkaArgs := []string{"upgrade", "--install", dc.primary.Name, chartRootPath(kafkaChartDir), "-n", kafkaNS, "--create-namespace"}

			// values-platform.yaml selects the chart's platform profile: the
			// kates topics, users (kates-backend, kafka-ui, kates-connect,
			// kates-mm2, …) and client NetworkPolicy grants the rest of this
			// deploy relies on. kafka-cluster 1.0 no longer carries them in its
			// defaults.
			kafkaArgs = append(kafkaArgs,
				"-f", dc.valuesFile,
				"-f", chartRootPath("charts/kafka-cluster/values-platform.yaml"),
				"--set", "global.clusterDomain="+clusterDomain,
				"--set", "networkPolicy.clients[0].name=connect",
				"--set", "networkPolicy.clients[0].namespace="+connectNS,
				"--timeout", "10m",
			)
			if dc.isKind {
				kafkaArgs = append(kafkaArgs, "-f", chartRootPath("charts/kafka-cluster/values-kind.yaml"))
			}
			// The resolved versions and the cluster name go last so they win
			// over every values file above (§3.3: derived settings travel
			// with the version).
			if dc.versions != nil {
				kafkaArgs = append(kafkaArgs, dc.versions.kafkaHelmArgs(dc.primary)...)
			}

			if deployHA {
				kafkaArgs = append(kafkaArgs,
					"--set", "kafka.replicas=3",
					"--set", "kafka.config.default\\.replication\\.factor=3",
					"--set", "kafka.config.min\\.insync\\.replicas=2",
					"--set", "zookeeper.replicas=3",
				)
			} else {
				kafkaArgs = append(kafkaArgs,
					"--set", "kafka.replicas=1",
					"--set", "kafka.config.default\\.replication\\.factor=1",
					"--set", "kafka.config.min\\.insync\\.replicas=1",
					"--set", "zookeeper.replicas=1",
				)
			}

			// (Values files are already appended above so these overrides take precedence)

			kafkaArgs = append(kafkaArgs, dc.scrapeArgs("charts/kafka-cluster")...)

			if err := runHelmFn(g2Ctx, kafkaArgs...); err != nil {
				return err
			}

			return nil
		})
	}

	if err := g2.Wait(); err != nil {
		return fmt.Errorf("failed during Group B (Core Infra) deployments: %w", err)
	}

	// ---------------------------------------------------------
	// ── Sequential Readiness Waits for Group B ───────────────
	// ---------------------------------------------------------
	// 1. Kafka Cluster
	if deployKafka {
		if err := dc.deployComponent("kafka", kafkaNS, dc.primary.ReadySelector(), 15*time.Minute, ""); err != nil {
			return fmt.Errorf("kafka readiness failed: %w", err)
		}
	}

	// 2. Monitoring Stack
	if deployWithMonitoring && deployMon {
		if err := dc.deployComponent("monitoring", jaegerNS, "release=monitoring", 10*time.Minute, ""); err != nil {
			return fmt.Errorf("monitoring readiness failed: %w", err)
		}
	}
	if err := wireKyvernoScrape(dc); err != nil {
		return err
	}

	// 2. PostgreSQL
	if deployWithKafkaConnect {
		if deployPG {
			err := dc.deployComponent("postgres", deployDbNS, "app.kubernetes.io/instance=postgresql", 5*time.Minute, "")
			if err != nil {
				dl.Printf("    %s PostgreSQL not ready: %v\n", output.WarningStyle.Render("⚠"), err)
			} else if !isTesting {
				// Grant superuser and replication to debezium after DB is ready
				for i := 0; i < 5; i++ {
					_, err := runExecCombinedFn(ctx, "kubectl", "exec", "-n", deployDbNS, "postgresql-0", "--",
						"env", "PGPASSWORD=postgres", "psql", "-U", "postgres", "-c",
						"ALTER ROLE debezium SUPERUSER REPLICATION;")
					if err == nil {
						break
					}
					time.Sleep(2 * time.Second)
				}
			}
		}
	}

	// Kafka users and topics are managed declaratively by the Helm chart
	// (charts/kafka-cluster templates/users.yaml + templates/topics.yaml).
	// Applying them again via raw manifests causes MethodNotAllowed (HTTP 405)
	// field-ownership conflicts with Helm's server-side apply.
	dl.Println("    - Kafka users and topics managed by Helm chart")

	if !isTesting {
		dl.Println("    - Waiting for Entity Operator to start...")
		eoDeadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(eoDeadline) {
			eoOut, _ := runExecOutputFn(ctx,
				"kubectl", "get", "pods", "-n", kafkaNS,
				"-l", "app.kubernetes.io/name=entity-operator",
				"--no-headers",
				"-o", "custom-columns=PHASE:.status.phase",
			)
			if strings.Contains(string(eoOut), "Running") {
				dl.Printf("    %s Entity Operator running\n", output.AccentStyle.Render("✔"))
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		dl.StartComponent("kafka-users", 8*time.Minute)
		err := waitKafkaUsersReadySilent(ctx, kafkaNS, 8*time.Minute)
		if err != nil {
			dl.Printf("    %s KafkaUsers not all ready after 5m — downstream deploys will retry secret lookup\n", output.WarningStyle.Render("⚠"))
		}
	}

	// 3. Kafka Connect (separate chart — deployed in connectNS)
	if deployWithKafkaConnect {
		if err := deployKafkaConnectStack(dc); err != nil {
			return err
		}
	}

	return nil
}

// deployKafkaConnectStack handles all Kafka Connect related deployments:
// namespace setup, secret copying, Helm install, readiness wait, and CDC connectors.
func deployKafkaConnectStack(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	connectNS := dc.ns.connect

	// Ensure connect namespace exists (idempotent — won't fail if already present)
	dl.Println("    - Ensuring connect namespace exists...")
	if err := ensureConfiguredDeployNamespace(ctx, connectNS); err != nil {
		return err
	}

	connectDomain := dc.resolveClusterDomain()
	bootstrap := dc.primary.Bootstrap(connectDomain)
	kafkaSvcDomain := ""
	if connectNS != kafkaNS {
		kafkaSvcDomain = fmt.Sprintf("%s.svc.%s", kafkaNS, connectDomain)
	}

	// Copy the kates-connect credentials from the kafka namespace to the connect
	// namespace (cross-namespace). The worker's exporter rules used to be copied
	// here too, from kafka-cluster's metrics ConfigMap; connect-cluster 2.0 renders
	// its own in its own namespace and points metricsConfig at that, so there is
	// nothing left to copy.
	if connectNS != kafkaNS {
		dl.Println("    - Copying kates-connect credentials to connect namespace...")
		var pwBytes []byte
		var jaasBytes []byte
		secretDeadline := time.Now().Add(2 * time.Minute)
		for {
			pwOut, pwErr := runExecOutputFn(ctx, "kubectl", "get", "secret", "kates-connect",
				"-n", kafkaNS, "-o", "jsonpath={.data.password}")
			if pwErr == nil && len(strings.TrimSpace(string(pwOut))) > 0 {
				pwBytes = bytes.TrimSpace(pwOut)
				jaasOut, jaasErr := runExecOutputFn(ctx, "kubectl", "get", "secret", "kates-connect",
					"-n", kafkaNS, "-o", "jsonpath={.data.sasl\\.jaas\\.config}")
				if jaasErr == nil {
					jaasBytes = bytes.TrimSpace(jaasOut)
				}
				break
			}
			if time.Now().After(secretDeadline) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		if len(pwBytes) > 0 {
			var dataLines strings.Builder
			dataLines.WriteString(fmt.Sprintf("  password: %s\n", string(pwBytes)))
			if len(jaasBytes) > 0 {
				dataLines.WriteString(fmt.Sprintf("  sasl.jaas.config: %s\n", string(jaasBytes)))
			}

			connectSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-connect
  namespace: %s
type: Opaque
data:
%s`, connectNS, dataLines.String())
			runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectSecretYaml)
		} else {
			dl.Printf("    ⚠  Secret 'kates-connect' not found in namespace %s after waiting — KafkaUser may not be ready\n", kafkaNS)
		}
	}

	dl.Println("    - Creating PostgreSQL credentials secret for Kafka Connect...")
	pgSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: connect-pg-credentials
  namespace: %s
type: Opaque
stringData:
  password: debezium
  username: debezium`, connectNS)
	runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, pgSecretYaml)

	connectDeployed := releaseKept(ctx, "connect-cluster", connectNS)
	if connectDeployed && !isTesting {
		needsReconcile, reason := kafkaConnectNeedsReconcile(ctx, connectNS, "connect-cluster", bootstrap, kafkaSvcDomain)
		if needsReconcile {
			dl.Printf("    ⚠  Kafka Connect release exists but %s — upgrading...\n", reason)
			connectDeployed = false
		}
	}
	if !connectDeployed {
		dl.Printf("\n📦 Deploying Kafka Connect (Namespace: %s)...\n", connectNS)

		// connect-cluster 2.0 is built on the kafka-common library, a file://
		// dependency Helm refuses to render until it is built. `update`, not
		// `build`: the git-ignored Chart.lock goes stale whenever kafka-common's
		// version moves, and a file://-only lock cannot drift.
		if err := helmDependency(ctx, "update", "--skip-refresh", chartRootPath("charts/connect-cluster")); err != nil {
			dl.Printf("    ✗ helm dependency update charts/connect-cluster: %v\n", err)
			return err
		}

		registryFQDN := fmt.Sprintf("http://apicurio-apicurio-registry.%s.svc.%s:80/apis/ccompat/v7",
			kafkaNS, connectDomain)

		connectArgs := []string{"upgrade", "--install", "connect-cluster", chartRootPath("charts/connect-cluster"),
			"-n", connectNS, "--create-namespace",
			"--reset-values",
			"-f", dc.chartOverlay(chartRootPath("charts/connect-cluster")),
			"--set-string", "clusterDomain=" + connectDomain,
			"--set", "kafka.namespace=" + kafkaNS,
			"--set-string", "kafka.bootstrapServers=" + bootstrap,
			"--set", "schemaRegistry.enabled=true",
			"--set-string", "extraConfig.schema\\.registry\\.url=" + registryFQDN,
			"--set", "networkPolicy.egress.databases[0].namespace=" + deployDbNS,
			"--set", "networkPolicy.egress.databases[0].port=5432",
			"--set", "networkPolicy.egress.databases[0].podSelector.app\\.kubernetes\\.io/name=postgresql",
			"--timeout", "10m",
		}
		// Connect's spec.version must be inside the operator's window, so it
		// follows the primary's Kafka version rather than the chart's pin.
		if dc.versions != nil {
			connectArgs = append(connectArgs, dc.versions.connectHelmArgs()...)
		}

		// Enable NetworkPolicy on non-Kind clusters (same as kates backend)
		if !dc.isKind {
			connectArgs = append(connectArgs, "--set", "networkPolicy.enabled=true")
		}

		// When Connect is in a different namespace than Kafka, brokers advertise
		// short hostnames (e.g. krafter-brokers-0.krafter-kafka-brokers.kafka.svc)
		// that only resolve within the kafka namespace DNS search domain.
		// Add the kafka namespace to Connect pod DNS search list.
		if kafkaSvcDomain != "" {
			connectArgs = append(connectArgs,
				"--set-string", "dnsConfig.searches[0]="+kafkaSvcDomain,
			)
		}

		if deployHA {
			connectArgs = append(connectArgs, "--set", "replicas=3")
		} else {
			connectArgs = append(connectArgs, "--set", "replicas=1")
		}

		// Monitoring is installed and ready by now (Group B), so the CRDs its
		// PodMonitor needs exist; without --with-monitoring the chart's own
		// API check keeps a bare cluster installable.
		connectArgs = append(connectArgs, dc.scrapeArgs("charts/connect-cluster")...)

		if err := runHelmFn(ctx, connectArgs...); err != nil {
			return err
		}
	} else {
		dl.Println(output.Glyphs().Skip + "  Kafka Connect already deployed. Skipping.")
	}

	dl.StartComponent("kafka-connect", 15*time.Minute)
	if !isTesting {
		if err := waitKafkaConnectReadySilent(ctx, connectNS, "connect-cluster", 15*time.Minute); err != nil {
			dl.FinishComponent("kafka-connect", false)
			return fmt.Errorf("kafka connect failed to become ready: %w", err)
		}
	}
	dl.FinishComponent("kafka-connect", true)
	dc.advanceStep()

	// Deploy Debezium connector
	checkOut, checkErr := runExecCombinedFn(ctx, "kubectl", "get", "kafkaconnector", "debezium-postgres-source", "-n", connectNS, "--no-headers")
	if checkErr != nil || strings.Contains(string(checkOut), "not found") {
		dl.Println("    - Deploying Debezium PostgreSQL CDC connector...")
		connectorYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: debezium-postgres-source
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.postgresql.PostgresConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    database.hostname: postgresql.%s.svc
    database.port: "5432"
    database.user: debezium
    database.password: "${secrets:%s/connect-pg-credentials:password}"
    database.dbname: orders
    topic.prefix: cdc
    schema.include.list: public
    plugin.name: pgoutput
    slot.name: debezium_kates
    heartbeat.interval.ms: "10000"
    # Default heartbeat topic is __debezium-heartbeat.<prefix>, which falls
    # OUTSIDE the kates-connect user's cdc* topic grant. The denial poisons the
    # transactional producer and the connector reports the misleading
    # "Cannot execute transactional method because we are in an error state".
    topic.heartbeat.prefix: cdc-heartbeat
    snapshot.mode: initial
    decimal.handling.mode: double
    tombstones.on.delete: "true"
    schema.history.internal.kafka.bootstrap.servers: %s
    schema.history.internal.kafka.security.protocol: SASL_PLAINTEXT
    schema.history.internal.kafka.sasl.mechanism: SCRAM-SHA-512
    schema.history.internal.kafka.sasl.jaas.config: "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-connect\" password=\"${secrets:%s/kates-connect:password}\";"
    schema.history.internal.kafka.topic: cdc-schema-history`, connectNS, deployDbNS, connectNS, bootstrap, connectNS)
		if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectorYaml); err != nil {
			dl.Printf("    %s Failed to deploy Debezium connector: %v\n", output.WarningStyle.Render("⚠"), err)
			dl.FinishComponent("kafka-connector", false)
		} else {
			dl.Printf("    %s Debezium PostgreSQL CDC connector deployed\n", output.SuccessStyle.Render("✔"))
		}
	} else {
		dl.Printf("    %s Debezium connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
	}

	// Deploy JDBC Sink connector
	sinkCheckOut, sinkCheckErr := runExecCombinedFn(ctx, "kubectl", "get", "kafkaconnector", "jdbc-sink-connector", "-n", connectNS, "--no-headers")
	if sinkCheckErr != nil || strings.Contains(string(sinkCheckOut), "not found") {
		dl.Println("    - Deploying JDBC Sink connector...")
		sinkYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: jdbc-sink-connector
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.jdbc.JdbcSinkConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    # The topic debezium actually produces (and the ACLs actually grant).
    # "test-sink-topic" was a placeholder: unauthorized AND fed by nothing, so
    # the sink failed on ACLs and would have sat idle even without them.
    topics: "cdc.public.demo_orders"
    connection.url: "jdbc:postgresql://postgresql.%s.svc:5432/orders"
    connection.username: "${secrets:%s/connect-pg-credentials:username}"
    connection.password: "${secrets:%s/connect-pg-credentials:password}"
    insert.mode: "insert"
    auto.create: "true"
    auto.evolve: "true"`, connectNS, deployDbNS, connectNS, connectNS)
		if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, sinkYaml); err != nil {
			dl.Printf("    %s Failed to deploy JDBC Sink connector: %v\n", output.WarningStyle.Render("⚠"), err)
		} else {
			dl.Printf("    %s JDBC Sink connector deployed\n", output.SuccessStyle.Render("✔"))
		}
	} else {
		dl.Printf("    %s JDBC Sink connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
	}

	if !isTesting {
		dl.StartComponent("kafka-connector", 10*time.Minute)
		if err := waitConnectorReadySilent(ctx, connectNS, 10*time.Minute); err != nil {
			dl.FinishComponent("kafka-connector", false)
			return fmt.Errorf("Kafka Connectors failed to become ready: %w", err)
		}
		dl.FinishComponent("kafka-connector", true)
	}
	dc.advanceStep()

	return nil
}

// katesChaosArgs puts the Kates API on the noop chaos provider when this
// deploy installs no Litmus and no chaos release is there. Left on its
// litmus-crd default, the API finds no Litmus, logs an ERROR and falls back to
// noop anyway; set to noop, every fault is skipped by design.
func katesChaosArgs(ctx context.Context, chaosNS string) []string {
	if deployWithChaos || isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
		return nil
	}
	return []string{"--set", "chaos.provider=noop"}
}

// deployGroupC deploys Group C components (Apps / Sequential):
// Apicurio Schema Registry, Kates Backend, Kafka UI, and Litmus Chaos.
func deployGroupC(dc *deployContext) error {
	ctx := dc.ctx
	kafkaNS := dc.ns.kafka
	appNS := dc.ns.app
	kafkaUINS := dc.ns.kafkaUI
	chaosNS := dc.ns.chaos

	// ---------------------------------------------------------
	// GROUP C (Apps / Sequential)
	// ---------------------------------------------------------
	// Deploy Schema Registry (if requested)
	if deployWithSchemaRegistry == "apicurio" {
		if !releaseKept(ctx, "apicurio", kafkaNS) {
			dl.Printf("\n📦 Deploying Apicurio Schema Registry (Namespace: %s)...\n", kafkaNS)
			dl.StartComponent("apicurio", 5*time.Minute)
			if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", chartRootPath("charts/apicurio-registry"), "-n", kafkaNS, "--create-namespace", "--timeout", "5m"); err != nil {
				dl.FinishComponent("apicurio", false)
				return err
			}
			dl.FinishComponent("apicurio", true)
			dc.advanceStep()
		} else {
			dl.Println(output.Glyphs().Skip + "  Apicurio already deployed.")
			dl.FinishComponent("apicurio", true)
			dc.advanceStep()
		}
	}

	// Deploy Kates.
	//
	// Reconciled unconditionally, for the same reason as the Strimzi operator
	// in Group A. This used to skip when a Helm release named "kates" existed,
	// which reads "already deployed" off a release record — a record that says
	// a helm install once succeeded, not that anything is running. A release
	// stuck in ImagePullBackOff satisfied it, so `kates deploy` reported the
	// backend as done and left it broken, and no amount of re-running could
	// repair the one component the platform is named after. It also meant a
	// change in the values a release should get (the kind image overlay
	// below, say) never reached an existing install.
	//
	// `helm upgrade --install` converges: same values, no-op; different
	// values, a rollout.
	katesInstalled := isHelmReleaseDeployedFn(ctx, "kates", appNS)
	{
		verb := "Deploying"
		if katesInstalled {
			verb = "Reconciling"
		}
		dl.Printf("\n📦 %s Kates Backend (Namespace: %s)...\n", verb, appNS)
		// Auto-cleanup stale ClusterRole ownership from previous topology switches
		cleanupStaleClusterResource(ctx, "clusterrole", "kates", appNS)
		cleanupStaleClusterResource(ctx, "clusterrolebinding", "kates", appNS)

		if kafkaNS != appNS {
			// Ensure app namespace exists before copying secrets into it.
			if err := ensureConfiguredDeployNamespace(ctx, appNS); err != nil {
				return err
			}

			// The KafkaUser was already waited on in Group B — just read the Secret.
			pwBytes, pwErr := runExecOutputFn(ctx, "kubectl", "get", "secret", "kates-backend",
				"-n", kafkaNS, "-o", "jsonpath={.data.password}")

			if pwErr == nil && len(pwBytes) > 0 {
				dl.Println("    - Copying Kafka SASL credentials to app namespace...")
				secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-backend
  namespace: %s
type: Opaque
data:
  password: %s`, appNS, string(pwBytes))
				runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
			} else {
				dl.Printf("    ⚠  Secret 'kates-backend' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
			}
		}

		katesBootstrap := dc.primary.Bootstrap(dc.report.Network.ClusterDomain)

		// Every install runs the native image. On kind, prefer a real local
		// backend image when one is available on every node; otherwise, and on
		// every other cluster, use the published native image.
		backendArgs := []string{"-f", chartRootPath(publishedNativeValues)}
		if dc.isKind {
			args, err := kindBackendImageArgs(ctx, dc.report.Context)
			if err != nil {
				return err
			}
			backendArgs = args
		} else {
			dl.Println("    - Backend image: published native image (not a kind cluster)")
		}

		if err := cleanupStaleKatesChartDependencies(chartRootPath("charts/kates")); err != nil {
			return err
		}

		katesArgs := []string{"upgrade", "--install", "kates", chartRootPath("charts/kates"),
			"-n", appNS, "--create-namespace"}
		katesArgs = append(katesArgs, dc.katesChartValues()...)
		katesArgs = append(katesArgs, backendArgs...)
		katesArgs = append(katesArgs,
			"--set", "kafka.bootstrapServers="+katesBootstrap,
			"--set", "kafka.topicNamespace="+kafkaNS,
			// Also the namespace and cluster the API's chaos, topology and conformity act on.
			"--set", "networkPolicy.kafka.namespace="+kafkaNS,
			"--set", "kafka.clusterName="+dc.primary.Name,
			"--set", fmt.Sprintf("monitoring.enabled=%t", deployWithMonitoring))
		katesArgs = append(katesArgs, dc.scrapeArgs("charts/kates")...)
		katesArgs = append(katesArgs, dc.prometheusArgs()...)
		katesArgs = append(katesArgs, dc.cdcDatabaseAccessArgs()...)
		katesArgs = append(katesArgs, katesChaosArgs(ctx, chaosNS)...)
		if deployAdminless {
			// No ClusterRole: a Role in the namespace, chaos off.
			katesArgs = append(katesArgs, "--set", "rbac.scope=namespace")
			if len(adminlessConformityMissing) > 0 {
				katesArgs = append(katesArgs, "--set", "rbac.kafkaNamespaceRole=false")
			}
		}
		katesArgs = append(katesArgs, "--timeout", "8m")

		dl.Println("    - Waiting for Kates backend pods to become ready (this may take 2-3 minutes)...")
		if err := runKatesHelmUpgrade(ctx, appNS, katesArgs...); err != nil {
			return err
		}

		if err := dc.deployComponent("kates", appNS, "app.kubernetes.io/instance=kates", 8*time.Minute, ""); err != nil {
			return err
		}
	}

	// Deploy Kafka UI
	if deployWithKafkaUI {
		if !releaseKept(ctx, "kafka-ui", kafkaUINS) {
			dl.Printf("\n💻 Deploying Kafka UI (Namespace: %s)...\n", kafkaUINS)
			dl.StartComponent("kafka-ui", 5*time.Minute)

			// Cross-namespace secret copy (KafkaUser secret lives in Kafka NS)
			if kafkaUINS != kafkaNS {
				if err := ensureConfiguredDeployNamespace(ctx, kafkaUINS); err != nil {
					return err
				}

				dl.Println("    - Copying kafka-ui SASL credentials to UI namespace...")
				pwBytes, pwErr := runExecOutputFn(ctx, "kubectl", "get", "secret", "kafka-ui",
					"-n", kafkaNS, "-o", "jsonpath={.data.password}")
				if pwErr == nil && len(pwBytes) > 0 {
					secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kafka-ui
  namespace: %s
type: Opaque
data:
  password: %s`, kafkaUINS, string(pwBytes))
					runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
				} else {
					dl.Printf("    ⚠  Secret 'kafka-ui' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
				}
			}

			helmArgs := []string{
				"upgrade", "--install", "kafka-ui", chartRootPath("charts/kafka-ui"),
				"-n", kafkaUINS, "--create-namespace",
				// The primary's name, as for MirrorMaker 2 below. This read
				// clusterName, the --cluster-name flag of kates detect, so
				// --kafka-name left kafka-ui on a cluster named krafter.
				"--set", "kafka.clusterName=" + dc.primary.Name,
				"--set", "kafka.namespace=" + kafkaNS,
				"--set-string", "kafka.clusterDomain=" + dc.resolveClusterDomain(),
				"--set", "kafkaUser.enabled=false",
				"--timeout", "5m",
			}
			if overlay := dc.chartOverlay(chartRootPath("charts/kafka-ui")); dc.fileExists(overlay) {
				helmArgs = append(helmArgs, "-f", overlay)
			}
			if err := runHelmFn(ctx, helmArgs...); err != nil {
				dl.FinishComponent("kafka-ui", false)
				return err
			}
			dl.FinishComponent("kafka-ui", true)
			dc.advanceStep()
		} else {
			dl.Println(output.Glyphs().Skip + "  Kafka UI already deployed.")
			dl.FinishComponent("kafka-ui", true)
			dc.advanceStep()
		}
	}

	// Deploy MirrorMaker 2 — the chart's loopback: the primary mirrored into
	// itself under a renamed topic set, which is enough to exercise every
	// MM2 mechanism (connectors, offset syncs, checkpoints, the ACLs of the
	// kates-mm2 user) without a second cluster. Real sources are `kates
	// migrate up --from …`.
	if deployWithMirrorMaker2 {
		mm2NS := dc.ns.mm2
		if !releaseKept(ctx, "mm2", mm2NS) {
			dl.Printf("\n🔁 Deploying MirrorMaker 2 (Namespace: %s)...\n", mm2NS)
			dl.StartComponent("mirror-maker2", 10*time.Minute)

			// The kates-mm2 credential is a KafkaUser of the primary, so its
			// Secret lives in the Kafka namespace; MM2 reads it from its own.
			if mm2NS != kafkaNS {
				if err := ensureConfiguredDeployNamespace(ctx, mm2NS); err != nil {
					return err
				}
				dl.Println("    - Copying kates-mm2 SASL credentials to the MirrorMaker 2 namespace...")
				pwBytes, pwErr := runExecOutputFn(ctx, "kubectl", "get", "secret", "kates-mm2",
					"-n", kafkaNS, "-o", "jsonpath={.data.password}")
				if pwErr == nil && len(pwBytes) > 0 {
					secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-mm2
  namespace: %s
type: Opaque
data:
  password: %s`, mm2NS, string(pwBytes))
					runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
				} else {
					dl.Printf("    ⚠  Secret 'kates-mm2' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
				}
			}

			// The chart depends on the kafka-common library (file://), which a
			// checkout does not carry built; `update` as for connect-cluster.
			if err := helmDependency(ctx, "update", "--skip-refresh", chartRootPath("charts/mirror-maker2")); err != nil {
				dl.Printf("    ✗ helm dependency update charts/mirror-maker2: %v\n", err)
				dl.FinishComponent("mirror-maker2", false)
				return err
			}
			helmArgs := []string{
				"upgrade", "--install", "mm2", chartRootPath("charts/mirror-maker2"),
				"-n", mm2NS, "--create-namespace",
			}
			if overlay := dc.chartOverlay(chartRootPath("charts/mirror-maker2")); dc.fileExists(overlay) {
				helmArgs = append(helmArgs, "-f", overlay)
			}
			// Both ends are the primary; the versions follow the resolved
			// pair so the worker image exists in the operator's window.
			helmArgs = append(helmArgs,
				"--set", "target.clusterName="+dc.primary.Name,
				"--set", "target.namespace="+kafkaNS,
				"--set", "mirrors[0].source.clusterName="+dc.primary.Name,
				"--set", "mirrors[0].source.namespace="+kafkaNS,
			)
			if dc.versions != nil {
				helmArgs = append(helmArgs,
					"--set-string", "version="+dc.versions.KafkaVersion,
					"--set-string", "strimziVersion="+dc.versions.StrimziVersion,
				)
			}
			helmArgs = append(helmArgs, dc.scrapeArgs("charts/mirror-maker2")...)
			helmArgs = append(helmArgs, "--timeout", "10m")
			if err := runHelmFn(ctx, helmArgs...); err != nil {
				dl.FinishComponent("mirror-maker2", false)
				return err
			}
			dl.FinishComponent("mirror-maker2", true)
			dc.advanceStep()
		} else {
			dl.Println(output.Glyphs().Skip + "  MirrorMaker 2 already deployed.")
			dl.FinishComponent("mirror-maker2", true)
			dc.advanceStep()
		}
	}

	// Deploy Chaos
	if deployWithChaos {
		if !isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
			dl.Printf("\n📦 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrole", "litmus", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrolebinding", "litmus", chaosNS)
			_ = helmDependency(ctx, "update", chartRootPath("charts/kates-chaos"))
			dl.Println("    - Waiting for Litmus Chaos pods to become ready (this may take a few minutes)...")
			chaosArgs := []string{"upgrade", "--install", "chaos", chartRootPath("charts/kates-chaos"),
				"-n", chaosNS, "--create-namespace",
				"-f", dc.valuesFile,
				"-f", dc.chartOverlay(chartRootPath("charts/kates-chaos")),
				"--set", "rbac.kafkaNamespace=" + kafkaNS}
			chaosArgs = append(chaosArgs, dc.scrapeArgs("charts/kates-chaos")...)
			chaosArgs = append(chaosArgs, "--timeout", "5m")
			if err := runHelmFn(ctx, chaosArgs...); err != nil {
				return err
			}

			if err := dc.deployComponent("chaos", chaosNS, "app.kubernetes.io/instance=chaos", 5*time.Minute, ""); err != nil {
				return err
			}
		} else {
			dl.Println(output.Glyphs().Skip + "  Litmus Chaos already deployed.")
			dl.FinishComponent("chaos", true)
			dc.advanceStep()
		}
	}

	return nil
}

// updateActiveContextAPIKey stores the key from Secret kates-api-key in the
// active CLI context after a deploy, so the commands that follow work without
// a kates ctx set, and reports what it did.
func updateActiveContextAPIKey(ctx context.Context, appNS string) {
	printDeployKeySync(os.Stdout, syncActiveContextKey(ctx, appNS), appNS)
}

// deployKeyOutcome is what kates deploy did with the key of the active context.
type deployKeyOutcome int

const (
	deployKeyStored       deployKeyOutcome = iota // the Secret's key was stored
	deployKeyPresent                              // the context already held the Secret's key
	deployKeyKept                                 // the context holds a key kates did not store, which stays
	deployKeyNoContext                            // --context or KATES_CONTEXT names a context that does not exist
	deployKeySecretUnread                         // the Secret could not be read
)

// deployKeySync is what syncActiveContextKey did, for printDeployKeySync.
type deployKeySync struct {
	Context   string
	Key       deployKeyOutcome
	SecretErr error // why the Secret could not be read (deployKeySecretUnread)
	SaveErr   error
}

// syncActiveContextKey puts the key from Secret kates-api-key into the active
// context (the current one, or the one --context or KATES_CONTEXT names) by
// the rule kates ports goes by, keyReplaceable: only into a context without a
// key, or in place of the key kates stored there itself.
//
// It used to overwrite whatever key the context held, so a context holding a
// narrower key for another tool (an MCP server, a CI job) was handed the
// shared admin key whenever a deploy finished while it was current. It also
// printed the key's first four characters.
func syncActiveContextKey(ctx context.Context, appNS string) deployKeySync {
	secretKey, err := fetchKatesAPIKey(ctx, appNS)
	if err != nil {
		return deployKeySync{Key: deployKeySecretUnread, SecretErr: err}
	}

	var result deployKeySync
	result.SaveErr = updateConfig(func(cfg *Config) error {
		result.Context = cfg.CurrentContext
		if contextFlag != "" {
			result.Context = contextFlag
		}
		c, ok := cfg.Contexts[result.Context]
		switch {
		case !ok:
			result.Key = deployKeyNoContext
			return errConfigUnchanged
		case c.APIKey == secretKey:
			result.Key = deployKeyPresent
			return errConfigUnchanged
		case !keyReplaceable(c):
			result.Key = deployKeyKept
			return errConfigUnchanged
		}
		storeSecretKey(&c, secretKey)
		cfg.Contexts[result.Context] = c
		result.Key = deployKeyStored
		return nil
	})
	return result
}

// printDeployKeySync reports what syncActiveContextKey did.
func printDeployKeySync(w io.Writer, s deployKeySync, appNS string) {
	okMark := output.SuccessStyle.Render("✓")
	warnMark := output.WarningStyle.Render("⚠")
	secret := fmt.Sprintf("Secret %s (namespace %s)", apiKeySecretName, appNS)

	switch {
	case s.Key == deployKeySecretUnread:
		fmt.Fprintf(w, "    %s No API key from %s: %v\n", warnMark, secret, s.SecretErr)
	case s.SaveErr != nil:
		fmt.Fprintf(w, "    %s Could not store the API key from %s: %v\n", warnMark, secret, s.SaveErr)
	case s.Key == deployKeyStored:
		fmt.Fprintf(w, "    %s API key from %s stored in context %q\n", okMark, secret, s.Context)
	case s.Key == deployKeyPresent:
		fmt.Fprintf(w, "    %s Context %q already holds the API key from %s\n", okMark, s.Context, secret)
	case s.Key == deployKeyKept:
		fmt.Fprintf(w, "    %s Context %q keeps the API key it holds: kates replaces only a key it copied from the Secret itself\n", warnMark, s.Context)
		fmt.Fprintf(w, "      %s\n", output.DimStyle.Render(fmt.Sprintf(
			"To use the Secret's key there: kates ctx set %s --url <url> --api-key <key>. kates ports keeps it in context %q instead.", s.Context, portsContextName)))
	case s.Key == deployKeyNoContext:
		fmt.Fprintf(w, "    %s Context %q does not exist, so the API key from %s was not stored\n", warnMark, s.Context, secret)
	}
	fmt.Fprintln(w)
}

// cdcDatabaseAccessArgs lets the backend read and reach the PostgreSQL that
// the INTEGRATION_CDC test (`kates kafka connect test`) uses: the Secret and
// Service in --db-ns, where --with-kafka-connect installs it. The chart grants
// no Secret and no cross-namespace database egress otherwise. A database an
// earlier run installed counts too, so a later deploy without the flag keeps
// the access. Without either nothing is passed: a Role in a namespace that
// does not exist fails the install.
func (dc *deployContext) cdcDatabaseAccessArgs() []string {
	if !deployWithKafkaConnect && !isHelmReleaseDeployedFn(dc.ctx, "postgresql", deployDbNS) {
		return nil
	}
	return []string{
		"--set", "rbac.cdcSecretNamespaces={" + deployDbNS + "}",
		"--set", "networkPolicy.cdcDatabase.enabled=true",
		"--set", "networkPolicy.cdcDatabase.namespace=" + deployDbNS,
	}
}

func (dc *deployContext) resolveClusterDomain() string {
	domain := dc.report.Network.ClusterDomain
	if domain == "" {
		return "cluster.local"
	}
	return domain
}

func (dc *deployContext) deployComponent(id, namespace, selector string, timeout time.Duration, errMsg string) error {
	dl.StartComponent(id, timeout)
	if !isTesting {
		if err := waitComponentReadySilent(dc.ctx, namespace, selector, timeout); err != nil {
			dl.FinishComponent(id, false)
			if errMsg != "" {
				output.Error(fmt.Sprintf("%s: %v", errMsg, err))
			}
			return err
		}
	}
	dl.FinishComponent(id, true)
	dc.advanceStep()
	return nil
}

// printConnectorDiagnostics surfaces WHY connectors are not ready: the
// NotReady condition message and the first line of each failed task's trace.
// Without this, a readiness timeout reported nothing and the real cause
// (an ACL denial, a bad topic) had to be dug out of the CRs by hand.
func printConnectorDiagnostics(ctx context.Context, namespace string) {
	out, err := runExecOutputFn(ctx, "kubectl", "get", "kafkaconnector", "-n", namespace,
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.status.conditions[0].type}{"\t"}{.status.conditions[0].message}{"\t"}{.status.connectorStatus.tasks[0].trace}{"\n"}{end}`)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 || parts[1] == "Ready" {
			continue
		}
		dl.Printf("    %s %s: %s — %s\n", output.ErrorStyle.Render("✖"), parts[0], parts[1], parts[2])
		if len(parts) == 4 && parts[3] != "" {
			// The root cause is usually the LAST "Caused by" in the trace.
			trace := parts[3]
			cause := trace
			if i := strings.LastIndex(trace, "Caused by: "); i >= 0 {
				cause = trace[i+len("Caused by: "):]
			}
			if nl := strings.IndexByte(cause, '\n'); nl >= 0 {
				cause = cause[:nl]
			}
			dl.Printf("      %s\n", output.DimStyle.Render(cause))
		}
	}
}
