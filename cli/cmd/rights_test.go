package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/proc"
)

func TestRightsRowsMergeVerbsAndDropDefaults(t *testing.T) {
	var review rulesReview
	if err := json.Unmarshal([]byte(`{"status":{"resourceRules":[
		{"verbs":["list","get"],"apiGroups":[""],"resources":["pods"]},
		{"verbs":["get","delete"],"apiGroups":[""],"resources":["pods"]},
		{"verbs":["create"],"apiGroups":["authorization.k8s.io"],"resources":["selfsubjectrulesreviews"]},
		{"verbs":["list","get"],"apiGroups":["apps"],"resources":["deployments"],"resourceNames":["kates"]}]}}`), &review); err != nil {
		t.Fatal(err)
	}

	want := [][]string{{"deployments.apps", "get, list", "kates"}, {"pods", "delete, get, list", ""}}
	if got := rightsRows(&review); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// A review the API server refuses is an error: printing "Nothing in kafka"
// would say the backend has no rights there.
func TestRightsRefusedReviewIsAnError(t *testing.T) {
	f := stubRunner(t)
	f.On("kubectl get deployment -A", `{"items":[{"metadata":{"name":"kates","namespace":"kates"}}]}`)
	f.On("kubectl get namespace", "kafka")
	f.Handle("kubectl create", func(proc.Call) ([]byte, error) { return nil, errors.New("cannot impersonate") })

	err := runRights(t.Context())
	if err == nil || !strings.Contains(err.Error(), "system:serviceaccount:kates:default") {
		t.Fatalf("err = %v", err)
	}
}

func TestRightsAsksAsTheDeploymentsServiceAccountInEachNamespace(t *testing.T) {
	f := stubRunner(t)
	f.On("kubectl get deployment -A", `{"items":[{"metadata":{"name":"kates","namespace":"team"},
		"spec":{"template":{"spec":{"serviceAccountName":"api"}}}}]}`)
	f.On("kubectl get namespace", "team kafka")
	f.On("kubectl create", `{"status":{"resourceRules":[{"verbs":["get"],"apiGroups":[""],"resources":["pods"]}]}}`)

	if err := runRights(t.Context()); err != nil {
		t.Fatal(err)
	}
	var asked []string
	for _, c := range f.Calls() {
		if c.Args[0] == "create" {
			if !strings.Contains(c.String(), "--as=system:serviceaccount:team:api") {
				t.Errorf("call = %s", c)
			}
			asked = append(asked, c.Stdin)
		}
	}
	if len(asked) != 2 || !strings.Contains(asked[0], `"kafka"`) || !strings.Contains(asked[1], `"team"`) {
		t.Errorf("reviews = %v", asked)
	}
}
