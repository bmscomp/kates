package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	deployStatusInteractive bool
	deployStatusOutput      string
)

var deployStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of deployed components and their health",
	RunE:  runDeployStatus,
}

func init() {
	// Note: We use the same package-level flag variables defined in deploy.go
	// However, cobra commands share flags if not careful. Since these are package vars,
	// if deployStatusCmd adds the same flags, they overwrite the pointers.
	// We'll reuse the pointers. It is safe since they are only accessed during RunE.

	deployStatusCmd.Flags().StringVar(&deployTopology, "topology", "isolated", "Deployment topology: 'isolated' (separate namespaces) or 'single' (one namespace)")
	deployStatusCmd.Flags().StringVar(&deployNamespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	deployStatusCmd.Flags().StringVar(&deployKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployDbNS, "db-ns", "database", "Namespace for PostgreSQL Database when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployAppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployMonitoringNS, "monitoring-ns", "monitoring", "Namespace for monitoring components when topology is 'isolated'")
	deployStatusCmd.Flags().StringVar(&deployKafkaUINS, "ui-ns", "kafka", "Namespace for Kafka UI when topology is 'isolated'")

	deployStatusCmd.Flags().BoolVarP(&deployStatusInteractive, "interactive", "i", false, "Enable interactive Bubble Tea status loader")
	deployStatusCmd.Flags().StringVarP(&deployStatusOutput, "output", "o", "table", "Output format: table or json")

	deployCmd.AddCommand(deployStatusCmd)
}

type ComponentStatus struct {
	Group      string      `json:"group"`
	Icon       string      `json:"icon"`
	Name       string      `json:"name"`
	Release    string      `json:"release"`
	Namespace  string      `json:"namespace"`
	HelmStatus string      `json:"helm_status"`
	Health     string      `json:"health"`
	Details    string      `json:"details"`
	RawDetails interface{} `json:"raw_details,omitempty"`
}

// ── JSON Status Parsing Structs ──

type PodDetail struct {
	Name       string            `json:"name"`
	Phase      string            `json:"phase"`
	Containers []ContainerDetail `json:"containers"`
}

type ContainerDetail struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restart_count"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
}

type KafkaDetail struct {
	Conditions []ConditionDetail `json:"conditions"`
}

type ConditionDetail struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type WorkloadDetail struct {
	Replicas        int `json:"replicas"`
	ReadyReplicas   int `json:"ready_replicas"`
	UpdatedReplicas int `json:"updated_replicas"`
}

// Kubernetes JSON structures for parsing
type k8sPodList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				Ready        bool   `json:"ready"`
				RestartCount int    `json:"restartCount"`
				State        struct {
					Waiting *struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"waiting"`
					Running *struct {
						StartedAt string `json:"startedAt"`
					} `json:"running"`
					Terminated *struct {
						Reason   string `json:"reason"`
						Message  string `json:"message"`
						ExitCode int    `json:"exitCode"`
					} `json:"terminated"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

type k8sCustomResource struct {
	Status struct {
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

type k8sWorkload struct {
	Spec struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		Replicas        int `json:"replicas"`
		ReadyReplicas   int `json:"readyReplicas"`
		UpdatedReplicas int `json:"updatedReplicas"`
	} `json:"status"`
}

type helmStatusRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

type componentSpec struct {
	Group     string
	Icon      string
	Name      string
	Release   string
	Namespace string
	Kind      string
	Resource  string
}

func runDeployStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	var kafkaNS, connectNS, appNS, chaosNS, jaegerNS, dbNS, kafkaUINS string
	if deployTopology == "single" {
		kafkaNS, connectNS, appNS, chaosNS, jaegerNS, dbNS, kafkaUINS = deployNamespace, deployNamespace, deployNamespace, deployNamespace, deployNamespace, deployNamespace, deployNamespace
	} else {
		kafkaNS, connectNS, appNS, chaosNS, jaegerNS, dbNS, kafkaUINS = deployKafkaNS, deployConnectNS, deployAppNS, deployChaosNS, deployMonitoringNS, deployDbNS, deployKafkaUINS
	}

	helmIndex := loadHelmReleaseIndex(ctx)

	components := []componentSpec{
		{"A", "☸️", "Strimzi Operator", "strimzi-operator", "strimzi-operator", "pod", "-l name=strimzi-cluster-operator"},
		{"A", "🔐", "Cert-Manager", "cert-manager", "cert-manager", "pod", "-l app.kubernetes.io/instance=cert-manager"},
		{"A", "🛡️", "Kyverno", "kyverno", "kyverno", "pod", "-l app.kubernetes.io/instance=kyverno"},
		{"B", "📨", "Kafka (krafter)", "krafter", kafkaNS, "kafkas.kafka.strimzi.io", "krafter"},
		{"B", "🐘", "PostgreSQL (CDC)", "postgresql", dbNS, "statefulset", "postgresql"},
		{"B", "🔗", "Kafka Connect", "connect-cluster", connectNS, "kafkaconnects.kafka.strimzi.io", "connect-cluster"},
		{"B", "📊", "Monitoring Stack", "monitoring", jaegerNS, "pod", "-l release=monitoring"},
		{"C", "📋", "Apicurio Registry", "apicurio", kafkaNS, "pod", "-l app.kubernetes.io/instance=apicurio"},
		{"C", "📦", "Kates Backend", "kates", appNS, "pod", "-l app.kubernetes.io/instance=kates"},
		{"C", "🖥️", "Kafka UI", "kafka-ui", kafkaUINS, "pod", "-l app.kubernetes.io/name=kafka-ui"},
		{"C", "🧪", "Litmus Chaos", "chaos", chaosNS, "pod", "-l app.kubernetes.io/instance=chaos"},
	}

	// Route based on flags
	if deployStatusOutput == "json" {
		statuses, err := runJSONStatus(ctx, components)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal status JSON: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if deployStatusInteractive {
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			statuses, err := runJSONStatus(ctx, components)
			if err != nil {
				return err
			}
			RenderStatusDashboard(statuses)
			return nil
		}

		statuses, err := runInteractiveStatus(ctx, components)
		if err != nil {
			return err
		}
		RenderStatusDashboard(statuses)
		return nil
	}

	// Default flow: Existing sequential implementation (re-uses existing getHelmStatus / getHealthStatus)
	var statuses []ComponentStatus
	action := func() {
		for _, c := range components {
			st := ComponentStatus{
				Group:     c.Group,
				Icon:      c.Icon,
				Name:      c.Name,
				Release:   c.Release,
				Namespace: c.Namespace,
			}
			st.HelmStatus, st.Namespace = getHelmStatusWithNamespace(ctx, helmIndex, c.Release, c.Namespace)
			if st.HelmStatus == "deployed" {
				st.Health, st.Details = getHealthStatus(ctx, c.Kind, c.Resource, st.Namespace)
			} else {
				st.Health = "N/A"
				st.Details = "Not deployed"
			}
			statuses = append(statuses, st)
		}
	}

	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render("Gathering component status... this may take a few moments."))
	action()

	RenderStatusDashboard(statuses)
	return nil
}

// ── V1 Existing Logic (Retained for default sequential flow) ──

func loadHelmReleaseIndex(ctx context.Context) map[string][]helmStatusRelease {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, "helm", "list", "-A", "-o", "json")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}

	var releases []helmStatusRelease
	if err := json.Unmarshal(out, &releases); err != nil {
		return nil
	}

	index := make(map[string][]helmStatusRelease, len(releases))
	for _, rel := range releases {
		index[rel.Name] = append(index[rel.Name], rel)
	}
	return index
}

