package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/components"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all Kates-managed resources and namespaces",
	Long: `Tears down the entire Kates stack by uninstalling all Helm releases
and deleting all managed namespaces. This is the inverse of 'kates deploy'.

It works on kubectl's current context, which it names before asking, and
sends every kubectl and helm call to that context by name. It lists what it
will remove and asks first; without a terminal, pass --yes. Only after that
does it stop the kubectl port-forwards into the namespaces it deletes.`,
	Example: `  kates clean         # list what goes, then ask
  kates clean --yes   # no prompt, for scripts (--force does the same)`,
	RunE: runClean,
}

var (
	cleanForce        bool
	cleanVerbose      bool
	cleanTopology     string
	cleanNamespace    string
	cleanKafkaNS      string
	cleanConnectNS    string
	cleanDbNS         string
	cleanMM2NS        string
	cleanAppNS        string
	cleanChaosNS      string
	cleanMonitoringNS string
	cleanKafkaUINS    string
)

func init() {
	cleanCmd.Flags().BoolVarP(&cleanForce, "yes", "y", false, "Skip the confirmation prompt")
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "Same as --yes")
	cleanCmd.Flags().BoolVarP(&cleanVerbose, "verbose", "v", false, "Show full command output during cleanup")
	// The same scope flags as deploy (deploy_flags.go), into clean's own
	// variables; an empty --topology cleans both.
	addDeployScopeFlags(cleanCmd,
		scopeInto(deployScopeVars{
			Topology: &cleanTopology, Namespace: &cleanNamespace,
			KafkaNS: &cleanKafkaNS, ConnectNS: &cleanConnectNS, DbNS: &cleanDbNS,
			AppNS: &cleanAppNS, ChaosNS: &cleanChaosNS, MonitoringNS: &cleanMonitoringNS,
			KafkaUINS: &cleanKafkaUINS, MM2NS: &cleanMM2NS,
		}),
		scopeTopologyDefault("", "Topology to clean: 'isolated' or 'single'. If empty, cleans both."))
	rootCmd.AddCommand(cleanCmd)
}

// cleanPlacements are the placements clean tears down: the single topology's,
// the isolated one's, or both when --topology is empty.
func cleanPlacements() []components.Placement {
	var placements []components.Placement
	if cleanTopology == "single" || cleanTopology == "" {
		placements = append(placements, components.SinglePlacement(cleanNamespace, cleanNamespace, deployKafkaName))
	}
	if cleanTopology == "isolated" || cleanTopology == "" {
		placements = append(placements, components.Placement{
			Kafka: cleanKafkaNS, Connect: cleanConnectNS, Database: cleanDbNS, App: cleanAppNS,
			Chaos: cleanChaosNS, Monitoring: cleanMonitoringNS, KafkaUI: cleanKafkaUINS, MM2: cleanMM2NS,
			KafkaName: deployKafkaName,
		})
	}
	return placements
}

// cleanTargets is what the component table says the stack occupies under the
// placements: every release (operators last, listed once), and every
// namespace (operators' own last, listed once).
func cleanTargets(placements []components.Placement) (releases []components.Release, namespaces []string) {
	var operators []components.Release
	for _, p := range placements {
		for _, r := range components.Releases(p) {
			switch c := releaseComponent(r.Name, p); {
			case c.Operator && !slices.Contains(operators, r):
				operators = append(operators, r)
			case !c.Operator && !slices.Contains(releases, r):
				releases = append(releases, r)
			}
		}
		for _, ns := range components.Namespaces(p) {
			if !slices.Contains(namespaces, ns) {
				namespaces = append(namespaces, ns)
			}
		}
	}
	return append(releases, operators...), namespaces
}

// releaseComponent is the table's component for a release name under p.
func releaseComponent(release string, p components.Placement) components.Component {
	if release == p.KafkaName {
		release = components.KafkaRelease
	}
	c, _ := components.ByRelease(release)
	return c
}

var (
	cleanRunFn       = cleanRunDefault
	cleanRunOutputFn = cleanRunOutputDefault
	cleanSleepFn     = time.Sleep
)

