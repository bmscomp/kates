package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
)

// cluster_topology: where the controllers and brokers of the pinned cluster
// run, and, for one topic, which broker leads each partition.

const mcpClusterTopologyDescription = "Where the pinned Kafka cluster runs: the Strimzi Kafka resource, its node pools " +
	"(roles, replicas, storage), the KRaft controllers, and the brokers, each with its pod's readiness and Kubernetes " +
	"node and whether the Kafka admin API lists it, and the pods of pools with neither role. With topic, also that topic's partitions: leader, replicas and " +
	"in-sync replicas of each, a page at a time (from_partition), with counts of under-replicated and leaderless " +
	"partitions and how many partitions each broker leads over the whole topic, and the topic's min.insync.replicas " +
	"in force with what set it (the topic, a broker, or Kafka's default). It does not name the KRaft quorum " +
	"leader: cluster_overview does. Node pools and controllers are read through the Kubernetes API, so a backend " +
	"running outside Kubernetes cannot report them; with topic, the partitions are still returned. Only reads."

// Caps on the lists cluster_topology returns. A lab cluster has a few pools
// and a handful of nodes; the caps and mcpFitTopology keep a large cluster, or
// a topic with thousands of partitions, inside one result.
const (
	mcpTopologyMaxPools        = 50
	mcpTopologyMaxNodes        = 200
	mcpTopologyPartitionsPage  = 100
	mcpTopologyLeaderSummaries = 200
)

// mcpKafkaTopicPattern is Kafka's rule for topic names: 1 to 249 of these
// characters (org.apache.kafka.common.internals.Topic, kafka-clients 3.9.2).
// "." and ".." match it and are refused separately.
const mcpKafkaTopicPattern = `^[a-zA-Z0-9._-]{1,249}$`

var mcpKafkaTopicRE = regexp.MustCompile(mcpKafkaTopicPattern)

// mcpValidateKafkaTopic refuses anything Kafka would not accept as a topic name,
// so a topic argument can carry neither prose nor a path.
func mcpValidateKafkaTopic(field, topic string) error {
	if !mcpKafkaTopicRE.MatchString(topic) || topic == "." || topic == ".." {
		return mcpInvalidArgument(field+" must be a Kafka topic name: 1 to 249 letters, digits, '.', '_' or '-', "+
			"and not \".\" or \"..\".", topic)
	}
	return nil
}

// mcpClusterInputSchema is the input schema addReadTool would infer for In, with
// edit applied: the constraints a struct tag cannot carry (a pattern, bounds),
// so that the SDK refuses a bad argument before any Kates code runs. The
// handler checks the same things again.
func mcpClusterInputSchema[In any](name string, edit func(props map[string]*jsonschema.Schema)) *jsonschema.Schema {
	s, err := mcpSchemaFor[In]()
	if err != nil {
		panic(fmt.Sprintf("kates mcp: tool %q input schema: %v", name, err))
	}
	edit(s.Properties)
	return s
}

func mcpClusterFloat(f float64) *float64 { return &f }

func registerMCPClusterTopology(s *mcp.Server, deps *mcpDeps) {
	addReadTool(s, deps, &mcp.Tool{
		Name:        "cluster_topology",
		Title:       "Cluster topology",
		Description: mcpClusterTopologyDescription,
		InputSchema: mcpClusterInputSchema[mcpClusterTopologyIn]("cluster_topology", func(p map[string]*jsonschema.Schema) {
			p["topic"].Pattern = mcpKafkaTopicPattern
			p["from_partition"].Minimum = mcpClusterFloat(0)
		}),
	}, mcpClusterTopology, mcpCaveatClusterDataCached)
}

type mcpClusterTopologyIn struct {
	Topic         string `json:"topic,omitempty" jsonschema:"a topic name; with it the result adds the topic's partitions"`
	FromPartition int    `json:"from_partition,omitempty" jsonschema:"with topic: the first partition to list (default 0); pass nextFromPartition from the previous result for the next page"`
}