func lookupHelmRelease(index map[string][]helmStatusRelease, release, namespace string) (status, resolvedNamespace string, ok bool) {
	if index == nil {
		return "", namespace, false
	}

	releases, exists := index[release]
	if !exists || len(releases) == 0 {
		return "", namespace, false
	}

	for _, rel := range releases {
		if rel.Namespace == namespace {
			return strings.ToLower(rel.Status), namespace, true
		}
	}

	// Fallback: if a release name is unique cluster-wide, trust that namespace.
	if len(releases) == 1 {
		return strings.ToLower(releases[0].Status), releases[0].Namespace, true
	}

	return "", namespace, false
}

func getHelmStatusWithNamespace(ctx context.Context, index map[string][]helmStatusRelease, release, namespace string) (status, resolvedNamespace string) {
	if st, ns, ok := lookupHelmRelease(index, release, namespace); ok {
		return st, ns
	}
	return getHelmStatus(ctx, release, namespace), namespace
}

func getHelmStatus(ctx context.Context, release, namespace string) string {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "helm", "status", release, "-n", namespace)
	if err := cmd.Run(); err == nil {
		return "deployed"
	}
	return "missing"
}

func getHealthStatus(ctx context.Context, kind, resource, namespace string) (string, string) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if kind == "pod" {
		args := append([]string{"get", "pods", "-n", namespace}, strings.Split(resource, " ")...)
		args = append(args, "--no-headers")
		cmd := exec.CommandContext(checkCtx, "kubectl", args...)
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			return "Unknown", "No pods found"
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		running := 0
		for _, l := range lines {
			if strings.Contains(l, "Running") || strings.Contains(l, "Completed") {
				running++
			}
		}
		if running == len(lines) {
			return "Healthy", fmt.Sprintf("%d/%d pods running", running, len(lines))
		}
		return "Degraded", fmt.Sprintf("%d/%d pods running", running, len(lines))
	} else if kind == "kafkas.kafka.strimzi.io" || kind == "kafkaconnects.kafka.strimzi.io" {
		cmd := exec.CommandContext(checkCtx, "kubectl", "get", kind, resource, "-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			return "Unknown", "Error: " + errMsg
		}
		status := strings.TrimSpace(string(out))
		if status == "True" {
			return "Healthy", kind + " CRD is Ready"
		}
		return "Degraded", kind + " CRD not ready"
	} else if kind == "statefulset" || kind == "deployment" {
		cmd := exec.CommandContext(checkCtx, "kubectl", "get", kind, resource, "-n", namespace, "-o", "jsonpath={.status.readyReplicas}/{.status.replicas}")
		out, err := cmd.Output()
		if err != nil {
			return "Unknown", "Resource not found"
		}
		val := string(out)
		if val == "" || val == "/" {
			return "Degraded", "0/0 replicas ready"
		}
		parts := strings.Split(val, "/")
		if len(parts) == 2 && parts[0] == parts[1] {
			return "Healthy", val + " replicas ready"
		}
		return "Degraded", val + " replicas ready"
	}

	return "Unknown", ""
}

