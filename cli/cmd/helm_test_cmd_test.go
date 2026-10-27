package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The first argument is the component unless it is a flag; everything after
// it goes to helm test.
func TestSplitHelmTestArgs(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		component string
		extra     []string
	}{
		{nil, "", nil},
		{[]string{"Kafka"}, "kafka", []string{}},
		{[]string{"kafka", "--filter", "x"}, "kafka", []string{"--filter", "x"}},
		{[]string{"--filter", "x"}, "", []string{"--filter", "x"}},
	} {
		component, extra := splitHelmTestArgs(tc.args)
		if component != tc.component || !slices.Equal(extra, tc.extra) {
			t.Errorf("splitHelmTestArgs(%q) = %q, %q; want %q, %q", tc.args, component, extra, tc.component, tc.extra)
		}
	}
}

// The suite counts each release by its status.
func TestNewHelmTestSuiteCounts(t *testing.T) {
	suite := newHelmTestSuite("kind-kates", []HelmTestResult{
		{Release: "a", Status: "passed"},
		{Release: "b", Status: "failed"},
		{Release: "c", Status: "passed"},
		{Release: "d", Status: "skipped"},
	}, 3*time.Second)
	if suite.Passed != 2 || suite.Failed != 1 || suite.Skipped != 1 {
		t.Fatalf("passed=%d failed=%d skipped=%d, want 2 1 1", suite.Passed, suite.Failed, suite.Skipped)
	}
	if suite.Cluster != "kind-kates" || suite.Duration != 3*time.Second || len(suite.Results) != 4 {
		t.Fatalf("suite = %+v", suite)
	}
}

// The PDF report renders every section: releases with and without hooks,
// a failed hook, a long hook name and a long error.
func TestExportHelmTestPDFWritesAPDF(t *testing.T) {
	suite := newHelmTestSuite("kind-kates", []HelmTestResult{
		{Release: "krafter", Chart: "kafka-cluster-0.4.0", Status: "passed", Hooks: []HelmTestHook{
			{Name: "krafter-test-connection", Phase: "Succeeded", Duration: 2 * time.Second},
		}},
		{Release: "kates", Chart: "kates-1.0.0", Status: "failed", Error: string(bytes.Repeat([]byte("x"), 300)), Hooks: []HelmTestHook{
			{Name: "kates-test-a-hook-name-well-over-forty-five-characters-long", Phase: "Failed", Duration: time.Second},
			{Name: "kates-test-running", Phase: "Running"},
		}},
		{Release: "chaos", Chart: "(override)", Status: "skipped"},
	}, 5*time.Second)
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := exportHelmTestPDF(suite, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", data[:min(len(data), 16)])
	}
}