type mcpClusterTopologyOut struct {
	Topology    mcpTopologyRead     `json:"topology" jsonschema:"whether the Strimzi resources could be read"`
	Kafka       *mcpTopologyKafka   `json:"kafka,omitempty" jsonschema:"the Strimzi Kafka resource the backend is configured to read"`
	NodePools   []mcpTopologyPool   `json:"nodePools" jsonschema:"the cluster's KafkaNodePools, by name"`
	Controllers []mcpTopologyPod    `json:"controllers" jsonschema:"KRaft controllers: pods of the node pools with the controller role, by id"`
	Brokers     []mcpTopologyBroker `json:"brokers" jsonschema:"pods of the node pools with the broker role, and every broker the Kafka admin API lists, by id; a node of a pool with both roles is also a controller"`
	OtherPods   []mcpTopologyPod    `json:"otherPods,omitempty" jsonschema:"pods of node pools with neither the controller nor the broker role, such as a pool whose roles are not set, by id"`
	Topic       *mcpTopicLayout     `json:"topic,omitempty" jsonschema:"the topic asked for"`
}

type mcpTopologyRead struct {
	Available bool         `json:"available" jsonschema:"false when the backend could not read the Strimzi resources; kafka, nodePools and controllers are then empty and brokers come from the Kafka admin API alone. Only a call with topic answers when this read fails."`
	ErrorCode mcpErrorCode `json:"errorCode,omitempty" jsonschema:"why the Strimzi resources are not available"`
}

// mcpTopologyKafka is the Kafka resource the backend is configured to read.
// The backend reports its name and namespace from configuration whether or not
// the resource exists (ClusterTopologyService.java:303-327).
type mcpTopologyKafka struct {
	Name         string `json:"name" jsonschema:"the name the backend is configured to read (kates.topology.kafka-cluster), reported whether or not such a resource exists"`
	Namespace    string `json:"namespace" jsonschema:"the namespace the backend is configured to read (kates.topology.kafka-namespace)"`
	KafkaVersion string `json:"kafkaVersion,omitempty" jsonschema:"from the resource's status, else its spec; absent when neither names one. With ready also absent, the resource may not exist or could not be read"`
	Ready        *bool  `json:"ready,omitempty" jsonschema:"the resource's Ready condition; absent when it has none, does not exist or could not be read"`
}

type mcpTopologyPool struct {
	Name         string   `json:"name"`
	Roles        []string `json:"roles" jsonschema:"controller, broker, or both"`
	Replicas     int      `json:"replicas" jsonschema:"spec.replicas: the pods the pool asks for, not the pods that are running"`
	Pods         int      `json:"pods" jsonschema:"pods of this pool the backend found. Fewer than replicas can mean pods not yet created or being replaced, or a pod read that failed partway, which the caveats then name"`
	StorageType  string   `json:"storageType,omitempty" jsonschema:"the Strimzi storage type, such as jbod, persistent-claim or ephemeral"`
	StorageSize  string   `json:"storageSize,omitempty" jsonschema:"size of the first volume of jbod storage. The backend reads no other size, so this is absent for persistent-claim and ephemeral storage even when they set one"`
	StorageClass string   `json:"storageClass,omitempty" jsonschema:"storage class of the first volume of jbod storage; absent for other storage types and when the volume names none"`
}

type mcpTopologyPod struct {
	ID       int    `json:"id" jsonschema:"node id, the number at the end of the pod name; -1 when the name does not end in one"`
	Pool     string `json:"pool"`
	PodReady bool   `json:"podReady" jsonschema:"the pod's Ready condition"`
	K8sNode  string `json:"k8sNode,omitempty" jsonschema:"the Kubernetes node the pod runs on"`
}

type mcpTopologyBroker struct {
	ID         int    `json:"id" jsonschema:"node id; -1 for a pod whose name does not end in one"`
	Pool       string `json:"pool,omitempty" jsonschema:"the node pool; absent when no pod of a broker pool has this id"`
	PodReady   *bool  `json:"podReady,omitempty" jsonschema:"the pod's Ready condition; absent when no pod was found"`
	Registered bool   `json:"registered" jsonschema:"whether the Kafka admin API lists this broker; it leaves out brokers that are down or fenced"`
	Host       string `json:"host,omitempty" jsonschema:"the host the broker advertises, when registered"`
	Rack       string `json:"rack,omitempty" jsonschema:"the broker's rack, when registered"`
	K8sNode    string `json:"k8sNode,omitempty" jsonschema:"the Kubernetes node the pod runs on"`
}

