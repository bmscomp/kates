package cmd

import (
	"strings"
	"testing"
)

// A snapshot keeps the broker that answered DescribeCluster, which on KRaft is
// not the active controller, and no quorum: the views name no controller.
func TestReportSnapshotCmdNamesNoController(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/tests/r1/report/snapshot", 200, `{
		"clusterId": "c1", "brokerCount": 2, "controllerId": 1,
		"brokers": [{"id": 1, "host": "broker-1", "port": 9092}, {"id": 2, "host": "broker-2", "port": 9092}]}`)
	defer ts.Close()

	if err := reportSnapshotCmd.RunE(reportSnapshotCmd, []string{"r1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	if !strings.Contains(out, "broker-1") || strings.Contains(out, "★") || strings.Contains(out, "Controller") {
		t.Errorf("broker 1 only answered DescribeCluster:\n%s", out)
	}
}

func TestReportBrokersCmdStarsNoController(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/tests/r1/report/brokers", 200, `[
		{"brokerId": 1, "host": "broker-1", "isController": true, "leaderPartitions": 3, "totalPartitions": 6},
		{"brokerId": 2, "host": "broker-2", "leaderPartitions": 3, "totalPartitions": 6}]`)
	defer ts.Close()

	if err := reportBrokersCmd.RunE(reportBrokersCmd, []string{"r1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "broker-1") || strings.Contains(out, "★") {
		t.Errorf("broker 1 only answered DescribeCluster:\n%s", out)
	}
}
