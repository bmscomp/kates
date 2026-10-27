package strimzi

import (
	"context"
	"strings"
	"testing"
)

func TestListReleases(t *testing.T) {
	r := newFakeRunner(t)
	r.outputs["helm list -A -o json"] = helmListJSON
	r.outputs["helm list -n kafka -o json"] = `[{"name":"kafka","namespace":"kafka","status":"pending-upgrade","chart":"kafka-cluster-0.9.0"}]`
	r.outputs["helm list -n empty -o json"] = ""
	r.outputs["helm list -n broken -o json"] = "Error: not json"

	all, err := ListReleases(context.Background(), r, "")
	if err != nil {
		t.Fatalf("ListReleases(-A): %v", err)
	}
	if len(all) != 4 || all[0].Name != "kates" || all[0].AppVersion != "0.4.0" || !all[0].Deployed() {
		t.Errorf("ListReleases(-A) = %+v", all)
	}

	kafka, err := ListReleases(context.Background(), r, "kafka")
	if err != nil {
		t.Fatalf("ListReleases(kafka): %v", err)
	}
	if len(kafka) != 1 || kafka[0].Deployed() {
		t.Errorf("a pending-upgrade release reads as deployed: %+v", kafka)
	}

	if empty, err := ListReleases(context.Background(), r, "empty"); err != nil || len(empty) != 0 {
		t.Errorf("empty output: %v, %v", empty, err)
	}
	if _, err := ListReleases(context.Background(), r, "broken"); err == nil || !strings.Contains(err.Error(), "parse helm list output") {
		t.Errorf("unparseable output: err = %v", err)
	}
	r.assertCalls("helm list -A -o json", "helm list -n kafka -o json", "helm list -n empty -o json", "helm list -n broken -o json")
}