type mcpTopicLayout struct {
	Name              string              `json:"name"`
	Internal          bool                `json:"internal"`
	PartitionCount    int                 `json:"partitionCount"`
	ReplicationFactor int                 `json:"replicationFactor" jsonschema:"replicas of the first partition"`
	MinInsyncReplicas *int                `json:"minInsyncReplicas,omitempty" jsonschema:"the topic's min.insync.replicas in force, wherever it is set; absent only from an older Kates backend, which leaves out a value set at broker level"`
	MinISRSource      string              `json:"minInsyncReplicasSource,omitempty" jsonschema:"what set minInsyncReplicas, as Kafka names it: DYNAMIC_TOPIC_CONFIG (the topic), STATIC_BROKER_CONFIG or DYNAMIC_BROKER_CONFIG (a broker), DYNAMIC_DEFAULT_BROKER_CONFIG (the cluster-wide default), DEFAULT_CONFIG (Kafka's default); absent from an older backend"`
	UnderReplicated   int                 `json:"underReplicated" jsonschema:"partitions, over the whole topic, with fewer in-sync replicas than replicas"`
	Leaderless        int                 `json:"leaderless" jsonschema:"partitions, over the whole topic, with no leader"`
	Leaders           []mcpBrokerLeads    `json:"leaders" jsonschema:"how many partitions each broker leads, over the whole topic, by broker id; broker -1 counts partitions with no leader"`
	FromPartition     int                 `json:"fromPartition" jsonschema:"the first partition this page lists"`
	Partitions        []mcpTopicPartition `json:"partitions" jsonschema:"this page of partitions, by partition id"`
	NextFromPartition *int                `json:"nextFromPartition,omitempty" jsonschema:"pass as from_partition for the next page; absent on the last page"`
}

type mcpBrokerLeads struct {
	Broker  int `json:"broker"`
	Leaders int `json:"leaders"`
}

type mcpTopicPartition struct {
	Partition       int   `json:"partition"`
	Leader          int   `json:"leader" jsonschema:"broker id of the leader; -1 when the partition has none"`
	Replicas        []int `json:"replicas"`
	ISR             []int `json:"isr" jsonschema:"in-sync replicas"`
	UnderReplicated bool  `json:"underReplicated"`
}