func cleanRunDefault(ctx context.Context, name string, args ...string) error {
	if cleanVerbose {
		fmt.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if cleanVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func cleanRunOutputDefault(ctx context.Context, name string, args ...string) ([]byte, error) {
	if cleanVerbose {
		fmt.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if cleanVerbose && len(out) > 0 {
		fmt.Print(string(out))
	}
	return out, err
}

func cleanRun(ctx context.Context, name string, args ...string) error {
	return cleanRunFn(ctx, name, args...)
}

func cleanRunOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return cleanRunOutputFn(ctx, name, args...)
}

// clusterObject is a cluster-scoped resource the stack makes by name.
type clusterObject struct{ Kind, Name string }

// cleanPlan is what one kates clean found and will remove: filled by
// discoverClean, narrowed by keepForOthers, shown by printCleanPlan and
// carried out by the clean steps.
type cleanPlan struct {
	kubeContext string
	// What the component table says the stack occupies.
	releases   []components.Release
	namespaces []string
	crds       []string
	// What the cluster holds of it.
	installed    []components.Release
	helmList     []helmRelease
	helmListed   bool
	existingNS   []string
	existingCRDs []string
	// presentCRDs are the stack's CRDs on the cluster, kept groups'
	// included: step 1 deletes custom resources inside the namespaces being
	// deleted, of every kind present.
	presentCRDs      []string
	clusterResources []clusterObject
	kept             []keptItem
}

// printCleanBanner prints the command's banner.
func printCleanBanner() {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
		Render("⎈ Kates Clean — Teardown"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 35)))
}

func runClean(cmd *cobra.Command, args []string) error {
	startTime := time.Now()
	printCleanBanner()

	// ── The cluster ──
	// Every call below names this context. Read once and pinned, the context
	// cannot move under a teardown that takes minutes: a `kubectl config
	// use-context` in another terminal used to send the rest of the deletes
	// to that cluster.
	kubeContext, err := cleanKubeContext()
	if err != nil {
		return cmdErr(err.Error())
	}
	defer pinCleanContext(kubeContext)()
	fmt.Printf("    Cluster: %s\n", lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(kubeContext))

	// The component table says what the stack occupies: the releases, the
	// namespaces and the CRDs.
	releases, namespaces := cleanTargets(cleanPlacements())
	p := &cleanPlan{
		kubeContext: kubeContext,
		releases:    releases,
		namespaces:  namespaces,
		crds:        components.CRDs(),
		clusterResources: []clusterObject{
			{"clusterrole", "kates"}, {"clusterrolebinding", "kates"},
			{"clusterrole", "litmus"}, {"clusterrolebinding", "litmus"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── Discovery ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrText).Render("  Scanning cluster..."))
	discoverClean(ctx, p)
	keepForOthers(ctx, p)

	if !printCleanPlan(p) {
		return nil
	}

	// ── Confirmation ──
	// Nothing above changed anything, so declining leaves the machine and
	// the cluster as they were.
	if !cleanForce {
		ok, err := confirm(fmt.Sprintf("Delete all of this from %s? This cannot be undone.", kubeContext))
		if err != nil {
			return cmdErr("aborted: " + err.Error())
		}
		if !ok {
			// A declined destructive action exits non-zero so scripts that
			// forgot --yes fail loudly instead of reporting success.
			return cmdErr("aborted: nothing removed")
		}
	}

	fmt.Println()

	// ── Port-forwards into the namespaces about to go ──
	if len(p.existingNS) > 0 {
		deleting := map[string]bool{}
		for _, ns := range p.existingNS {
			deleting[ns] = true
		}
		stopped := stopPortForwards(func(pf portForwardProc) bool {
			return deleting[pf.Namespace] && (pf.Context == "" || pf.Context == kubeContext)
		})
		if stopped > 0 {
			fmt.Printf("    %s Stopped %s into these namespaces\n",
				lipgloss.NewStyle().Foreground(clrDim).Render("🧹"), plural(stopped, "port-forward", "port-forwards"))
		}
	}

	failed := runCleanSteps(ctx, p)

	// ── Done ──
	fmt.Println()

	elapsed := time.Since(startTime).Round(time.Second)

	if len(failed) > 0 {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
			Render(fmt.Sprintf("  Not removed after %s:", elapsed)))
		for _, f := range failed {
			fmt.Printf("  %s %s\n", lipgloss.NewStyle().Foreground(clrRed).Render("✖"),
				lipgloss.NewStyle().Foreground(clrText).Render(f))
		}
		fmt.Println()
		return cmdErr(fmt.Sprintf("%s could not be removed from %s; see above", plural(len(failed), "step", "steps"), kubeContext))
	}

	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
		Render(fmt.Sprintf("  ✅ Cluster cleaned successfully in %s.", elapsed)))
	fmt.Println()
	return nil
}

