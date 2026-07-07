package helm

import "testing"

func TestBuildArgs(t *testing.T) {
	c := &Client{Namespace: "kafka"}
	args := c.buildArgs([]string{"status", "krafter"})
	if len(args) != 4 || args[0] != "-n" || args[1] != "kafka" {
		t.Errorf("expected -n kafka prepended, got %v", args)
	}

	c2 := &Client{}
	args2 := c2.buildArgs([]string{"list"})
	if len(args2) != 1 {
		t.Errorf("expected no extra args, got %v", args2)
	}
}
