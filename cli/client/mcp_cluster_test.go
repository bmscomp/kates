package client

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
)

func TestMCPTopicDetailEscapesTheName(t *testing.T) {
	got := firstRequest(t, func(ctx context.Context, c *Client) error { return ignore(c.MCPTopicDetail(ctx, hostileID)) })
	if got.Method != http.MethodGet || got.RequestURI != "/api/kafka/topics/"+hostileIDEscaped {
		t.Errorf("request = %s %s, want GET /api/kafka/topics/%s", got.Method, got.RequestURI, hostileIDEscaped)
	}

	for _, name := range []string{"", ".", ".."} {
		c, requests := recordingServer(t)
		if _, err := c.MCPTopicDetail(context.Background(), name); !errors.Is(err, ErrInvalidPathSegment) {
			t.Errorf("MCPTopicDetail(%q): err = %v, want ErrInvalidPathSegment", name, err)
		}
		if seen := requests(); len(seen) != 0 {
			t.Errorf("MCPTopicDetail(%q) sent %+v", name, seen)
		}
	}
}

func TestMCPTopicDetailDecodes(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, http.MethodGet, "/api/kafka/topics/orders", map[string]any{
		"name": "orders", "internal": false, "partitions": 2, "replicationFactor": 3,
		"partitionInfo": []any{
			map[string]any{"partition": 0, "leader": 1, "replicas": []int{1, 2, 0}, "isr": []int{1, 2}, "underReplicated": true},
			map[string]any{"partition": 1, "leader": -1, "replicas": []int{2, 0, 1}, "isr": []int{}, "underReplicated": true},
		},
		"configs": map[string]string{"min.insync.replicas": "2"},
	}))
	d, err := c.MCPTopicDetail(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "orders" || d.Partitions != 2 || len(d.PartitionInfo) != 2 || d.PartitionInfo[1].Leader != -1 ||
		len(d.PartitionInfo[0].ISR) != 2 || d.Configs["min.insync.replicas"] != "2" {
		t.Errorf("detail = %+v", d)
	}
}

func TestMCPTopicNamesQuery(t *testing.T) {
	got := firstRequest(t, func(ctx context.Context, c *Client) error { return ignore(c.MCPTopicNames(ctx, 3, 200)) })
	if got.Method != http.MethodGet || got.Path != "/api/cluster/topics" ||
		got.Query.Get("page") != "3" || got.Query.Get("size") != "200" || len(got.Query) != 2 {
		t.Errorf("request = %s %s", got.Method, got.RequestURI)
	}

	c, _ := testServer(t, jsonHandler(t, http.MethodGet, "/api/cluster/topics", map[string]any{
		"page": 0, "size": 200, "total": 2, "count": 2, "items": []string{"orders", "payments"},
	}))
	p, err := c.MCPTopicNames(context.Background(), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 2 || p.Size != 200 || len(p.Items) != 2 || p.Items[1] != "payments" {
		t.Errorf("page = %+v", p)
	}
}

// TestMCPClusterTopologyDecodesOnlyWhatItReads: the other twenty-odd sections
// of the topology may take any shape without failing the read, and a Kafka
// resource without a Ready condition reads as unknown, not as not ready.
func TestMCPClusterTopologyDecodesOnlyWhatItReads(t *testing.T) {
	c, _ := testServer(t, jsonHandler(t, http.MethodGet, "/api/cluster/topology", map[string]any{
		"kubernetes": "not the object ClusterTopology expects",
		"users":      []any{1, 2, 3},
		"logDirs":    map[string]any{"unexpected": true},
		"cluster": map[string]any{
			"name": "krafter", "namespace": "kafka", "kafkaVersion": "unknown", "clusterId": "abc",
			"controllerQuorumLeader": 4, "brokerCount": 3,
		},
		"nodePools": []any{map[string]any{"name": "controllers", "role": "controller", "replicas": 3,
			"storageType": "jbod", "storageSize": "10Gi"}},
		"nodes": []any{map[string]any{"id": 4, "host": "made-up", "port": 9092, "role": "controller",
			"pool": "controllers", "status": "Ready", "isQuorumLeader": true, "k8sNode": "kind-worker"}},
	}))
	topo, err := c.MCPClusterTopology(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if topo.Cluster == nil || topo.Cluster.Name != "krafter" || topo.Cluster.ClusterID != "abc" || topo.Cluster.Ready != nil {
		t.Errorf("cluster = %+v, want ready unknown", topo.Cluster)
	}
	if len(topo.NodePools) != 1 || topo.NodePools[0].Role != "controller" || topo.NodePools[0].Replicas != 3 {
		t.Errorf("nodePools = %+v", topo.NodePools)
	}
	if len(topo.Nodes) != 1 || topo.Nodes[0].ID != 4 || topo.Nodes[0].Status != "Ready" || topo.Nodes[0].K8sNode != "kind-worker" {
		t.Errorf("nodes = %+v", topo.Nodes)
	}

	for _, ready := range []bool{true, false} {
		c, _ := testServer(t, jsonHandler(t, http.MethodGet, "/api/cluster/topology", map[string]any{
			"cluster": map[string]any{"name": "krafter", "ready": ready},
		}))
		topo, err := c.MCPClusterTopology(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if topo.Cluster.Ready == nil || *topo.Cluster.Ready != ready {
			t.Errorf("ready %s: got %v", strconv.FormatBool(ready), topo.Cluster.Ready)
		}
	}
}