// discoverClean reads which of the stack's releases, namespaces and CRDs the
// cluster holds. Each list is read in one call, or one object at a time when
// that fails.
func discoverClean(ctx context.Context, p *cleanPlan) {
	helmList, helmErr := helmReleases(ctx, cleanRunOutputFn, "")
	p.helmList, p.helmListed = helmList, helmErr == nil
	if p.helmListed {
		for _, r := range p.releases {
			if slices.ContainsFunc(helmList, func(l helmRelease) bool { return l.Name == r.Name && l.Namespace == r.Namespace }) {
				p.installed = append(p.installed, r)
			}
		}
	} else {
		// Fallback to sequential scanning
		for _, r := range p.releases {
			c, cn := context.WithTimeout(ctx, 3*time.Second)
			if cleanRun(c, "helm", "status", r.Name, "-n", r.Namespace) == nil {
				p.installed = append(p.installed, r)
			}
			cn()
		}
	}

	p.existingNS = cleanPresent(ctx, p.namespaces,
		[]string{"get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}"},
		func(ns string) []string { return []string{"get", "namespace", ns} })
	p.existingCRDs = cleanPresent(ctx, p.crds,
		[]string{"get", "crd", "-o", "jsonpath={.items[*].metadata.name}"},
		func(crd string) []string { return []string{"get", "crd", crd} })
	p.presentCRDs = append([]string(nil), p.existingCRDs...)
}

// cleanPresent returns which of wanted the cluster holds: from one kubectl
// list (listArgs, printing names), or one kubectl get per name when the list
// fails.
func cleanPresent(ctx context.Context, wanted []string, listArgs []string, getArgs func(string) []string) []string {
	var present []string
	out, err := cleanRunOutput(ctx, "kubectl", listArgs...)
	if err == nil {
		have := strings.Fields(strings.TrimSpace(string(out)))
		for _, name := range wanted {
			if slices.Contains(have, name) {
				present = append(present, name)
			}
		}
		return present
	}
	// Fallback to sequential scanning
	for _, name := range wanted {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if cleanRun(c, "kubectl", getArgs(name)...) == nil {
			present = append(present, name)
		}
		cn()
	}
	return present
}

// keepForOthers takes out of the plan what something outside the stack still
// uses (clean_shared.go), and records why.
func keepForOthers(ctx context.Context, p *cleanPlan) {
	operatorNS := map[string]bool{}
	for _, r := range p.releases {
		if c, ok := components.ByRelease(r.Name); ok && c.Operator {
			operatorNS[r.Namespace] = true
		}
	}

	// A namespace with a Helm release kates does not install is someone
	// else's too: deleting it would delete that release's objects.
	var nsGoing []string
	for _, ns := range p.existingNS {
		switch {
		case !p.helmListed:
			p.kept = append(p.kept, keptItem{"namespace " + ns, "could not list Helm releases to see what else is in it"})
		default:
			if foreign := foreignReleases(p.helmList, ns, p.releases); len(foreign) > 0 {
				p.kept = append(p.kept, keptItem{"namespace " + ns, "it also holds " + plural(len(foreign), "release", "releases") + " kates does not install: " + strings.Join(foreign, ", ")})
				continue
			}
			nsGoing = append(nsGoing, ns)
		}
	}
	p.existingNS = nsGoing

	// An operator, its namespace and its CRDs stay while something outside
	// the stack uses them.
	deleting := map[string]bool{}
	for _, ns := range p.existingNS {
		if !operatorNS[ns] {
			deleting[ns] = true
		}
	}
	present := map[string]bool{}
	for _, crd := range p.existingCRDs {
		present[crd] = true
	}
	for _, g := range cleanCRDGroups() {
		why := groupInUse(ctx, g, present, deleting)
		if why == "" {
			continue
		}
		what := g.Name + " CRDs"
		drop := map[string]bool{}
		for _, crd := range g.CRDs {
			drop[crd] = true
		}
		p.existingCRDs = slices.DeleteFunc(p.existingCRDs, func(crd string) bool { return drop[crd] })
		if op := g.Owner; op != nil {
			what = g.Name + " operator, its namespace and its CRDs"
			p.installed = slices.DeleteFunc(p.installed, func(r components.Release) bool { return r == *op })
			p.existingNS = slices.DeleteFunc(p.existingNS, func(ns string) bool { return ns == op.Namespace })
		}
		p.kept = append(p.kept, keptItem{what, why})
	}

	// ClusterRoles go by name, unless a release outside the stack installed
	// them.
	going := map[string]bool{}
	for _, ns := range p.existingNS {
		going[ns] = true
	}
	var clusterGoing []clusterObject
	for _, cr := range p.clusterResources {
		owner, exists, err := clusterObjectOwner(ctx, cr.Kind, cr.Name)
		switch {
		case err != nil:
			p.kept = append(p.kept, keptItem{cr.Kind + " " + cr.Name, "could not read it to see what installed it"})
		case !exists:
		case owner == "" || going[owner]:
			clusterGoing = append(clusterGoing, cr)
		default:
			p.kept = append(p.kept, keptItem{cr.Kind + " " + cr.Name, "installed by a Helm release in " + owner})
		}
	}
	p.clusterResources = clusterGoing
}