// ── V2 Enhanced Logic (Used in Interactive and JSON output modes) ──

func getHealthStatusV2(ctx context.Context, kind, resource, namespace string) (string, string, interface{}) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if kind == "pod" {
		args := append([]string{"get", "pods", "-n", namespace}, strings.Split(resource, " ")...)
		args = append(args, "-o", "json")
		cmd := exec.CommandContext(checkCtx, "kubectl", args...)
		out, err := cmd.Output()
		if err != nil {
			return "Unknown", "Failed to query pods: " + err.Error(), nil
		}
		if len(out) == 0 {
			return "Unknown", "No pods found", nil
		}
		health, details, raw := parsePodListHealth(out)
		return health, details, raw
	} else if kind == "kafka" || kind == "kafkaconnect" {
		cmd := exec.CommandContext(checkCtx, "kubectl", "get", kind, resource, "-n", namespace, "-o", "json")
		out, err := cmd.Output()
		if err != nil {
			return "Unknown", "Resource not found: " + err.Error(), nil
		}
		health, details, raw := parseKafkaHealth(out, kind)
		return health, details, raw
	} else if kind == "statefulset" || kind == "deployment" {
		cmd := exec.CommandContext(checkCtx, "kubectl", "get", kind, resource, "-n", namespace, "-o", "json")
		out, err := cmd.Output()
		if err != nil {
			return "Unknown", "Resource not found: " + err.Error(), nil
		}
		health, details, raw := parseWorkloadHealth(out)
		return health, details, raw
	}

	return "Unknown", "", nil
}

// ── JSON Parsers ──

