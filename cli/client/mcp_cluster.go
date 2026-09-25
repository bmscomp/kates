package client

import (
	"context"
	"net/url"
	"strconv"
)

// The reads behind the cluster tools of `kates mcp` (cluster_topology and
// consumer_group_lag) that the rest of the client does not already make in the
// form those tools need.

// MCPClusterTopology is the part of GET /api/cluster/topology that the
// cluster_topology tool reads: the Strimzi Kafka resource, its KafkaNodePools
// and the pods of those pools. The endpoint returns some twenty other sections
// (services, PVCs, ACLs, Connect and more); decoding only these three keeps a
// change in any other section from failing the tool.
type MCPClusterTopology struct {
	Cluster   *MCPTopologyCluster `json:"cluster"`
	NodePools []MCPNodePool       `json:"nodePools"`
	Nodes     []MCPTopologyNode   `json:"nodes"`
}

// MCPTopologyCluster describes the Kafka resource the backend is configured to
// read (ClusterTopologyService.describeKafkaCluster).
type MCPTopologyCluster struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// KafkaVersion is "unknown" when neither the resource's status nor its
	// spec names one.
	KafkaVersion string `json:"kafkaVersion"`
	// ClusterID is "unknown" when the admin API could not be asked.
	ClusterID string `json:"clusterId"`
	// Ready is the resource's Ready condition. It is nil, not false, when the
	// resource has no such condition or could not be read: the backend then
	// leaves the key out.
	Ready *bool `json:"ready"`
}

// MCPNodePool is one KafkaNodePool (ClusterTopologyService.describeNodePools).
type MCPNodePool struct {
	Name string `json:"name"`
	// Role is the pool's spec.roles joined with ",", or "unknown".
	Role     string `json:"role"`
	Replicas int    `json:"replicas"`
	// StorageType is "unknown" when the pool sets none; StorageSize and
	// StorageClass describe only its first volume.
	StorageType  string `json:"storageType"`
	StorageSize  string `json:"storageSize"`
	StorageClass string `json:"storageClass"`
}

// MCPTopologyNode is one pod of a node pool (ClusterTopologyService.describeNodes).
// Host, port and rack are left out: for any pod the admin API does not list
// as a broker, the backend makes them up from the pod name and port 9092.
type MCPTopologyNode struct {
	// ID is the number at the end of the pod name, or -1.
	ID int `json:"id"`
	// Role is the first of the pool's roles only.
	Role string `json:"role"`
	Pool string `json:"pool"`
	// Status is "Ready" or "NotReady", from the pod's Ready condition.
	Status  string `json:"status"`
	K8sNode string `json:"k8sNode"`
}

// MCPClusterTopology reads GET /api/cluster/topology.
func (c *Client) MCPClusterTopology(ctx context.Context) (*MCPClusterTopology, error) {
	return get[*MCPClusterTopology](c, ctx, "/api/cluster/topology")
}

// MCPTopicDetail reads GET /api/kafka/topics/{name}: each partition's leader,
// replicas and in-sync replicas, and the topic configs TopicService keeps. It
// is the endpoint TopicDetail reads under /api/cluster, served by the same
// TopicService.describeTopicDetail.
func (c *Client) MCPTopicDetail(ctx context.Context, name string) (*TopicDetail, error) {
	path, err := pathf("/api/kafka/topics/%s", name)
	if err != nil {
		return nil, err
	}
	return get[*TopicDetail](c, ctx, path)
}

// MCPTopicNamesPage is one page of GET /api/cluster/topics: topic names in
// order, and how many there are in all. Kafka's internal topics are not
// listed.
type MCPTopicNamesPage struct {
	Page  int      `json:"page"`
	Size  int      `json:"size"`
	Total int      `json:"total"`
	Items []string `json:"items"`
}

// MCPTopicNames reads one page of topic names. The backend caps size at 200.
func (c *Client) MCPTopicNames(ctx context.Context, page, size int) (*MCPTopicNamesPage, error) {
	return get[*MCPTopicNamesPage](c, ctx, withQuery("/api/cluster/topics", url.Values{
		"page": {strconv.Itoa(page)},
		"size": {strconv.Itoa(size)},
	}))
}