// printCleanPlan lists what will be removed and what is kept, and returns
// false when there is nothing to remove.
func printCleanPlan(p *cleanPlan) bool {
	printKept := func() {
		if len(p.kept) == 0 {
			return
		}
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  Kept:"))
		for _, k := range p.kept {
			fmt.Printf("  %s %s %s\n", lipgloss.NewStyle().Foreground(clrGreen).Render("•"),
				lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(k.What),
				lipgloss.NewStyle().Foreground(clrDim).Render("— "+k.Why))
		}
	}

	if len(p.installed) == 0 && len(p.existingNS) == 0 && len(p.existingCRDs) == 0 && len(p.clusterResources) == 0 {
		printKept()
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Bold(true).
			Render("  ✓ Nothing to remove."))
		fmt.Println()
		return false
	}

	// ── Show what will be removed ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrOrange).
		Render("  The following resources will be removed:"))

	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	dimStyle := lipgloss.NewStyle().Foreground(clrDim)
	bulletStyle := lipgloss.NewStyle().Foreground(clrRed)
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render(title))
		for _, line := range lines {
			fmt.Printf("  %s %s\n", bulletStyle.Render("✖"), line)
		}
	}

	var releases, cluster []string
	for _, r := range p.installed {
		releases = append(releases, nameStyle.Render(fmt.Sprintf("%-20s", r.Name))+" "+dimStyle.Render(fmt.Sprintf("→ %s", r.Namespace)))
	}
	for _, cr := range p.clusterResources {
		cluster = append(cluster, nameStyle.Render(cr.Kind+" "+cr.Name))
	}
	styled := func(names []string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = nameStyle.Render(n)
		}
		return out
	}
	section("  Helm Releases:", releases)
	section("  Namespaces:", styled(p.existingNS))
	section("  CustomResourceDefinitions (CRDs):", styled(p.existingCRDs))
	section("  Cluster-scoped:", cluster)
	printKept()
	fmt.Println()
	return true
}

// cleanFailures collects the steps that did not go through. Each is printed
// where it fails and listed again at the end, which then exits 1: clean used
// to print "cleaned successfully" and exit 0 over a release that would not
// uninstall or a CRD batch kubectl refused.
type cleanFailures struct {
	mu   sync.Mutex
	list []string
}

func (f *cleanFailures) add(what string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = append(f.list, what)
}

var (
	cleanOKStyle   = lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	cleanErrStyle  = lipgloss.NewStyle().Foreground(clrRed)
	cleanStepStyle = lipgloss.NewStyle().Foreground(clrCyan).Bold(true)
	cleanTextStyle = lipgloss.NewStyle().Foreground(clrText)
)