func mcpClusterTopology(ctx context.Context, call *mcpCall, in mcpClusterTopologyIn) (mcpClusterTopologyOut, error) {
	var out mcpClusterTopologyOut
	if in.Topic != "" {
		if err := mcpValidateKafkaTopic("topic", in.Topic); err != nil {
			return out, err
		}
	}
	switch {
	case in.FromPartition < 0:
		return out, mcpInvalidArgument("from_partition must be 0 or more.", strconv.Itoa(in.FromPartition))
	case in.FromPartition > 0 && in.Topic == "":
		return out, mcpInvalidArgument("from_partition pages a topic's partitions, so it needs topic.", strconv.Itoa(in.FromPartition))
	}

	// The two reads are independent. With a topic, the partitions are what
	// was asked for, so a failed topology read (always, for a backend outside
	// Kubernetes) is recorded rather than returned; without one it is the
	// whole answer, and its error is. call.Go, so that a panic ends this call,
	// not the server.
	var (
		topo      *client.MCPClusterTopology
		topoErr   error
		detail    *client.TopicDetail
		detailErr error
	)
	g, gctx := errgroup.WithContext(ctx)
	call.Go(g, func() error {
		topo, topoErr = call.Client().MCPClusterTopology(gctx)
		return nil
	})
	if in.Topic != "" {
		call.Go(g, func() error {
			detail, detailErr = call.Client().MCPTopicDetail(gctx, in.Topic)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return out, err
	}

	if in.Topic != "" {
		if detailErr != nil {
			return out, mcpTopicDetailError(ctx, call, in.Topic, detailErr)
		}
		if detail == nil {
			return out, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty topic description.", Retryable: true}
		}
	}
	switch {
	case topoErr != nil && in.Topic == "":
		return out, mcpTopologyError(call, topoErr)
	case topoErr != nil:
		call.deps.logger.Warn("cluster_topology: topology unavailable", "error", topoErr)
		out.Topology = mcpTopologyRead{Available: false, ErrorCode: mcpTopologyError(call, topoErr).Code}
		topo = nil
	case topo == nil:
		if in.Topic == "" {
			return out, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty cluster topology.", Retryable: true}
		}
		out.Topology = mcpTopologyRead{Available: false, ErrorCode: mcpErrBackend}
	default:
		out.Topology = mcpTopologyRead{Available: true}
		// The backend fills clusterId from the admin API, or with "unknown"
		// when that call failed (ClusterTopologyService.java:396-411).
		if topo.Cluster != nil && topo.Cluster.ClusterID != "unknown" {
			if err := call.CheckCluster(topo.Cluster.ClusterID); err != nil {
				return out, err
			}
		}
	}

	mcpTopologyNodes(call, topo, &out)
	if detail != nil {
		layout, err := mcpTopicLayoutFrom(call, detail, in.FromPartition)
		if err != nil {
			return out, err
		}
		out.Topic = layout
	}
	mcpFitTopology(call, &out)
	return out, nil
}

// mcpTopologyError says what a failed topology read means. The backend
// answers 503 when it cannot reach the Kubernetes API, which it reads the
// Strimzi resources through (ClusterTopologyService.java:115-122,
// ClusterResource.java:226-229): for a backend outside Kubernetes that is
// every call, so the generic "retry later" would send an agent round in
// circles.
func mcpTopologyError(call *mcpCall, err error) *mcpToolError {
	te := call.deps.classify(err)
	var he *client.HTTPError
	if errors.As(err, &he) && he.StatusCode == http.StatusServiceUnavailable {
		te.Message = "The Kates API could not read the cluster topology (HTTP 503). The backend answers so when it " +
			"cannot reach the Kubernetes API, through which it reads the Strimzi resources, and always when it runs " +
			"outside Kubernetes; the detail says which. Retry only if the backend runs in Kubernetes."
	}
	return te
}

// mcpTopologyNodes fills in the Kafka resource, the node pools, the
// controllers and the brokers. topo is nil when the Strimzi resources could
// not be read; the brokers the Kafka admin API lists (the cluster info the pin
// check read for this call) are added either way.
//
// A pod's roles are its pool's: the backend gives each node only the first of
// them (ClusterTopologyService.java:639-640), which would hide the controller
// in a pool whose roles are [broker, controller].
//
// Names from Kubernetes are passed on only in the shape Kubernetes allows
// (mcpK8sName and the patterns below), so a value that is not a real name
// cannot carry prose into a field the output schema presents as data; one
// that fails is left out.
func mcpTopologyNodes(call *mcpCall, topo *client.MCPClusterTopology, out *mcpClusterTopologyOut) {
	pools := []mcpTopologyPool{}
	controllers := []mcpTopologyPod{}
	others := []mcpTopologyPod{}
	brokers := map[int]*mcpTopologyBroker{}
	// Pods whose name ends in no number all get id -1 from the backend
	// (ClusterTopologyService.java:1364-1371); keyed by id they would collapse
	// into one, so they are kept apart.
	var unnumbered []mcpTopologyBroker
	if topo != nil {
		if c := topo.Cluster; c != nil {
			out.Kafka = &mcpTopologyKafka{
				Name:         mcpK8sName(c.Name, mcpK8sSubdomainRE, 253),
				Namespace:    mcpK8sName(c.Namespace, mcpK8sLabelRE, 63),
				KafkaVersion: mcpK8sName(c.KafkaVersion, mcpKafkaVersionRE, 32),
				Ready:        c.Ready,
			}
		}
		var roles map[string][]string
		var short bool
		pools, roles, short = mcpTopologyPools(topo)
		for _, n := range topo.Nodes {
			rs, known := roles[n.Pool]
			if !known {
				rs = mcpTopologyRoles([]string{n.Role})
			}
			ready := n.Status == "Ready"
			pod := mcpTopologyPod{
				ID:       n.ID,
				Pool:     mcpK8sName(n.Pool, mcpK8sSubdomainRE, 253),
				PodReady: ready,
				K8sNode:  mcpK8sName(n.K8sNode, mcpK8sSubdomainRE, 253),
			}
			isController, isBroker := mcpHasRole(rs, "controller"), mcpHasRole(rs, "broker")
			if isController {
				controllers = append(controllers, pod)
			}
			switch {
			case isBroker && n.ID < 0:
				unnumbered = append(unnumbered, mcpTopologyBroker{ID: -1, Pool: pod.Pool, PodReady: &ready, K8sNode: pod.K8sNode})
			case isBroker:
				brokers[n.ID] = &mcpTopologyBroker{ID: n.ID, Pool: pod.Pool, PodReady: &ready, K8sNode: pod.K8sNode}
			case !isController:
				others = append(others, pod)
			}
		}
		// The backend lists the pools, then their pods pool by pool, and a
		// read that fails is logged and left out: the pods listed before it
		// are kept (ClusterTopologyService.java:523-596,599-688). So an empty
		// list, or a pool with fewer pods than replicas, is not evidence that
		// the nodes are missing.
		if len(topo.NodePools) == 0 || len(topo.Nodes) == 0 || short {
			call.Caveat(mcpCaveatTopologyUnreadLooksEmpty)
		}
	}
	if info := call.ClusterInfo(); info != nil {
		for _, b := range info.Brokers {
			id := mcpInt(b.ID)
			br := brokers[id]
			if br == nil {
				br = &mcpTopologyBroker{ID: id}
				brokers[id] = br
			}
			br.Registered = true
			br.Host = mcpSanitizeLine(b.Host, 255)
			br.Rack = mcpSanitizeLine(b.Rack, 64)
		}
	}

	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	mcpSortPods(controllers)
	mcpSortPods(others)
	list := make([]mcpTopologyBroker, 0, len(brokers)+len(unnumbered))
	for _, b := range brokers {
		list = append(list, *b)
	}
	list = append(list, unnumbered...)
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Pool != b.Pool {
			return a.Pool < b.Pool
		}
		return a.K8sNode < b.K8sNode
	})

	out.NodePools = mcpCap(call, pools, mcpTopologyMaxPools)
	out.Controllers = mcpCap(call, controllers, mcpTopologyMaxNodes)
	out.Brokers = mcpCap(call, list, mcpTopologyMaxNodes)
	if len(others) > 0 {
		out.OtherPods = mcpCap(call, others, mcpTopologyMaxNodes)
	}
}