func parsePodListHealth(data []byte) (string, string, []PodDetail) {
	var list k8sPodList
	if err := json.Unmarshal(data, &list); err != nil {
		return "Unknown", "Failed to parse pod JSON: " + err.Error(), nil
	}
	if len(list.Items) == 0 {
		return "Degraded", "No pods found", nil
	}

	var pods []PodDetail
	healthyPods := 0
	var failureMessages []string

	for _, item := range list.Items {
		podName := item.Metadata.Name
		podPhase := item.Status.Phase

		podDetail := PodDetail{
			Name:  podName,
			Phase: podPhase,
		}

		podHealthy := true
		if podPhase != "Running" && podPhase != "Succeeded" {
			podHealthy = false
			failureMessages = append(failureMessages, fmt.Sprintf("Pod %s is %s", podName, podPhase))
		}

		for _, cs := range item.Status.ContainerStatuses {
			stateStr := "Unknown"
			var reason, msg string
			if cs.State.Waiting != nil {
				stateStr = "Waiting"
				reason = cs.State.Waiting.Reason
				msg = cs.State.Waiting.Message
				if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					podHealthy = false
					failureMessages = append(failureMessages, fmt.Sprintf("Pod %s: container %s in %s", podName, cs.Name, reason))
				}
			} else if cs.State.Running != nil {
				stateStr = "Running"
			} else if cs.State.Terminated != nil {
				stateStr = "Terminated"
				reason = cs.State.Terminated.Reason
				msg = cs.State.Terminated.Message
				if cs.State.Terminated.ExitCode != 0 {
					podHealthy = false
					failureMessages = append(failureMessages, fmt.Sprintf("Pod %s: container %s terminated with exit code %d", podName, cs.Name, cs.State.Terminated.ExitCode))
				}
			}

			if !cs.Ready && podPhase == "Running" {
				podHealthy = false
				if reason != "" {
					failureMessages = append(failureMessages, fmt.Sprintf("Pod %s: container %s not ready (%s)", podName, cs.Name, reason))
				} else {
					failureMessages = append(failureMessages, fmt.Sprintf("Pod %s: container %s not ready", podName, cs.Name))
				}
			}

			podDetail.Containers = append(podDetail.Containers, ContainerDetail{
				Name:         cs.Name,
				Ready:        cs.Ready,
				RestartCount: cs.RestartCount,
				State:        stateStr,
				Reason:       reason,
				Message:      msg,
			})
		}

		pods = append(pods, podDetail)
		if podHealthy {
			healthyPods++
		}
	}

	details := fmt.Sprintf("%d/%d pods running & ready", healthyPods, len(list.Items))
	if healthyPods < len(list.Items) && len(failureMessages) > 0 {
		details = failureMessages[0]
	}

	health := "Healthy"
	if healthyPods == 0 || healthyPods < len(list.Items) {
		health = "Degraded"
	}

	return health, details, pods
}

func parseKafkaHealth(data []byte, kind string) (string, string, KafkaDetail) {
	var cr k8sCustomResource
	if err := json.Unmarshal(data, &cr); err != nil {
		return "Unknown", "Failed to parse CRD JSON: " + err.Error(), KafkaDetail{}
	}

	var conditions []ConditionDetail
	for _, cond := range cr.Status.Conditions {
		conditions = append(conditions, ConditionDetail{
			Type:    cond.Type,
			Status:  cond.Status,
			Reason:  cond.Reason,
			Message: cond.Message,
		})
	}

	detail := KafkaDetail{Conditions: conditions}

	for _, cond := range cr.Status.Conditions {
		if cond.Type == "Ready" {
			if cond.Status == "True" {
				return "Healthy", fmt.Sprintf("%s CRD is Ready", kind), detail
			}
			details := cond.Message
			if details == "" {
				details = cond.Reason
			}
			if details == "" {
				details = fmt.Sprintf("%s CRD not ready", kind)
			}
			return "Degraded", details, detail
		}
	}

	return "Degraded", fmt.Sprintf("%s CRD missing Ready condition", kind), detail
}

func parseWorkloadHealth(data []byte) (string, string, WorkloadDetail) {
	var wl k8sWorkload
	if err := json.Unmarshal(data, &wl); err != nil {
		return "Unknown", "Failed to parse workload JSON: " + err.Error(), WorkloadDetail{}
	}

	specReplicas := 1
	if wl.Spec.Replicas != nil {
		specReplicas = *wl.Spec.Replicas
	}

	detail := WorkloadDetail{
		Replicas:        wl.Status.Replicas,
		ReadyReplicas:   wl.Status.ReadyReplicas,
		UpdatedReplicas: wl.Status.UpdatedReplicas,
	}

	details := fmt.Sprintf("%d/%d replicas ready", wl.Status.ReadyReplicas, specReplicas)
	if wl.Status.ReadyReplicas == specReplicas && specReplicas > 0 {
		return "Healthy", details, detail
	}

	return "Degraded", details, detail
}