// runCleanSteps carries the plan out and returns the steps that failed.
func runCleanSteps(ctx context.Context, p *cleanPlan) []string {
	var failed cleanFailures

	// Filter operator CRD types actually present in cluster
	var activeCRDTypes []string
	for _, crdType := range components.NamespacedCRDs() {
		if slices.Contains(p.presentCRDs, crdType) {
			activeCRDTypes = append(activeCRDTypes, crdType)
		}
	}

	cleanOperatorCRs(ctx, activeCRDTypes, p.existingNS)
	cleanUninstallReleases(ctx, p.installed, &failed)

	// ── 4. Delete cluster-scoped resources ──
	for _, cr := range p.clusterResources {
		dCtx, dCancel := context.WithTimeout(ctx, 10*time.Second)
		err := cleanRun(dCtx, "kubectl", "delete", cr.Kind, cr.Name, "--ignore-not-found")
		dCancel()
		if err != nil {
			fmt.Printf("%s%s\n", cleanTextStyle.Render("  Deleting "+cr.Kind+" "+cr.Name), cleanErrStyle.Render("  ✖ "+err.Error()))
			failed.add(cr.Kind + " " + cr.Name)
		}
	}

	cleanDeleteNamespaces(ctx, p.existingNS, &failed)

	// ── 6. Delete CustomResourceDefinitions (CRDs) ──
	if len(p.existingCRDs) > 0 {
		fmt.Println()
		fmt.Println(cleanStepStyle.Render("  Step 5: Deleting CustomResourceDefinitions (CRDs) in batch..."))
		label := fmt.Sprintf("  Deleting %d CRDs", len(p.existingCRDs))
		fmt.Print(cleanTextStyle.Render(label))

		args := append([]string{"delete", "crd", "--ignore-not-found"}, p.existingCRDs...)
		cCtx, cCancel := context.WithTimeout(ctx, 1*time.Minute)
		err := cleanRun(cCtx, "kubectl", args...)
		cCancel()

		if err != nil {
			fmt.Println(cleanErrStyle.Render("  ✖ " + err.Error()))
			failed.add(fmt.Sprintf("%d CRDs", len(p.existingCRDs)))
		} else {
			fmt.Println(cleanOKStyle.Render("  ✔"))
		}
	}
	return failed.list
}

// cleanOperatorCRs is steps 1 and 2: delete the operators' custom resources
// in the namespaces going, while the operators still run, then strip the
// finalizers of any that are stuck.
func cleanOperatorCRs(ctx context.Context, activeCRDTypes, existingNS []string) {
	// ── 1. Delete Operator CRs FIRST (while operators are still running) ──
	if len(activeCRDTypes) > 0 && len(existingNS) > 0 {
		fmt.Println(cleanStepStyle.Render("  Step 1: Removing operator custom resources..."))
		var crWg sync.WaitGroup
		for _, crdType := range activeCRDTypes {
			for _, ns := range existingNS {
				crWg.Add(1)
				go func(crd, namespace string) {
					defer crWg.Done()
					dCtx, dCancel := context.WithTimeout(ctx, 30*time.Second)
					defer dCancel()
					_ = cleanRun(dCtx, "kubectl", "delete", crd, "--all", "-n", namespace, "--ignore-not-found")
				}(crdType, ns)
			}
		}
		crWg.Wait()
	}
	// Wait briefly for operators to process finalizer removal.
	cleanSleepFn(5 * time.Second)

	// ── 2. Strip orphaned finalizers on any remaining stuck resources ──
	if len(activeCRDTypes) > 0 && len(existingNS) > 0 {
		fmt.Println(cleanStepStyle.Render("  Step 2: Stripping orphaned finalizers..."))
		var finWg sync.WaitGroup
		for _, crdType := range activeCRDTypes {
			for _, ns := range existingNS {
				finWg.Add(1)
				go func(crd, namespace string) {
					defer finWg.Done()
					pCtx, pCancel := context.WithTimeout(ctx, 10*time.Second)
					out, err := cleanRunOutput(pCtx, "kubectl", "get", crd, "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}")
					pCancel()
					if err != nil {
						return
					}
					names := strings.Fields(strings.TrimSpace(string(out)))
					if len(names) > 0 {
						var patchWg sync.WaitGroup
						for _, name := range names {
							patchWg.Add(1)
							go func(n string) {
								defer patchWg.Done()
								fCtx, fCancel := context.WithTimeout(ctx, 5*time.Second)
								defer fCancel()
								_ = cleanRun(fCtx, "kubectl", "patch", crd, n, "-n", namespace,
									"--type", "merge", "-p", `{"metadata":{"finalizers":[]}}`)
							}(name)
						}
						patchWg.Wait()
					}
				}(crdType, ns)
			}
		}
		finWg.Wait()
	}
}