// mcpTopologyPools lists the node pools, each with the pods found for it. It
// also returns each pool's roles, by the name the backend reports, and whether
// any pool has fewer pods than replicas.
func mcpTopologyPools(topo *client.MCPClusterTopology) (pools []mcpTopologyPool, roles map[string][]string, short bool) {
	podsIn := map[string]int{}
	for _, n := range topo.Nodes {
		podsIn[n.Pool]++
	}
	pools = []mcpTopologyPool{}
	roles = map[string][]string{}
	for _, p := range topo.NodePools {
		rs := mcpTopologyRoles(strings.Split(p.Role, ","))
		roles[p.Name] = rs
		pools = append(pools, mcpTopologyPool{
			Name:         mcpK8sName(p.Name, mcpK8sSubdomainRE, 253),
			Roles:        rs,
			Replicas:     p.Replicas,
			Pods:         podsIn[p.Name],
			StorageType:  mcpTopologyStorageType(p.StorageType),
			StorageSize:  mcpK8sName(p.StorageSize, mcpK8sQuantityRE, 32),
			StorageClass: mcpK8sName(p.StorageClass, mcpK8sSubdomainRE, 253),
		})
		if podsIn[p.Name] < p.Replicas {
			short = true
		}
	}
	return pools, roles, short
}

