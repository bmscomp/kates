package kubectl

import "testing"

func TestBuildArgs(t *testing.T) {
	c := &Client{Context: "test-ctx"}
	args := c.buildArgs([]string{"get", "pods"})
	if len(args) != 4 || args[0] != "--context" || args[1] != "test-ctx" {
		t.Errorf("expected --context prepended, got %v", args)
	}

	c2 := &Client{}
	args2 := c2.buildArgs([]string{"get", "pods"})
	if len(args2) != 2 {
		t.Errorf("expected no extra args, got %v", args2)
	}
}