// ── Parallel Runner for JSON Output ──

func runJSONStatus(ctx context.Context, components []componentSpec) ([]ComponentStatus, error) {
	var wg sync.WaitGroup
	statuses := make([]ComponentStatus, len(components))
	helmIndex := loadHelmReleaseIndex(ctx)

	for i, c := range components {
		wg.Add(1)
		go func(idx int, comp componentSpec) {
			defer wg.Done()
			st := ComponentStatus{
				Group:     comp.Group,
				Icon:      comp.Icon,
				Name:      comp.Name,
				Release:   comp.Release,
				Namespace: comp.Namespace,
			}
			st.HelmStatus, st.Namespace = getHelmStatusWithNamespace(ctx, helmIndex, comp.Release, comp.Namespace)
			if st.HelmStatus == "deployed" {
				st.Health, st.Details, st.RawDetails = getHealthStatusV2(ctx, comp.Kind, comp.Resource, st.Namespace)
			} else {
				st.Health = "N/A"
				st.Details = "Not deployed"
			}
			statuses[idx] = st
		}(i, c)
	}

	wg.Wait()
	return statuses, nil
}

// ── Interactive Bubble Tea Status TUI Loader ──

type compStatusUpdateMsg struct {
	index  int
	status ComponentStatus
}

type interactiveStatusModel struct {
	components []componentSpec
	statuses   []ComponentStatus
	checking []bool
	done     []bool
	spinner  spinner.Model
	ctx      context.Context
	cancel   context.CancelFunc
}

func (m interactiveStatusModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m interactiveStatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case compStatusUpdateMsg:
		m.statuses[msg.index] = msg.status
		m.done[msg.index] = true
		m.checking[msg.index] = false

		allDone := true
		for _, d := range m.done {
			if !d {
				allDone = false
				break
			}
		}
		if allDone {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m interactiveStatusModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(clrAccent).
		Padding(0, 1).
		Render(" ⎈ Gathering Kates Deployment Status "))
	b.WriteString("\n")

	prevGroup := ""
	for i, c := range m.components {
		if c.Group != prevGroup {
			b.WriteString(fmt.Sprintf("\n  %s\n", lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render(componentGroupNames[c.Group])))
			prevGroup = c.Group
		}

		iconStr := c.Icon
		if visualWidth(iconStr) == 1 {
			iconStr += " "
		}
		compName := iconStr + " " + c.Name
		nameStyle := lipgloss.NewStyle().Foreground(clrText)
		var statusStr string

		if m.done[i] {
			st := m.statuses[i]
			healthStyle := lipgloss.NewStyle().Foreground(clrDim)
			if st.Health == "Healthy" {
				healthStyle = lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
			} else if st.Health == "Degraded" {
				healthStyle = lipgloss.NewStyle().Foreground(clrRed).Bold(true)
			} else if st.Health == "Unknown" {
				healthStyle = lipgloss.NewStyle().Foreground(clrOrange).Bold(true)
			}
			statusStr = healthStyle.Render(fmt.Sprintf("[%s]", st.Health))
		} else if m.checking[i] {
			statusStr = lipgloss.NewStyle().Foreground(clrAccent).Render(fmt.Sprintf("[%s Checking]", m.spinner.View()))
		} else {
			statusStr = lipgloss.NewStyle().Foreground(clrDim).Render("[◯ Pending]")
		}

		pad := 30 - (2 + 1 + visualWidth(c.Name))
		if pad < 1 {
			pad = 1
		}
		b.WriteString(fmt.Sprintf("    %s%s%s\n", nameStyle.Render(compName), strings.Repeat(" ", pad), statusStr))
	}

	b.WriteString("\n")
	return b.String()
}