// cleanUninstallReleases is step 3: the releases, all at once.
func cleanUninstallReleases(ctx context.Context, installed []components.Release, failed *cleanFailures) {
	if len(installed) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(cleanStepStyle.Render("  Step 3: Uninstalling Helm releases..."))
	var helmWg sync.WaitGroup
	var printMutex sync.Mutex
	for _, r := range installed {
		helmWg.Add(1)
		go func(rel components.Release) {
			defer helmWg.Done()
			uCtx, uCancel := context.WithTimeout(ctx, 2*time.Minute)
			out, err := cleanRunOutput(uCtx, "helm", "uninstall", rel.Name, "-n", rel.Namespace)
			uCancel()

			printMutex.Lock()
			defer printMutex.Unlock()
			label := fmt.Sprintf("  Uninstalling %-16s from %s", rel.Name, rel.Namespace)
			if err != nil {
				fmt.Printf("%s%s\n", cleanTextStyle.Render(label), cleanErrStyle.Render("  ✖ "+strings.TrimSpace(string(out))))
				failed.add(fmt.Sprintf("helm release %s in %s", rel.Name, rel.Namespace))
			} else {
				fmt.Printf("%s%s\n", cleanTextStyle.Render(label), cleanOKStyle.Render("  ✔"))
			}
		}(r)
	}
	helmWg.Wait()
}

// cleanDeleteNamespaces is step 4: the namespaces, all at once. A delete the
// API accepted that is still winding down after three minutes finishes on
// its own; it is not a failure.
func cleanDeleteNamespaces(ctx context.Context, existingNS []string, failed *cleanFailures) {
	if len(existingNS) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(cleanStepStyle.Render("  Step 4: Deleting namespaces..."))
	var nsWg sync.WaitGroup
	var printMutex sync.Mutex
	for _, ns := range existingNS {
		nsWg.Add(1)
		go func(namespace string) {
			defer nsWg.Done()
			nCtx, nCancel := context.WithTimeout(ctx, 3*time.Minute)
			err := cleanRun(nCtx, "kubectl", "delete", "namespace", namespace, "--ignore-not-found")
			timedOut := errors.Is(nCtx.Err(), context.DeadlineExceeded)
			nCancel()

			printMutex.Lock()
			defer printMutex.Unlock()
			label := fmt.Sprintf("  Deleting namespace %s", namespace)
			switch {
			case err == nil:
				fmt.Printf("%s%s\n", cleanTextStyle.Render(label), cleanOKStyle.Render("  ✔"))
			case timedOut:
				fmt.Printf("%s%s\n", cleanTextStyle.Render(label), lipgloss.NewStyle().Foreground(clrOrange).Render("  ⚠ still terminating"))
			default:
				fmt.Printf("%s%s\n", cleanTextStyle.Render(label), cleanErrStyle.Render("  ✖ "+err.Error()))
				failed.add("namespace " + namespace)
			}
		}(ns)
	}
	nsWg.Wait()
}

// cleanKubeContext returns kubectl's current context, the cluster kates clean
// works on.
func cleanKubeContext() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := cleanRunOutput(ctx, "kubectl", "config", "current-context")
	name := strings.TrimSpace(string(out))
	if err != nil || name == "" {
		return "", fmt.Errorf("kubectl has no current context, so there is no cluster to clean.\n" +
			"  Choose one first:  kubectl config use-context <name>")
	}
	return name, nil
}

// pinCleanContext points every kubectl and helm call kates clean makes at
// kubeContext, and returns the function that undoes it. It wraps the seams
// rather than replacing them, so a test's stub still receives each call,
// flag included.
func pinCleanContext(kubeContext string) (unpin func()) {
	runFn, outputFn := cleanRunFn, cleanRunOutputFn
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		return runFn(ctx, name, withKubeContext(kubeContext, name, args)...)
	}
	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return outputFn(ctx, name, withKubeContext(kubeContext, name, args)...)
	}
	return func() { cleanRunFn, cleanRunOutputFn = runFn, outputFn }
}