// mcpSortPods orders pods by id, then pool and Kubernetes node, so pods that
// share id -1 come out in a stable order.
func mcpSortPods(pods []mcpTopologyPod) {
	sort.SliceStable(pods, func(i, j int) bool {
		a, b := pods[i], pods[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Pool != b.Pool {
			return a.Pool < b.Pool
		}
		return a.K8sNode < b.K8sNode
	})
}

// mcpTopologyRoles keeps the KRaft roles Strimzi allows a node pool
// (controller and broker); anything else, such as the backend's "unknown"
// for a pool with no roles, is dropped.
func mcpTopologyRoles(raw []string) []string {
	rs := []string{}
	for _, r := range raw {
		if r = strings.TrimSpace(r); (r == "broker" || r == "controller") && !mcpHasRole(rs, r) {
			rs = append(rs, r)
		}
	}
	return rs
}

// mcpTopologyStorageType is the pool's Strimzi storage type, or "" when the
// backend reports none ("unknown").
func mcpTopologyStorageType(t string) string {
	if t == "unknown" {
		return ""
	}
	return mcpK8sName(t, mcpStorageTypeRE, 32)
}

// Shapes of the names Kubernetes and Strimzi hand the backend: resource and
// node names are DNS-1123 subdomains, namespaces DNS-1123 labels, sizes
// resource quantities, storage types Strimzi's lowercase keywords.
var (
	mcpK8sSubdomainRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	mcpK8sLabelRE     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	mcpK8sQuantityRE  = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+|[KMGTPE]i|[numkMGTPE])?$`)
	mcpStorageTypeRE  = regexp.MustCompile(`^[a-z][a-z-]*$`)
	mcpKafkaVersionRE = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)
)

// mcpK8sName returns s when it has the shape re describes and at most maxLen
// bytes, and "" otherwise. Every shape is plain ASCII without spaces, so a
// value that passes needs no cleaning and cannot carry prose.
func mcpK8sName(s string, re *regexp.Regexp, maxLen int) string {
	if len(s) > maxLen || !re.MatchString(s) {
		return ""
	}
	return s
}

func mcpHasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// mcpTopicLayoutFrom summarises a topic over all its partitions and lists one
// page of them, starting at partition from.
func mcpTopicLayoutFrom(call *mcpCall, d *client.TopicDetail, from int) (*mcpTopicLayout, error) {
	parts := make([]mcpTopicPartition, 0, len(d.PartitionInfo))
	leads := map[int]int{}
	t := &mcpTopicLayout{
		Name:              mcpSanitizeLine(d.Name, 249),
		Internal:          d.Internal,
		PartitionCount:    d.Partitions,
		ReplicationFactor: d.ReplicationFactor,
		FromPartition:     from,
	}
	for _, p := range d.PartitionInfo {
		leader := p.Leader
		if leader < 0 {
			leader = -1
			t.Leaderless++
		}
		if p.UnderReplicated {
			t.UnderReplicated++
		}
		leads[leader]++
		parts = append(parts, mcpTopicPartition{
			Partition:       p.Partition,
			Leader:          leader,
			Replicas:        mcpClusterInts(p.Replicas),
			ISR:             mcpClusterInts(p.ISR),
			UnderReplicated: p.UnderReplicated,
		})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Partition < parts[j].Partition })

	leaders := make([]mcpBrokerLeads, 0, len(leads))
	for b, n := range leads {
		leaders = append(leaders, mcpBrokerLeads{Broker: b, Leaders: n})
	}
	sort.Slice(leaders, func(i, j int) bool { return leaders[i].Broker < leaders[j].Broker })
	t.Leaders = mcpCap(call, leaders, mcpTopologyLeaderSummaries)

	// Topic detail reports the value in force of each key it lists, and in
	// configSources what set it (TopicService.java:170-198). A backend from
	// before configSources kept only entries set on the topic or left at
	// Kafka's default: there a present value is also the one in force, but an
	// absent one is set at broker level and is not in the response.
	if v, ok := d.Configs["min.insync.replicas"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			t.MinInsyncReplicas = &n
			t.MinISRSource = mcpSanitizeLine(d.ConfigSources["min.insync.replicas"], 40)
		}
	}
	if t.MinInsyncReplicas == nil {
		call.Caveat(mcpCaveatMinISRBrokerLevel)
	}

	start := sort.Search(len(parts), func(i int) bool { return parts[i].Partition >= from })
	if from > 0 && start == len(parts) {
		return nil, mcpInvalidArgument("from_partition is past the topic's last partition; start again from 0.", strconv.Itoa(from))
	}
	page := parts[start:]
	if len(page) > mcpTopologyPartitionsPage {
		next := page[mcpTopologyPartitionsPage].Partition
		t.NextFromPartition = &next
		page = page[:mcpTopologyPartitionsPage]
		call.MarkTruncated()
	}
	t.Partitions = page
	return t, nil
}

// mcpClusterInts never returns nil, so an empty list serialises as [].
func mcpClusterInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

// mcpTopologyMinPage is the fewest partitions a page is cut to before the
// other lists are.
const mcpTopologyMinPage = 10

// mcpFitTopology shortens a result too large for one call. The partition page
// goes first, down to mcpTopologyMinPage, since the caller pages on with
// nextFromPartition; after that the longest list is halved until it fits.
func mcpFitTopology(call *mcpCall, out *mcpClusterTopologyOut) {
	type list struct {
		n   int
		cut func(n int)
	}
	cutPage := func(t *mcpTopicLayout, n int) {
		next := t.Partitions[n].Partition
		t.Partitions, t.NextFromPartition = t.Partitions[:n], &next
	}
	for !call.Fits(out) {
		call.MarkTruncated()
		t := out.Topic
		if t != nil && len(t.Partitions) > mcpTopologyMinPage {
			cutPage(t, len(t.Partitions)/2)
			continue
		}
		lists := []list{
			{len(out.Brokers), func(n int) { out.Brokers = out.Brokers[:n] }},
			{len(out.Controllers), func(n int) { out.Controllers = out.Controllers[:n] }},
			{len(out.NodePools), func(n int) { out.NodePools = out.NodePools[:n] }},
		}
		if t != nil {
			lists = append(lists,
				list{len(t.Leaders), func(n int) { t.Leaders = t.Leaders[:n] }},
				list{len(t.Partitions), func(n int) { cutPage(t, n) }})
		}
		longest := 0
		for i, l := range lists {
			if l.n > lists[longest].n {
				longest = i
			}
		}
		if lists[longest].n <= 1 {
			return // nothing left to cut; the guard refuses the result
		}
		lists[longest].cut(lists[longest].n / 2)
	}
}

// Paging through the topic list to tell a missing topic from a failed read.
const (
	mcpTopicListPageSize = 200 // the backend's largest page
	mcpTopicListMaxPages = 10
)

// mcpTopicDetailError explains a failed topic read. The backend answers a
// topic it cannot describe with HTTP 500 whether the topic is missing or Kafka
// failed: TopicService wraps every failure as "Failed to describe topic", and
// the resource answers 404 only for a message containing "not found", which
// that one never does (TopicService.java:138-140,201-203;
// KafkaClientResource.java:111-119). So on a 5xx the topic list decides: a
// topic missing from it is reported as not found, anything else is passed on.
func mcpTopicDetailError(ctx context.Context, call *mcpCall, topic string, err error) error {
	var he *client.HTTPError
	if !errors.As(err, &he) || he.StatusCode < 500 {
		return err
	}
	if listed, known := mcpTopicIsListed(ctx, call, topic); !known || listed {
		return err
	}
	// The list is the backend's, cached for 30 seconds and never cleared
	// early (TopicService.java:32,101-116), so a topic created since can be
	// missing from it. The common case is a name that does not exist, so the
	// error is not marked retryable; the message names the exception.
	return &mcpToolError{
		Code: mcpErrNotFound,
		Message: "The Kates API could not describe this topic, and it is not in the cluster's topic list, so Kafka most " +
			"likely has no topic of that name. The backend caches that list for up to 30 seconds: if the topic was " +
			"created in the last 30 seconds, retry after that.",
		Detail: he.Error(),
		cause:  err,
	}
}

// mcpTopicIsListed reports whether topic is in the cluster's topic list, and
// whether the list could be read far enough to say. The list leaves out
// Kafka's internal topics, whose names start "__" (TopicService.listTopics
// takes the admin API's default), so for those it says nothing.
func mcpTopicIsListed(ctx context.Context, call *mcpCall, topic string) (listed, known bool) {
	if strings.HasPrefix(topic, "__") {
		return false, false
	}
	for page := 0; page < mcpTopicListMaxPages; page++ {
		p, err := call.Client().MCPTopicNames(ctx, page, mcpTopicListPageSize)
		if err != nil || p == nil {
			return false, false
		}
		for _, name := range p.Items {
			if name == topic {
				return true, true
			}
		}
		size := p.Size
		if size <= 0 {
			size = mcpTopicListPageSize
		}
		if len(p.Items) == 0 || (page+1)*size >= p.Total {
			return false, true
		}
	}
	return false, false
}