func runInteractiveStatus(ctx context.Context, components []componentSpec) ([]ComponentStatus, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	helmIndex := loadHelmReleaseIndex(ctx)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(clrAccent)

	m := interactiveStatusModel{
		components: components,
		statuses:   make([]ComponentStatus, len(components)),
		checking:   make([]bool, len(components)),
		done:       make([]bool, len(components)),
		spinner:    s,
		ctx:        ctx,
		cancel:     cancel,
	}

	p := tea.NewProgram(m)

	// Launch checks concurrently
	for i, c := range components {
		m.checking[i] = true
		go func(idx int, comp componentSpec) {
			st := ComponentStatus{
				Group:     comp.Group,
				Icon:      comp.Icon,
				Name:      comp.Name,
				Release:   comp.Release,
				Namespace: comp.Namespace,
			}
			st.HelmStatus, st.Namespace = getHelmStatusWithNamespace(ctx, helmIndex, comp.Release, comp.Namespace)
			if st.HelmStatus == "deployed" {
				st.Health, st.Details, st.RawDetails = getHealthStatusV2(ctx, comp.Kind, comp.Resource, st.Namespace)
			} else {
				st.Health = "N/A"
				st.Details = "Not deployed"
			}
			p.Send(compStatusUpdateMsg{index: idx, status: st})
		}(i, c)
	}

	resModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	finalModel := resModel.(interactiveStatusModel)
	return finalModel.statuses, nil
}

func RenderStatusDashboard(statuses []ComponentStatus) {
	fmt.Println()

	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(clrAccent).
		Padding(0, 1).
		Render(" ⎈ Kates Deployment Status ")
	fmt.Println(banner)

	groups := map[string][]ComponentStatus{"A": {}, "B": {}, "C": {}}
	for _, s := range statuses {
		groups[s.Group] = append(groups[s.Group], s)
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(clrCyan)
	termW := 105

	sepLine := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("─", termW))

	colHdrName := lipgloss.NewStyle().Width(25).Render("COMPONENT")
	colHdrNs := lipgloss.NewStyle().Width(18).Render("NAMESPACE")
	colHdrHelm := lipgloss.NewStyle().Width(12).Render("HELM")
	colHdrHealth := lipgloss.NewStyle().Width(12).Render("HEALTH")
	colHdrDetails := lipgloss.NewStyle().Render("DETAILS")
	colHeaders := lipgloss.NewStyle().Bold(true).Foreground(clrDim).Render(fmt.Sprintf("  %s%s%s%s%s", colHdrName, colHdrNs, colHdrHelm, colHdrHealth, colHdrDetails))

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(headerStyle.Render(fmt.Sprintf("  Group %s — %s", g, componentGroupNames[g])))
		fmt.Println(colHeaders)
		fmt.Println("  " + sepLine)
		for _, s := range groups[g] {
			printStatusRow(s)
		}
	}
	fmt.Println()
}

func printStatusRow(s ComponentStatus) {
	iconStr := s.Icon
	if visualWidth(iconStr) == 1 {
		iconStr += " "
	}
	nameStr := iconStr + " " + s.Name
	// Emoji icons render as 2 cells in terminals, but runewidth often
	// miscounts them (e.g. ☸️ with VS16 reports width 1 instead of 2).
	// Use a fixed 2-cell icon slot + 1 space + text width for padding.
	nameLen := 2 + 1 + visualWidth(s.Name)
	namePad := 25 - nameLen
	if namePad < 1 {
		namePad = 1
	}
	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(nameStr) + strings.Repeat(" ", namePad)

	nsPad := 18 - visualWidth(s.Namespace)
	if nsPad < 1 {
		nsPad = 1
	}
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(s.Namespace) + strings.Repeat(" ", nsPad)

	helmStyle := lipgloss.NewStyle().Foreground(clrDim)
	if s.HelmStatus == "deployed" {
		helmStyle = lipgloss.NewStyle().Foreground(clrGreen)
	}
	helmPad := 12 - visualWidth(s.HelmStatus)
	if helmPad < 1 {
		helmPad = 1
	}
	helmCol := helmStyle.Render(s.HelmStatus) + strings.Repeat(" ", helmPad)

	healthStyle := lipgloss.NewStyle().Foreground(clrDim)
	if s.Health == "Healthy" {
		healthStyle = lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	} else if s.Health == "Degraded" {
		healthStyle = lipgloss.NewStyle().Foreground(clrRed).Bold(true)
	} else if s.Health == "Unknown" {
		healthStyle = lipgloss.NewStyle().Foreground(clrOrange).Bold(true)
	}
	healthPad := 12 - visualWidth(s.Health)
	if healthPad < 1 {
		healthPad = 1
	}
	healthCol := healthStyle.Render(s.Health) + strings.Repeat(" ", healthPad)

	detailsCol := lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render(s.Details)

	fmt.Printf("  %s%s%s%s%s\n", nameCol, nsCol, helmCol, healthCol, detailsCol)
}
